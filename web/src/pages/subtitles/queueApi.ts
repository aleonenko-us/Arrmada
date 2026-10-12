import { req, type SubtitleJob } from "../../lib/api";

// The Subtitles queue's calls: the live list with the AI backlog, the saved history, per-lane
// clearing and a row's retry. They live here, in the Subtitles page's own chunk, not in
// lib/api.ts, which every page (the requester's first load included) pays for.

export type SubtitleLane = "fast" | "gpu";
export type SubtitleJobState = SubtitleJob["state"];

/** GET /subtitles/jobs: every queued and running job plus recent ones, and the AI backlog. */
export interface SubtitleQueue {
  jobs: SubtitleJob[];
  gpu_backlog: number; // GPU-lane jobs queued or running
  gpu_backlog_audio_sec: number; // audio still to go through whisper (a pass per language)
  gpu_eta_sec: number; // 0 = unknown: no speed measured yet
  rtf: number; // whisper's measured realtime factor
}

export interface SubtitleJobPage { jobs: SubtitleJob[]; next_before: number }

export const queueApi = {
  live: () => req<SubtitleQueue>("/api/v1/subtitles/jobs"),
  // The saved history (the last 500 finished jobs), newest first; before is the previous
  // page's next_before.
  history: (p: { state?: SubtitleJobState; before?: number; limit?: number } = {}) => {
    const q = new URLSearchParams({ limit: String(p.limit ?? 30) });
    if (p.state) q.set("state", p.state);
    if (p.before) q.set("before", String(p.before));
    return req<SubtitleJobPage>(`/api/v1/subtitles/jobs?${q.toString()}`);
  },
  // lane: just that lane's queued jobs ("gpu" = the AI backlog); omitted, both.
  clear: (lane?: SubtitleLane) =>
    req<{ cleared: number }>(`/api/v1/subtitles/jobs/clear${lane ? `?lane=${lane}` : ""}`, { method: "POST" }),
  // Retry queues the job's file again from a button (manual priority): every kept language
  // still missing gets another go, skipping the sweep's backoff.
  retry: (j: SubtitleJob) =>
    j.kind === "episode"
      ? req<SubtitleJob>(`/api/v1/subtitles/library/episodes/${j.series_id}/${j.season}/${j.episode}`, { method: "POST" })
      : req<SubtitleJob>(`/api/v1/subtitles/library/movies/${j.movie_id}`, { method: "POST" }),
};
