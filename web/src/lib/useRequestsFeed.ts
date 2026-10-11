import type { MediaRequest } from "./api";
import { useLiveRefresh } from "./useLiveRefresh";

// One live feed for every request view — the Discover strips, the Requests page and the
// bell — instead of each on its own timer.
//
// The server announces a request changing (made, decided, grabbed, imported, ready,
// withdrawn) as request.updated to staff and user.<id>.request.updated to the requester
// and each follower: ids and status only, and a requester's socket never carries anyone
// else's (internal/realtime/policy.go). A view refetches its own list on either.
//
// Between events a view polls only as a backstop: every 10 s while something it shows is
// moving on its own (download progress has no requester-safe event), otherwise once a
// minute — and never in a hidden tab.

export const REQUEST_TOPICS = ["request.updated", "user.*.request.updated"] as const;
export const FEED_MOVING_MS = 10_000;
export const FEED_IDLE_MS = 60_000;

// Stages that move with no event: download progress, the import, waiting for Plex.
const FEED_MOVING = new Set(["downloading", "importing", "queued", "adding"]);

/** Whether any of items is moving on its own (so the backstop should be quick). */
export function feedMoving(items: readonly MediaRequest[] | null | undefined): boolean {
  return (items ?? []).some((rq) => FEED_MOVING.has(rq.tracking?.stage ?? ""));
}

/**
 * useRequestsFeed keeps a request view fresh: load runs on a request event (debounced)
 * and on the backstop poll. It doesn't load on mount — the view does, once per query.
 */
export function useRequestsFeed(load: () => Promise<unknown> | void, items: readonly MediaRequest[] | null | undefined, enabled = true): () => void {
  return useLiveRefresh(load, { topics: REQUEST_TOPICS, fallbackMs: feedMoving(items) ? FEED_MOVING_MS : FEED_IDLE_MS, enabled });
}
