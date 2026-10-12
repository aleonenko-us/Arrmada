import { req } from "./api";

// Book authors as rows of their own (/api/v1/book-authors, manager only). Its own module,
// not part of lib/api.ts: only the staff Books pages import it, so none of it lands in a
// requester's first load.

export interface LibraryAuthor {
  id: number;
  name: string;
  /** The catalogue's id for the person: Open Library "OL…A" or Hardcover "hc:a:<id>"; "" = not chosen yet. */
  catalogue_key: string;
  image_url?: string;
  bio?: string;
  /** Followed: new books are added on a schedule when monitor_new is on too. */
  monitored: boolean;
  monitor_new: boolean;
  /** Profile for books added for this author; "" = the default book profile. */
  quality_profile: string;
  /** When the weekly new-books check last looked at the author's catalogue (SQLite UTC); absent = not yet. */
  works_checked_at?: string;
  /** How many books that check has added. */
  new_books_found?: number;
  books: number;
  owned: number;
  wanted: number;
  /** The spellings on the author's books, most used first. */
  names?: string[];
}

export type LibraryAuthorPatch = Partial<Pick<LibraryAuthor, "monitored" | "monitor_new" | "quality_profile" | "catalogue_key">>;

export const bookAuthorApi = {
  list: () => req<{ authors: LibraryAuthor[] }>("/api/v1/book-authors").then((r) => r.authors),
  /** The library authors a name can mean: one is that author, several need a person to choose. */
  named: (name: string) => req<{ authors: LibraryAuthor[] }>(`/api/v1/book-authors?name=${encodeURIComponent(name)}`).then((r) => r.authors),
  get: (id: number) => req<LibraryAuthor>(`/api/v1/book-authors/${id}`),
  /** Picking a catalogue key another author row holds joins the two: the answer is the row that stays. */
  update: (id: number, patch: LibraryAuthorPatch) =>
    req<LibraryAuthor>(`/api/v1/book-authors/${id}`, { method: "PUT", body: JSON.stringify(patch) }),
};
