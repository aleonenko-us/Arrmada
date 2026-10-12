import { req, type SubLangStatus, type SubTrack, type SubtitleAttempt, type SubtitleJob, type SubtitleResult } from "./api";

// The per-file subtitle drawer's calls (GET /subtitles/file, shift, delete). Staff-only,
// and only the drawer and the detail pages' subtitle status import this module, so it
// rides in their lazy chunks rather than lib/api.ts, which every page (the requester's
// first load included) pays for.

/** One video, named by ids: the server finds the path, the client never sends one. */
export type SubMediaRef =
  | { kind: "movie"; movie_id: number }
  | { kind: "episode"; series_id: number; season: number; episode: number };

export interface SubCuePreview { start_ms: number; end_ms: number; text: string }

/** A subtitle file paired with the video, joined with where it came from. */
export interface SubSidecar {
  name: string;
  lang: string; // "" = untagged <base>.srt
  variant?: "" | "forced" | "sdh";
  ext: string;
  size: number;
  modified_at: number;
  cue_count: number;
  first_cues: SubCuePreview[];
  shiftable: boolean; // SRT: the nudges can rewrite it
  owned: boolean; // Arrmada wrote or adopted it
  source: string; // embedded | release | opensubtitles | ai-transcribe | ai-translate | adopted | manual | unknown
  provider?: string;
  provider_file_id?: string;
  release?: string;
  hash_match?: boolean;
  score?: number;
  model?: string;
  sync_offset_ms?: number; // running total of shifts applied
  sync_confidence?: number; // the Health score; absent = not checked
  locked?: boolean;
  embedded_index?: number;
  created_at?: number;
}

export interface SubAudioTrack { index: number; lang?: string; title?: string; default?: boolean }
export interface SubOrphan { name: string; lang?: string; variant?: string }
export interface SubBackoff {
  lang: string; variant?: string; primary_reason: SubtitleResult; detail?: string; count: number;
  next_retry_at?: number; clear_on?: string[]; updated_at: number;
}

/** GET /subtitles/file: one video's whole subtitle picture. */
export interface SubFileView {
  kind: "movie" | "episode";
  movie_id?: number; series_id?: number; season?: number; episode?: number;
  media_key: string;
  title: string;
  video_path: string;
  kept: string[];
  languages: SubLangStatus[];
  audio: SubAudioTrack[];
  embedded: SubTrack[];
  sidecars: SubSidecar[];
  orphans: SubOrphan[];
  backoffs: SubBackoff[];
  attempts: SubtitleAttempt[]; // the newest per language, variant and rung
  jobs: SubtitleJob[]; // queued and running for this file
}

function refQuery(m: SubMediaRef): URLSearchParams {
  return m.kind === "movie"
    ? new URLSearchParams({ kind: "movie", id: String(m.movie_id) })
    : new URLSearchParams({ kind: "episode", series: String(m.series_id), season: String(m.season), episode: String(m.episode) });
}

export function subMediaKey(m: SubMediaRef): string {
  return m.kind === "movie" ? `m:${m.movie_id}` : `e:${m.series_id}:${m.season}:${m.episode}`;
}

export const subtitleFileApi = {
  view: (m: SubMediaRef) => req<SubFileView>(`/api/v1/subtitles/file?${refQuery(m).toString()}`),
  // offsetMs > 0 moves every cue later.
  shift: (m: SubMediaRef, name: string, offsetMs: number) =>
    req<SubSidecar>("/api/v1/subtitles/file/shift", { method: "POST", body: JSON.stringify({ ...m, name, offset_ms: offsetMs }) }),
  // To the recycle bin, never deleted outright; a bin failure leaves the file and errors.
  remove: (m: SubMediaRef, name: string) => {
    const q = refQuery(m);
    q.set("name", name);
    return req<{ removed: string }>(`/api/v1/subtitles/file?${q.toString()}`, { method: "DELETE" });
  },
};
