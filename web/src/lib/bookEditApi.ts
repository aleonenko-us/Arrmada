import { req, type Book, type BookEditField } from "./api";

// Fixing a book's details (staff, the book page): hand edits over the catalogue's details,
// and Change match. Its own module, not part of lib/api.ts, so none of it lands in a
// requester's first load.

/** One save of the Edit details dialog. */
export interface BookEditRequest {
  set?: Partial<Record<BookEditField, string | number | string[]>>;
  reset?: BookEditField[];
  reset_all?: boolean;
}

export interface BookEditResult {
  book: Book;
  /** The details whose shown value changed. */
  changed: BookEditField[];
  /** The edit changed where the book naming would put its files: offer the rename preview. */
  rename_available: boolean;
}

export const bookEditApi = {
  editDetails: (id: number, body: BookEditRequest) =>
    req<BookEditResult>(`/api/v1/books/${id}/details`, { method: "PUT", body: JSON.stringify(body) }),
  // Re-point a book at a different catalogue entry, keeping its files and settings. Details
  // edited by hand are kept (the new entry's go underneath) unless edits is "clear".
  rematch: (id: number, body: { ol_key: string; title: string; author: string; year: number; cover_url: string; edits?: "keep" | "clear" }) =>
    req<Book>(`/api/v1/books/${id}/rematch`, { method: "POST", body: JSON.stringify(body) }),
};
