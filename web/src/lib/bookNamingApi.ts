import { req, type JobRef } from "./api";

// Book naming (staff): the audiobook and ebook templates' presets and live preview, and the
// library-wide rename that applies them. Its own module, not part of lib/api.ts: only the
// Settings → Naming & metadata card and the book page's rename preview load it, so none of
// it lands in a requester's first load.

/** One kind of book's templates: the folder ("/" between levels), a single file, and each
 * file of a multi-file audiobook (track; audiobooks only). */
export interface BookNaming {
  folder: string;
  file: string;
  track?: string;
}

export interface BookNamingPreset {
  id: string;
  label: string;
  description: string;
  audiobook: BookNaming;
  ebook: BookNaming;
}

export interface BookNamingSample {
  label: string;
  /** Relative to the library folder, "/" between levels. */
  paths: string[];
}

/** A preview: the samples rendered, or which template has a problem and why. */
export interface BookNamingPreview {
  samples: BookNamingSample[];
  error?: { field: "folder" | "file" | "track"; message: string };
}

/** One book edition's planned rename (a book's ebook, its audiobook, or an audiobook version). */
export interface BookRenameItem {
  media: "book";
  book_id: number;
  version_id: number;
  kind: "ebook" | "audiobook";
  title: string;
  author?: string;
  label?: string;
  current: string;
  proposed: string;
  file_count: number;
  status: "ok" | "same" | "conflict" | "missing";
  reason?: string;
}

export interface BookOrganizePreview {
  counts: { rename: number; same: number; conflict: number; missing: number };
  items: BookRenameItem[];
  total: number;
  offset: number;
  token: string;
}

export interface BookOrganizeSummary {
  total: number;
  renamed: number;
  skipped: number;
}

export const bookNamingApi = {
  presets: () => req<{ presets: BookNamingPreset[]; tokens: string[] }>("/api/v1/books/naming/presets"),
  // Renders the sample books with templates that may not be saved yet.
  preview: (kind: "audiobook" | "ebook", n: BookNaming, signal?: AbortSignal) => {
    const q = new URLSearchParams({ kind, folder: n.folder, file: n.file, track: n.track ?? "" });
    return req<BookNamingPreview>(`/api/v1/books/naming/preview?${q.toString()}`, { signal });
  },
  // The dry run over every book, or one (bookId: the book page's "Preview rename"); nothing moves.
  organizePreview: (offset = 0, limit = 200, bookId?: number) =>
    req<BookOrganizePreview>(`/api/v1/books/organize?offset=${offset}&limit=${limit}${bookId ? `&book_id=${bookId}` : ""}`),
  // Applies exactly the previewed plan as a background job; a plan that changed answers 409.
  organizeApply: (token: string, bookId?: number) =>
    req<{ status: string; total: number } & JobRef>("/api/v1/books/organize", { method: "POST", body: JSON.stringify(bookId ? { token, book_id: bookId } : { token }) }),
};
