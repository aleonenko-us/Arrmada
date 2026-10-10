import type { LiveEvent } from "./live";

// The download queue's two live topics (staff-only, hashes and numbers only):
//  - queue.progress: every unfinished download's progress, ETA and phase, each time a bar
//    has visibly moved (internal/download/progress.go). A list moves its bars from it
//    instead of polling.
//  - queue.changed: hashes that arrived, left or changed phase (finished, paused,
//    errored). A list refetches on it, since what it shows has changed shape.

export const QUEUE_PROGRESS = "queue.progress";
export const QUEUE_CHANGED = "queue.changed";

export interface QueueProgressItem {
  hash: string;
  progress: number; // 0..1, rounded to half a percent
  eta_seconds: number;
  state: string; // the download's phase: downloading, stalled, queued, …
}

/** queue.progress's items by lower-cased hash, or null for any other event. */
export function progressByHash(ev: LiveEvent): Map<string, QueueProgressItem> | null {
  if (ev.topic !== QUEUE_PROGRESS || !Array.isArray(ev.data)) return null;
  const out = new Map<string, QueueProgressItem>();
  for (const it of ev.data as QueueProgressItem[]) {
    if (it && typeof it.hash === "string" && typeof it.progress === "number") out.set(it.hash.toLowerCase(), it);
  }
  return out;
}

/** Whether a queue.changed event names any of hashes. */
export function queueChangedFor(ev: LiveEvent, hashes: Iterable<string>): boolean {
  if (ev.topic !== QUEUE_CHANGED) return false;
  const d = ev.data as { added?: string[]; removed?: string[]; changed?: string[] } | null;
  const moved = new Set([...(d?.added ?? []), ...(d?.removed ?? []), ...(d?.changed ?? [])].map((h) => h.toLowerCase()));
  for (const h of hashes) if (moved.has(h.toLowerCase())) return true;
  return false;
}

/**
 * patchProgress moves the bars of the downloads a queue.progress event covers, and
 * returns the same array when nothing changed (so React skips the render). get/set read
 * and write each row's download; rows without a hash are left alone.
 */
export function patchProgress<R>(
  rows: R[],
  live: Map<string, QueueProgressItem>,
  get: (r: R) => { hash?: string; progress: number; phase?: string } | null | undefined,
  set: (r: R, p: QueueProgressItem) => R,
): R[] {
  let changed = false;
  const out = rows.map((r) => {
    const d = get(r);
    const p = d?.hash ? live.get(d.hash.toLowerCase()) : undefined;
    if (!d || !p || (p.progress === d.progress && (d.phase === undefined || p.state === d.phase))) return r;
    changed = true;
    return set(r, p);
  });
  return changed ? out : rows;
}
