import { describe, expect, it } from "vitest";
import type { Book } from "../../lib/api";
import { sortAuthors, sortBooks, sortFor } from "./sort";

const book = (id: number, title: string, author: string, extra: Partial<Book> = {}): Book => ({
  id, ol_key: `OL${id}W`, title, author, year: 0, monitored: true, quality_profile: "",
  has_file: false, want_ebook: true, want_audiobook: false, search_misses: 0, ...extra,
});

const LIST = [
  book(1, "Dune", "Herbert", { year: 1965, added_at: "2026-01-02 10:00:00" }),
  book(2, "Anathem", "Stephenson", { year: 2008, added_at: "2026-03-01 10:00:00", release_date: "2008-09-09" }),
  book(3, "Children of Dune", "Herbert", { added_at: "2026-02-01 10:00:00", release_date: "2027-01-05" }),
];
const titles = (l: Book[]) => l.map((b) => b.title);

describe("sortBooks", () => {
  it("orders by author then title by default, and by title on request", () => {
    expect(titles(sortBooks(LIST, "author"))).toEqual(["Children of Dune", "Dune", "Anathem"]);
    expect(titles(sortBooks(LIST, "title"))).toEqual(["Anathem", "Children of Dune", "Dune"]);
  });
  it("puts the newest first and anything without a date or year last", () => {
    expect(titles(sortBooks(LIST, "added"))).toEqual(["Anathem", "Children of Dune", "Dune"]);
    expect(titles(sortBooks(LIST, "year"))).toEqual(["Anathem", "Dune", "Children of Dune"]);
    expect(titles(sortBooks(LIST, "release"))).toEqual(["Children of Dune", "Anathem", "Dune"]);
  });
  it("doesn't reorder the list it was given", () => {
    const copy = [...LIST];
    sortBooks(LIST, "title");
    expect(LIST).toEqual(copy);
  });
});

describe("sortAuthors", () => {
  const groups = [
    { name: "Stephenson", books: [LIST[1]] },
    { name: "Herbert", books: [LIST[0], LIST[2]] },
  ];
  it("sorts by name, newest book or book count", () => {
    expect(sortAuthors(groups, "author").map((g) => g.name)).toEqual(["Herbert", "Stephenson"]);
    expect(sortAuthors(groups, "added").map((g) => g.name)).toEqual(["Stephenson", "Herbert"]);
    expect(sortAuthors(groups, "books").map((g) => g.name)).toEqual(["Herbert", "Stephenson"]);
  });
});

describe("sortFor", () => {
  it("falls back to the default for an order the view doesn't offer", () => {
    expect(sortFor("author", "year")).toBe("author");
    expect(sortFor("book", "books")).toBe("author");
    expect(sortFor("book", "release")).toBe("release");
  });
});
