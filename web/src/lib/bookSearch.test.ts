import { describe, expect, it } from "vitest";
import { editionsHeld, wantedCopy, wantedTitle } from "./bookSearch";

describe("editionsHeld", () => {
  it("names the editions on disk in full", () => {
    expect(editionsHeld({ ebook: {}, audiobook: {} })).toBe("Ebook · Audiobook");
    expect(editionsHeld({ ebook: {} })).toBe("Ebook");
    expect(editionsHeld({ audiobook: {} })).toBe("Audiobook");
    expect(editionsHeld({})).toBe("");
  });
});

describe("wantedCopy", () => {
  const now = Date.parse("2026-10-09T12:00:00Z");
  const base = { monitored: true, search_misses: 0 };
  it("says searching for a book with no misses yet", () => {
    expect(wantedCopy(base, "Ebook", now)).toBe("Wanted — searching.");
  });
  it("says not found yet, with the last and next check, once searches have missed", () => {
    const b = { ...base, search_misses: 3, last_search_at: "2026-10-06T12:00:00Z", next_search_at: "2026-10-13T12:00:00Z" };
    const line = wantedCopy(b, "Ebook", now);
    expect(line.startsWith("Not found yet — last checked 3d ago, next check ")).toBe(true);
    expect(line).not.toContain("Arrmada is searching");
    expect(line).not.toContain("soon");
  });
  it("doesn't claim to search an unmonitored book", () => {
    expect(wantedCopy({ ...base, monitored: false }, "Audiobook", now)).toBe("Wanted — not monitored, so the audiobook isn't being searched for.");
  });
});

describe("wantedTitle", () => {
  const now = Date.parse("2026-10-09T12:00:00Z");
  it("gives the next check for a book searches keep missing", () => {
    expect(wantedTitle({ monitored: true, search_misses: 2, next_search_at: "2026-10-08T00:00:00Z" }, now)).toBe("Not found yet — next check soon");
    expect(wantedTitle({ monitored: true, search_misses: 0 }, now)).toBe("Searching");
    expect(wantedTitle({ monitored: false, search_misses: 4 }, now)).toBeUndefined();
  });
});

describe("upcoming books", () => {
  const now = new Date(2026, 9, 9, 12).getTime(); // 9 Oct 2026, the viewer's own day
  it("says when an upcoming book will be searched, not that it's searching", () => {
    const b = { monitored: true, search_misses: 2, release_date: "2027-03-12" };
    const line = wantedCopy(b, "Ebook", now);
    expect(line.startsWith("Not out yet — we'll search on ")).toBe(true);
    expect(line).toMatch(/12/);
    expect(line).toMatch(/Mar/);
    expect(line).toMatch(/2027/); // not this year, so the year is said
    expect(wantedTitle(b, now)?.startsWith("Out ")).toBe(true);
  });
  it("treats a book already out as any other", () => {
    expect(wantedCopy({ monitored: true, search_misses: 0, release_date: "2026-10-01" }, "Ebook", now)).toBe("Wanted — searching.");
  });
});
