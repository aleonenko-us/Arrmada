import { formatAgo } from "./format";

// followLine is the author page's line about the weekly new-books check for a followed
// author: when it last looked and how many books it has added. The server stores its
// stamp as SQLite UTC ("2026-10-13 06:00:00"), read here as UTC.
export function followLine(a: { works_checked_at?: string; new_books_found?: number }, now = Date.now()): string {
  const ms = a.works_checked_at ? Date.parse(a.works_checked_at.replace(" ", "T") + (a.works_checked_at.endsWith("Z") ? "" : "Z")) : NaN;
  const last = Number.isFinite(ms) ? `last checked ${formatAgo(ms, now)}` : "first check soon";
  const n = a.new_books_found ?? 0;
  const found = n > 0 ? ` · ${n} new book${n === 1 ? "" : "s"} added so far` : "";
  return `Checks weekly for new books — ${last}${found}`;
}
