import { useCallback, useEffect, useRef } from "react";
import { useQuery, type QueryResult } from "./query";
import { usePoll } from "./usePoll";
import { useLiveEvent, useOnReconnect, type LiveEvent } from "./live";

export interface LiveRefreshOptions {
  /** Topics (lib/live topicMatches patterns) that mean this data changed. */
  topics: readonly string[];
  /** Only events this accepts trigger a refresh (e.g. one movie's id). */
  filter?: (ev: LiveEvent) => boolean;
  /** The slow backstop poll, for events the socket dropped. Default 30 s; null for none. */
  fallbackMs?: number | null;
  /** A burst of events within this window makes one request. Default 500 ms. */
  debounceMs?: number;
  enabled?: boolean;
  /** Also run fn on mount (a page keeping its own state). useLiveQuery leaves this to useQuery. */
  immediate?: boolean;
}

export interface LiveQueryOptions extends LiveRefreshOptions {
  staleMs?: number;
}

// A reconnect right after a request (the tab coming back into view does both) needn't ask
// again.
const RECONNECT_GRACE_MS = 3000;

/**
 * useLiveRefresh runs fn when the server says the data moved, instead of on a fast poll:
 *  - a matching topic runs it, debounced, so a sweep's burst is one request;
 *  - a slow fallback poll covers anything the socket dropped;
 *  - the socket coming back after a drop runs it;
 *  - a hidden tab makes no requests (it catches up the moment it's shown, via usePoll).
 * fn is read from a ref, like usePoll's. It returns "refresh now" for the page's own use.
 */
export function useLiveRefresh(fn: () => Promise<unknown> | void, opts: LiveRefreshOptions): () => void {
  const { topics, filter, fallbackMs = 30_000, debounceMs = 500, enabled = true, immediate = false } = opts;
  const fnRef = useRef(fn);
  const filterRef = useRef(filter);
  useEffect(() => { fnRef.current = fn; filterRef.current = filter; });
  const lastAt = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const missed = useRef(false);
  const now = useCallback(() => {
    clearTimeout(timer.current);
    timer.current = undefined;
    missed.current = false;
    lastAt.current = Date.now();
    return fnRef.current();
  }, []);

  usePoll(now, enabled ? fallbackMs : null, { immediate });

  // An event that landed while the tab was hidden is owed a refresh when it's shown.
  // usePoll pays it when there is a fallback poll; without one, this does.
  useLiveEvent(topics, (ev) => {
    if (filterRef.current && !filterRef.current(ev)) return;
    if (timer.current !== undefined) return; // one request for the burst
    timer.current = setTimeout(() => {
      timer.current = undefined;
      if (document.visibilityState === "hidden") { missed.current = true; return; }
      void now();
    }, debounceMs);
  }, enabled);
  const pollless = fallbackMs == null;
  useEffect(() => {
    if (!enabled || !pollless) return;
    const onVisible = () => {
      if (document.visibilityState === "hidden" || !missed.current) return;
      missed.current = false;
      void now();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [enabled, pollless, now]);

  useOnReconnect(() => {
    if (Date.now() - lastAt.current < RECONNECT_GRACE_MS || document.visibilityState === "hidden") return;
    void now();
  }, enabled);

  useEffect(() => () => clearTimeout(timer.current), []);
  return useCallback(() => { void now(); }, [now]);
}

/** useLiveQuery is useQuery kept fresh by useLiveRefresh instead of a fast poll. */
export function useLiveQuery<T>(key: string, fetcher: () => Promise<T>, opts: LiveQueryOptions): QueryResult<T> {
  const q = useQuery<T>(key, fetcher, { enabled: opts.enabled, staleMs: opts.staleMs });
  useLiveRefresh(q.refetch, { ...opts, immediate: false });
  return q;
}
