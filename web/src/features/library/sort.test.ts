import { describe, expect, it } from "vitest";
import { legacySortPref, nextSort, sortChoices, sortItems } from "./sort";
import { shortDate } from "./format";
import type { LibraryColumn, LibrarySort } from "./types";

interface Row { id: number; title: string; year?: number; added?: string; codec?: string }

const sorts: LibrarySort<Row>[] = [
  { key: "title", label: "Title" },
  { key: "added", label: "Added", dir: "desc", value: (r) => r.added },
  { key: "year", label: "Year", dir: "desc", value: (r) => r.year },
];
const columns: LibraryColumn<Row>[] = [
  { key: "year", label: "Year", sort: (r) => r.year, cell: () => null },
  { key: "codec", label: "Codec", sort: (r) => r.codec, cell: () => null },
  { key: "notes", label: "Notes", cell: () => null }, // not sortable
];
const byTitle = (a: Row, b: Row) => a.title.localeCompare(b.title);
const rows: Row[] = [
  { id: 1, title: "Bravo", year: 2001, added: "2026-03-01", codec: "x265" },
  { id: 2, title: "Alpha", year: 2019, added: "2026-01-01" },
  { id: 3, title: "Charlie", added: "2026-02-01", codec: "x264" },
];
const titles = (l: Row[]) => l.map((r) => r.title);

describe("sortChoices", () => {
  it("merges the Sort menu with the sortable columns under one key each", () => {
    const c = sortChoices({ sorts, columns });
    expect(c.map((x) => x.key)).toEqual(["title", "added", "year", "codec"]);
    // The menu's direction wins for a key both offer: a header click starts newest-first too.
    expect(c.find((x) => x.key === "year")?.dir).toBe("desc");
    expect(c.find((x) => x.key === "codec")?.dir).toBe("asc");
  });
  it("always offers Title, first", () => {
    expect(sortChoices({ sorts: [], columns })[0]).toMatchObject({ key: "title", dir: "asc" });
  });
});

describe("nextSort", () => {
  it("reverses the same key on a header click and starts another in its own direction", () => {
    expect(nextSort({ key: "year", dir: "desc" }, "year", "desc")).toEqual({ key: "year", dir: "asc" });
    expect(nextSort({ key: "year", dir: "asc" }, "year", "desc")).toEqual({ key: "year", dir: "desc" });
    expect(nextSort({ key: "title", dir: "asc" }, "year", "desc")).toEqual({ key: "year", dir: "desc" });
  });
  it("a menu pick (no toggle) always starts the order in its own direction", () => {
    expect(nextSort({ key: "year", dir: "asc" }, "year", "desc", false)).toEqual({ key: "year", dir: "desc" });
  });
});

describe("sortItems", () => {
  const c = sortChoices({ sorts, columns });
  const by = (k: string) => c.find((x) => x.key === k);
  it("sorts by year and by added, either way, with no value last", () => {
    expect(titles(sortItems(rows, by("year"), "desc", byTitle))).toEqual(["Alpha", "Bravo", "Charlie"]);
    expect(titles(sortItems(rows, by("year"), "asc", byTitle))).toEqual(["Bravo", "Alpha", "Charlie"]);
    expect(titles(sortItems(rows, by("added"), "desc", byTitle))).toEqual(["Bravo", "Charlie", "Alpha"]);
    expect(titles(sortItems(rows, by("added"), "asc", byTitle))).toEqual(["Alpha", "Charlie", "Bravo"]);
  });
  it("sorts by the title (the config's order) when the choice has no value", () => {
    expect(titles(sortItems(rows, by("title"), "asc", byTitle))).toEqual(["Alpha", "Bravo", "Charlie"]);
    expect(titles(sortItems(rows, by("title"), "desc", byTitle))).toEqual(["Charlie", "Bravo", "Alpha"]);
  });
  it("leaves the list it was given alone", () => {
    const copy = [...rows];
    sortItems(rows, by("year"), "asc", byTitle);
    expect(rows).toEqual(copy);
  });
});

describe("legacySortPref", () => {
  const choices = sortChoices({ sorts, columns });
  const store = (m: Record<string, string>) => (k: string) => m[k] ?? null;
  it("keeps the table's sort for someone on the table", () => {
    expect(legacySortPref(store({ "movies.view": "table", "movies.table.sort": "codec:desc", "movies.grid.sort": "year" }), "movies", choices)).toBe("codec:desc");
  });
  it("keeps the grid menu's pick, in its first direction, for someone on the grid", () => {
    expect(legacySortPref(store({ "movies.view": "grid", "movies.table.sort": "codec:desc", "movies.grid.sort": "year" }), "movies", choices)).toBe("year:desc");
  });
  it("has nothing to carry over on a first visit", () => {
    expect(legacySortPref(store({}), "movies", choices)).toBeNull();
  });
});

describe("shortDate", () => {
  it("reads the day from either timestamp form, never shifted by the time zone", () => {
    const want = new Date(2026, 9, 3).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
    expect(shortDate("2026-10-03T23:30:00Z")).toBe(want);
    expect(shortDate("2026-10-03 10:00:00")).toBe(want);
    expect(shortDate(undefined)).toBe("—");
    expect(shortDate("")).toBe("—");
  });
});
