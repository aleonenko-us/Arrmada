import type { HistoryCategory, HistoryKind } from "./api";
import type { Tone } from "../ui";

// Words and links for the per-title event log: Activity → History and the Dashboard's
// Recent activity read the same feed (GET /api/v1/history), so they say the same things.

// eventHref is the page an event's title lives on. Music events belong to the artist.
export function eventHref(kind: HistoryKind, id: number): string {
  switch (kind) {
    case "movie": return `/movies/${id}`;
    case "series": return `/series/${id}`;
    case "book": return `/books/${id}`;
    case "music": return `/music/${id}`;
  }
}

// Each filter category: its chip label and tone. In the order the filter lists them;
// "other" is the bookkeeping (added, refreshed, searched…) the default view leaves out.
export const EVENT_CATEGORIES: { key: HistoryCategory; label: string; tone: Tone }[] = [
  { key: "grabbed", label: "Grabbed", tone: "accent" },
  { key: "imported", label: "Imported", tone: "good" },
  { key: "upgraded", label: "Upgraded", tone: "good" },
  { key: "failed", label: "Failed", tone: "reject" },
  { key: "blocklisted", label: "Blocklisted", tone: "avoid" },
  { key: "removed", label: "Removed", tone: "faint" },
  { key: "seeded", label: "Seeded", tone: "faint" },
  { key: "deleted", label: "Deleted", tone: "avoid" },
  { key: "renamed", label: "Renamed", tone: "faint" },
  { key: "review", label: "Review", tone: "faint" },
  { key: "other", label: "Other", tone: "faint" },
];

const CATEGORY = new Map(EVENT_CATEGORIES.map((c) => [c.key, c]));

export function categoryChip(c: HistoryCategory): { label: string; tone: Tone } {
  return CATEGORY.get(c) ?? { label: c, tone: "faint" };
}

export const HISTORY_KINDS: { key: HistoryKind; label: string }[] = [
  { key: "movie", label: "Movies" },
  { key: "series", label: "Series" },
  { key: "book", label: "Books" },
  { key: "music", label: "Music" },
];

// dayLabel heads a day's group: "Today", "Yesterday", else "Mon 6 Oct" (with the year
// when it isn't this one), in the viewer's own time zone.
export function dayLabel(ms: number, now: Date = new Date()): string {
  const d = new Date(ms);
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(now) - start(d)) / 86_400_000);
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    weekday: "short", day: "numeric", month: "short",
    ...(d.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
  });
}

// groupByDay splits a newest-first list into days, keeping the order.
export function groupByDay<T extends { at_ms: number }>(items: T[], now: Date = new Date()): { label: string; items: T[] }[] {
  const out: { label: string; items: T[] }[] = [];
  for (const it of items) {
    const label = dayLabel(it.at_ms, now);
    const last = out[out.length - 1];
    if (last && last.label === label) last.items.push(it);
    else out.push({ label, items: [it] });
  }
  return out;
}

// eventKey identifies an event across pages (row ids are per kind).
export const eventKey = (e: { kind: string; id: number }) => `${e.kind}:${e.id}`;

// mergeFirstPage folds a fresh first page into what's already loaded: the fresh rows,
// then the older rows Load more had fetched past the point the fresh page reaches, so a
// refresh adds what's new without dropping them. A short fresh page is the whole list on
// its own. null when the two can't be stitched — a full fresh page that reaches nothing
// loaded may leave a gap — and the fresh page should replace the list.
export function mergeFirstPage<T extends { kind: string; id: number }>(fresh: T[], loaded: T[], freshIsFull: boolean): T[] | null {
  if (!freshIsFull || loaded.length === 0) return fresh;
  const at = loaded.findIndex((e) => eventKey(e) === eventKey(fresh[fresh.length - 1]));
  if (at < 0) return null;
  const seen = new Set(fresh.map(eventKey));
  return [...fresh, ...loaded.slice(at + 1).filter((e) => !seen.has(eventKey(e)))];
}
