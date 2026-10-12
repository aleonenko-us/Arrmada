import type { Book } from "../../lib/api";
import { compareValues } from "../../features/library/sort";

// The Books library's orders. One ?sort= value serves both views: "author" is the default
// for both (the order the library always had), and a value only one view offers falls
// back to it in the other.
export const SORT_KEYS = ["author", "title", "added", "year", "release", "books"] as const;
export type BookSort = (typeof SORT_KEYS)[number];
export const DEFAULT_SORT: BookSort = "author";

export const BOOK_SORTS: { value: BookSort; label: string }[] = [
  { value: "author", label: "Author" },
  { value: "title", label: "Title" },
  { value: "added", label: "Recently added" },
  { value: "year", label: "Year" },
  { value: "release", label: "Release date" },
];
export const AUTHOR_SORTS: { value: BookSort; label: string }[] = [
  { value: "author", label: "Name" },
  { value: "added", label: "Recently added" },
  { value: "books", label: "Most books" },
];

// sortFor is the order a view really uses for a ?sort= value.
export function sortFor(mode: "author" | "book", key: BookSort): BookSort {
  const offered = mode === "author" ? AUTHOR_SORTS : BOOK_SORTS;
  return offered.some((o) => o.value === key) ? key : DEFAULT_SORT;
}

const byTitle = (a: Book, b: Book) => a.title.localeCompare(b.title);

// sortBooks orders the book view. Newest first for dates and years; a book that doesn't
// know one (no release date from the catalogue) goes last, and ties fall back to the title.
export function sortBooks(list: Book[], key: BookSort): Book[] {
  const out = [...list];
  switch (key) {
    case "title": return out.sort(byTitle);
    case "added": return out.sort((a, b) => compareValues(a.added_at || undefined, b.added_at || undefined, "desc") || byTitle(a, b));
    case "year": return out.sort((a, b) => compareValues(a.year || undefined, b.year || undefined, "desc") || byTitle(a, b));
    case "release": return out.sort((a, b) => compareValues(a.release_date || undefined, b.release_date || undefined, "desc") || byTitle(a, b));
    default: return out.sort((a, b) => (a.author || "").localeCompare(b.author || "") || byTitle(a, b));
  }
}

// sortAuthors orders the author tiles: by name, by their newest book, or by how many
// books they have.
export function sortAuthors<G extends { name: string; books: Book[] }>(groups: G[], key: BookSort): G[] {
  const newest = (g: G) => g.books.reduce<string | undefined>((m, b) => (b.added_at && (!m || b.added_at > m) ? b.added_at : m), undefined);
  const byName = (a: G, b: G) => a.name.localeCompare(b.name);
  const out = [...groups];
  switch (key) {
    case "added": return out.sort((a, b) => compareValues(newest(a), newest(b), "desc") || byName(a, b));
    case "books": return out.sort((a, b) => b.books.length - a.books.length || byName(a, b));
    default: return out.sort(byName);
  }
}
