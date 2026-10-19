import { req, type JobRef } from "./api";

// Music placement (staff): the music naming templates' presets and live preview, an
// album's rename preview and apply, the library-wide rename, and an album's retag. Its own
// module, not part of lib/api.ts: only the Settings → Naming & metadata card and the album
// page load it, so none of it lands in a requester's first load.

/** The album folder ("/" between levels), a track's file, and a compilation's track file. */
export interface MusicNaming {
  folder: string;
  track: string;
  compilation: string;
}

export interface MusicNamingPreset {
  id: string;
  label: string;
  description: string;
  naming: MusicNaming;
}

export interface MusicNamingSample {
  label: string;
  /** Relative to the music folder, "/" between levels. */
  paths: string[];
}

/** A preview: the samples rendered, or which template has a problem and why. */
export interface MusicNamingPreview {
  samples: MusicNamingSample[];
  error?: { field: "folder" | "track" | "compilation"; message: string };
}

/** One proposed move. track_id 0 is an extra (artwork, a booklet, lyrics) moving with the album. */
export interface MusicRenameItem {
  album_id: number;
  album?: string;
  artist?: string;
  track_id?: number;
  from: string;
  to: string;
  conflict?: string;
}

export interface MusicRenameResult {
  renamed: number;
  skipped: { from: string; to: string; reason: string }[];
}

export interface MusicOrganizePreview {
  counts: { rename: number; conflict: number; albums: number };
  items: MusicRenameItem[];
  total: number;
  offset: number;
  token: string;
}

export interface MusicOrganizeSummary {
  albums: number;
  renamed: number;
  skipped: number;
}

export interface MusicRetagResult {
  retagged: number;
  skipped: number;
  failed: number;
  notes?: string[];
}

export const musicNamingApi = {
  presets: () => req<{ presets: MusicNamingPreset[]; tokens: string[] }>("/api/v1/music/naming/presets"),
  // Renders the sample albums with templates that may not be saved yet.
  preview: (n: MusicNaming, signal?: AbortSignal) => {
    const q = new URLSearchParams({ folder: n.folder, track: n.track, compilation: n.compilation });
    return req<MusicNamingPreview>(`/api/v1/music/naming/preview?${q.toString()}`, { signal });
  },
  // An album's moves under the current naming; nothing moves.
  albumRenamePreview: (albumId: number) => req<{ items: MusicRenameItem[] }>(`/api/v1/music/albums/${albumId}/rename`),
  // Applies exactly the moves sent back (the ones previewed); anything changed since is skipped.
  albumRename: (albumId: number, items: MusicRenameItem[]) =>
    req<MusicRenameResult>(`/api/v1/music/albums/${albumId}/rename`, { method: "POST", body: JSON.stringify({ items }) }),
  // The dry run over every album with files; nothing moves.
  organizePreview: (offset = 0, limit = 200) => req<MusicOrganizePreview>(`/api/v1/music/organize?offset=${offset}&limit=${limit}`),
  // Applies exactly the previewed plan as a background job; a plan that changed answers 409.
  organizeApply: (token: string) =>
    req<{ status: string; albums: number } & JobRef>("/api/v1/music/organize", { method: "POST", body: JSON.stringify({ token }) }),
  // Writes the house tags into an album's files: staged copies swapped in, originals to the bin.
  retag: (albumId: number) => req<{ status: string } & JobRef>(`/api/v1/music/albums/${albumId}/retag`, { method: "POST" }),
};
