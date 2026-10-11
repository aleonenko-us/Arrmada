import { describe, expect, it } from "vitest";
import { dayLabel, eventHref, groupByDay, mergeFirstPage } from "./events";

const now = new Date(2026, 9, 8, 15, 0); // Thu 8 Oct 2026, local time

describe("dayLabel", () => {
  it("says Today and Yesterday, then the weekday and date", () => {
    expect(dayLabel(new Date(2026, 9, 8, 0, 5).getTime(), now)).toBe("Today");
    expect(dayLabel(new Date(2026, 9, 7, 23, 59).getTime(), now)).toBe("Yesterday");
    const older = dayLabel(new Date(2026, 9, 5, 12).getTime(), now);
    expect(older).toMatch(/Mon/);
    expect(older).toMatch(/5/);
    expect(older).not.toMatch(/2026/);
  });
  it("adds the year for another year", () => {
    expect(dayLabel(new Date(2025, 11, 30, 12).getTime(), now)).toMatch(/2025/);
  });
});

describe("groupByDay", () => {
  it("keeps the newest-first order and starts a group per day", () => {
    const at = (d: number, h: number) => ({ at_ms: new Date(2026, 9, d, h).getTime() });
    const groups = groupByDay([at(8, 14), at(8, 9), at(7, 20), at(5, 1)], now);
    expect(groups.map((g) => [g.label === "Today" || g.label === "Yesterday" ? g.label : "older", g.items.length])).toEqual([
      ["Today", 2], ["Yesterday", 1], ["older", 1],
    ]);
  });
});

describe("eventHref", () => {
  it("links each kind to its page; music to the artist", () => {
    expect(eventHref("movie", 1)).toBe("/movies/1");
    expect(eventHref("series", 2)).toBe("/series/2");
    expect(eventHref("book", 3)).toBe("/books/3");
    expect(eventHref("music", 4)).toBe("/music/4");
  });
});

describe("mergeFirstPage", () => {
  const ev = (kind: string, id: number) => ({ kind, id });
  it("puts what's new on top and keeps the pages Load more fetched", () => {
    const loaded = [ev("movie", 5), ev("series", 4), ev("movie", 3), ev("book", 2), ev("movie", 1)];
    const fresh = [ev("movie", 6), ev("movie", 5), ev("series", 4)]; // a full page of 3
    expect(mergeFirstPage(fresh, loaded, true)).toEqual([ev("movie", 6), ev("movie", 5), ev("series", 4), ev("movie", 3), ev("book", 2), ev("movie", 1)]);
  });
  it("takes a short fresh page as the whole list (something was deleted, or nothing more)", () => {
    expect(mergeFirstPage([ev("movie", 6)], [ev("movie", 5)], false)).toEqual([ev("movie", 6)]);
  });
  it("can't stitch a full page that reaches nothing loaded", () => {
    expect(mergeFirstPage([ev("movie", 9), ev("movie", 8)], [ev("movie", 2), ev("movie", 1)], true)).toBeNull();
  });
});
