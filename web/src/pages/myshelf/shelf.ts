import type { MyBook, MyRequest } from "../../lib/api";
import type { StagedRequest } from "../../lib/requestStage";

// The shelf's rows, worked out from GET /me/books: what arrived this week, each series in
// reading order, and the rest. Pure, so the rules can be tested without a page.

const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

// addedMs reads a book's added_at: SQLite's "2026-10-13 09:30:00" (UTC) or RFC 3339.
export function addedMs(at: string | undefined): number {
  if (!at) return NaN;
  return Date.parse(at.includes("T") ? at : at.replace(" ", "T") + "Z");
}

// newThisWeek is what arrived in the last seven days, newest first.
export function newThisWeek(books: readonly MyBook[], now = Date.now()): MyBook[] {
  return books
    .filter((b) => { const t = addedMs(b.added_at); return Number.isFinite(t) && now - t <= WEEK_MS && t <= now + 60_000; })
    .sort((a, b) => addedMs(b.added_at) - addedMs(a.added_at));
}

export interface SeriesGroup {
  name: string;
  books: MyBook[];
}

// seriesGroups gathers the shelf's books by series, each in reading order (numbered ones
// first, by number; then the rest by title). Only a series with two or more books here is
// a row — one book isn't a group. Series are ordered by name.
export function seriesGroups(books: readonly MyBook[]): SeriesGroup[] {
  const by = new Map<string, { name: string; books: MyBook[] }>();
  for (const b of books) {
    const name = b.series_name?.trim();
    if (!name) continue;
    const key = name.toLowerCase();
    const g = by.get(key) ?? { name, books: [] };
    g.books.push(b);
    by.set(key, g);
  }
  return [...by.values()]
    .filter((g) => g.books.length >= 2)
    .map((g) => ({ name: g.name, books: g.books.sort(readingOrder) }))
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
}

function readingOrder(a: MyBook, b: MyBook): number {
  const pa = a.series_position || 0;
  const pb = b.series_position || 0;
  if (pa > 0 && pb > 0 && pa !== pb) return pa - pb;
  if (pa > 0 !== pb > 0) return pa > 0 ? -1 : 1;
  return a.title.localeCompare(b.title, undefined, { sensitivity: "base" });
}

// seriesLabel is "Dune Chronicles #3", or the series alone when its number isn't known.
export function seriesLabel(b: MyBook): string {
  if (!b.series_name) return b.series ?? "";
  return b.series_position ? `${b.series_name} #${b.series_position}` : b.series_name;
}

// asRequest gives a shelf request the shape lib/requestStage reads, so the shelf, the
// Requests page and Discover word the same request the same way.
export function asRequest(r: MyRequest): StagedRequest {
  return { media_type: "book", status: r.status, available: false, tracking: r.tracking, decline_reason: r.decline_reason };
}

// The shelf's "All books" pages in at this many, so a large library stays quick on a phone.
export const PAGE = 120;

// fmtSize is a file size for a button: "1.2 MB", "340 KB".
export function fmtSize(b: number): string {
  if (!b) return "";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = b;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${u[i]}`;
}
