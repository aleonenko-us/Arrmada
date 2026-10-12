import { req, type SubtitleJob } from "../../lib/api";

// Orphaned movie subtitles: adopt one (rename it to pair with the movie's file) or replace
// it (make a fresh one; the orphan goes to the recycle bin once that's written). These
// live here, in the staff pages' own chunks, not in lib/api.ts, which every page (the
// requester's first load included) pays for.

export interface OrphanBulkSkip { movie_id: number; title: string; name: string; reason: string }
export interface OrphanBulkResult { done: number; skipped: OrphanBulkSkip[]; errors: OrphanBulkSkip[] }

export const orphanApi = {
  adopt: (movieId: number, name: string, opts?: { lang?: string; variant?: string }) =>
    req<{ name: string }>(`/api/v1/subtitles/movies/${movieId}/orphans/adopt`, { method: "POST", body: JSON.stringify({ name, ...opts }) }),
  replace: (movieId: number, name: string) =>
    req<SubtitleJob>(`/api/v1/subtitles/movies/${movieId}/orphans/replace`, { method: "POST", body: JSON.stringify({ name }) }),
  // movieIds omitted = every movie with orphans.
  bulk: (action: "adopt" | "replace", movieIds?: number[]) =>
    req<OrphanBulkResult>("/api/v1/subtitles/orphans/bulk", { method: "POST", body: JSON.stringify({ action, movie_ids: movieIds }) }),
};

// bulkSummary is the toast after a bulk action: what happened, and the first reason
// something was left alone.
export function bulkSummary(action: "adopt" | "replace", r: OrphanBulkResult): string {
  const verb = action === "adopt" ? "Adopted" : "Replacing";
  let msg = `${verb} ${r.done} subtitle${r.done === 1 ? "" : "s"}`;
  if (r.skipped.length) msg += ` · ${r.skipped.length} left alone (${r.skipped[0].name}: ${r.skipped[0].reason}${r.skipped.length > 1 ? ", …" : ""})`;
  if (r.errors.length) msg += ` · ${r.errors.length} failed (${r.errors[0].reason})`;
  return msg;
}
