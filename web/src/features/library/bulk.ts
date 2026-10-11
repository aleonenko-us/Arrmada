import type { LibrarySelection } from "./types";

export interface BulkResult<R> {
  /** The answers of the calls that worked. */
  ok: R[];
  /** The ids whose call failed. */
  failed: number[];
  /** The first failure's message, for the toast. */
  error?: string;
}

// runBulk makes one call per selected id, all at once, and waits for every one: a single
// failure no longer hides what worked (Promise.all used to give up on the first error,
// skip the refresh and say nothing). Afterwards the selection keeps only the failures, so
// trying again is one click; when everything worked it clears.
export async function runBulk<T, R>(sel: LibrarySelection<T>, fn: (id: number) => Promise<R>): Promise<BulkResult<R>> {
  const ids = [...sel.ids];
  const results = await Promise.allSettled(ids.map(fn));
  const out: BulkResult<R> = { ok: [], failed: [] };
  results.forEach((r, i) => {
    if (r.status === "fulfilled") out.ok.push(r.value);
    else {
      out.failed.push(ids[i]);
      out.error ??= r.reason instanceof Error ? r.reason.message : String(r.reason);
    }
  });
  if (out.failed.length) sel.set(out.failed);
  else sel.clear();
  return out;
}

// bulkFailure is the toast for a run where some calls failed: "2 of 5 failed (reason) —
// they're still selected."
export function bulkFailure(r: BulkResult<unknown>): string {
  const total = r.ok.length + r.failed.length;
  return `${r.failed.length} of ${total} failed${r.error ? ` (${r.error})` : ""} — they're still selected.`;
}
