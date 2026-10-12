import type { Page, Route } from "@playwright/test";
import type { Book, BookSource } from "../../src/lib/api";

// The admin Books library for BOOK-25 / FE-12: a few books with different authors, years,
// added days and editions, so the sort and the status words have something to show.
const book = (id: number, title: string, author: string, extra: Partial<Book> = {}): Book => ({
  id, ol_key: `OL${id}W`, title, author, year: 2000, monitored: true, quality_profile: "ebook",
  has_file: false, want_ebook: true, want_audiobook: false, search_misses: 0, ...extra,
});

const epub = { path: "/books/x.epub", format: "EPUB", size_bytes: 2_000_000, file_count: 1 };
const m4b = { path: "/audiobooks/x", format: "M4B", size_bytes: 300_000_000, file_count: 1 };

export const LIBRARY: Book[] = [
  book(11, "Dune", "Frank Herbert", { year: 1965, added_at: "2026-01-02 10:00:00", has_file: true, ebook: epub, audiobook: m4b }),
  book(12, "Anathem", "Neal Stephenson", { year: 2008, added_at: "2026-03-01 10:00:00", has_file: true, ebook: epub }),
  book(13, "Children of Dune", "Frank Herbert", { year: 1976, added_at: "2026-02-01 10:00:00" }),
];

export const DETAIL: Book = {
  ...LIBRARY[2],
  description: "The third Dune novel.",
  catalogue: { name: "Open Library", url: "https://openlibrary.org/works/OL13W" },
};

const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });

// mockLibrary answers the Books list (and one book's page) for these tests.
export async function mockLibrary(page: Page, opts: { source?: BookSource; upgradable?: number; dupes?: number } = {}) {
  const state = { searches: 0 };
  await page.route((u) => u.pathname === "/api/v1/books", (route) =>
    json(route, { books: LIBRARY, metadata_available: true, metadata_source: opts.source ?? "openlibrary", upgradable: opts.upgradable ?? 0 }));
  await page.route((u) => u.pathname === "/api/v1/books/13", (route) => json(route, DETAIL));
  await page.route((u) => u.pathname === "/api/v1/books/13/series", (route) => json(route, { name: "", entries: [], gaps: 0 }));
  await page.route((u) => u.pathname === "/api/v1/books/13/history", (route) => json(route, { events: [] }));
  await page.route((u) => u.pathname === "/api/v1/books/13/releases", (route) => json(route, { profile: "ebook", releases: [], searched: 2 }));
  await page.route((u) => u.pathname === "/api/v1/books/lookup", (route) => json(route, { results: [], source: opts.source ?? "openlibrary" }));
  await page.route((u) => u.pathname === "/api/v1/books/13/manualimport", (route) => json(route, { path: "/downloads", candidates: [] }));
  await page.route((u) => u.pathname === "/api/v1/books/13/covers", (route) => json(route, { covers: [] }));
  await page.route((u) => u.pathname === "/api/v1/books/13/search", (route) => { state.searches++; return json(route, { status: "started" }); });
  if (opts.dupes) {
    await page.route((u) => u.pathname === "/api/v1/books/duplicates", (route) =>
      json(route, { groups: Array.from({ length: opts.dupes ?? 0 }, () => ({ books: [], suggested_keep_id: 0, reasons: [] })), past_merges: [] }));
  }
  return state;
}
