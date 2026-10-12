import { describe, expect, it } from "vitest";
import type { MyBook, MyRequest } from "../../lib/api";
import { requestStage } from "../../lib/requestStage";
import { asRequest, newThisWeek, seriesGroups, seriesLabel } from "./shelf";

// APP-19: the shelf's rows — New this week, each series in reading order — and request
// cards worded by the shared stage vocabulary.

const book = (id: number, extra: Partial<MyBook> = {}): MyBook => ({ book_id: id, title: `Book ${id}`, audiobook: false, mine: false, ...extra });
const NOW = Date.parse("2026-10-13T12:00:00Z");

describe("newThisWeek", () => {
  it("keeps what arrived in the last seven days, newest first", () => {
    const got = newThisWeek([
      book(1, { added_at: "2026-10-13 09:00:00" }), // SQLite's UTC stamp: today
      book(2, { added_at: "2026-10-01 09:00:00" }), // twelve days ago
      book(3, { added_at: "2026-10-08T10:00:00Z" }), // RFC 3339, five days ago
      book(4), // never stamped
    ], NOW);
    expect(got.map((b) => b.book_id)).toEqual([1, 3]);
  });
});

describe("seriesGroups", () => {
  it("groups by series in reading order, unnumbered last, single books left out", () => {
    const groups = seriesGroups([
      book(1, { title: "Dune Messiah", series_name: "Dune", series_position: 2 }),
      book(2, { title: "Dune", series_name: "Dune", series_position: 1 }),
      book(3, { title: "Tales of Dune", series_name: "dune" }), // same series, other case, no number
      book(4, { title: "Mistborn", series_name: "Mistborn", series_position: 1 }), // alone: no row
      book(5, { title: "Alpha", series_name: "Aardvark", series_position: 1 }),
      book(6, { title: "Beta", series_name: "Aardvark", series_position: 1.5 }),
    ]);
    expect(groups.map((g) => g.name)).toEqual(["Aardvark", "Dune"]);
    expect(groups[1].books.map((b) => b.title)).toEqual(["Dune", "Dune Messiah", "Tales of Dune"]);
    expect(groups[0].books.map((b) => b.book_id)).toEqual([5, 6]);
  });

  it("labels a book with its number when known", () => {
    expect(seriesLabel(book(1, { series_name: "Dune", series_position: 3 }))).toBe("Dune #3");
    expect(seriesLabel(book(1, { series_name: "Dune" }))).toBe("Dune");
    expect(seriesLabel(book(1, { series: "Old #2" }))).toBe("Old #2");
  });
});

describe("request cards", () => {
  const req = (extra: Partial<MyRequest>): MyRequest => ({ title: "X", status: "approved", requested_at: "2026-10-01", ...extra });

  it("say where a request has got to, as the Requests page does", () => {
    expect(requestStage(asRequest(req({ tracking: { stage: "downloading", progress: 0.4 } }))).detail).toMatch(/^40%/);
    const nf = requestStage(asRequest(req({ tracking: { stage: "searching", next_check_at: "2026-10-14T09:00:00Z", last_search_at: "2026-10-10T09:00:00Z" } })));
    expect(nf.badge).toBe("Searching");
    expect(nf.detail).toMatch(/^Not found yet/);
    expect(requestStage(asRequest(req({ status: "pending", tracking: { stage: "pending" } }))).badge).toBe("Pending");
  });

  it("don't guess a download stage when downloads can't be read", () => {
    expect(requestStage(asRequest(req({ tracking: { stage: "downloading", progress: 0.4 } })), false).badge).toBe("Status unknown");
  });
});
