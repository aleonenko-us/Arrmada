import { describe, expect, it } from "vitest";
import type { MovieEvent, SearchAttempt } from "../../lib/api";
import { eventTime, mergeActivity } from "./ActivityTab";

const attempt = (id: number, at: number, over: Partial<SearchAttempt> = {}): SearchAttempt => ({
  id, media_type: "series", media_id: 1, scope: "S02", trigger: "manual", started_at: at, duration_ms: 1, returned: 0, wrong_title: 0, blocklisted: 0,
  pending: 0, out_of_scope: 0, rejected: 0, eligible: 0, grabbed: 0, reasons: {}, top_reason: "", example: "", grabbed_titles: [], indexer_errors: {},
  outcome: "nothing_found", reason: "", ...over,
});
const iso = (ms: number) => new Date(ms).toISOString();

describe("mergeActivity", () => {
  const T = Date.parse("2026-10-09T12:00:00Z");
  it("merges events and searches newest first, typed for the filters", () => {
    const events: MovieEvent[] = [
      { event: "imported", detail: "S01E01", created_at: iso(T - 1000) },
      { event: "upgraded", detail: "S01E02", created_at: iso(T - 5000) },
      { event: "renumbered", detail: "2 files", created_at: iso(T - 9000) },
      { event: "added", detail: "", created_at: iso(T - 99_000) },
    ];
    const rows = mergeActivity(events, [attempt(7, T - 3000)]);
    expect(rows.map((r) => [r.event, r.type])).toEqual([
      ["imported", "imported"], ["searched", "searched"], ["upgraded", "imported"], ["renumbered", "numbering"], ["added", "other"],
    ]);
    expect(rows[1].detail).toBe("No releases found");
  });
  it("keeps a 'searched' event only from before the oldest stored search", () => {
    const events: MovieEvent[] = [
      { event: "searched", detail: "Season 2: Grabbed nothing", created_at: iso(T - 1000) },
      { event: "searched", detail: "Season 1: Grabbed nothing", created_at: iso(T - 900_000) },
    ];
    const rows = mergeActivity(events, [attempt(7, T - 1000)]);
    expect(rows.map((r) => r.detail)).toEqual(["No releases found", "Season 1: Grabbed nothing"]);
  });
});

describe("eventTime", () => {
  it("reads SQLite's UTC stamps and RFC 3339", () => {
    expect(eventTime("2026-10-08 09:00:00")).toBe(Date.parse("2026-10-08T09:00:00Z"));
    expect(eventTime("2026-10-08T09:00:00Z")).toBe(Date.parse("2026-10-08T09:00:00Z"));
    expect(eventTime("nonsense")).toBe(0);
  });
});
