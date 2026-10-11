import { useQuery, type QueryResult } from "./query";
import { useLiveRefresh, type LiveRefreshOptions } from "./useLiveRefresh";

export { useLiveRefresh, type LiveRefreshOptions } from "./useLiveRefresh";

export interface LiveQueryOptions extends LiveRefreshOptions {
  staleMs?: number;
}

/** useLiveQuery is useQuery kept fresh by useLiveRefresh (lib/useLiveRefresh) instead of a fast poll. */
export function useLiveQuery<T>(key: string, fetcher: () => Promise<T>, opts: LiveQueryOptions): QueryResult<T> {
  const q = useQuery<T>(key, fetcher, { enabled: opts.enabled, staleMs: opts.staleMs });
  useLiveRefresh(q.refetch, { ...opts, immediate: false });
  return q;
}
