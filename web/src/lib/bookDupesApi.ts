import { req } from "./api";

// Books → Possible duplicates (staff). Its own module, not part of lib/api.ts: only the
// lazy duplicates modal and the Books page's count load it, so none of it lands in a
// requester's first load.

export interface DupBookFile {
  path: string;
  format: string;
  size_bytes: number;
  file_count: number;
}

/** One book as the review shows it. Never anything about who has listened to it. */
export interface DupBook {
  id: number;
  title: string;
  author: string;
  year?: number;
  cover_url?: string;
  key: string;
  source: string;
  ebook?: DupBookFile;
  audiobook?: DupBookFile;
  audio_versions: number;
  monitored: boolean;
  added_at?: string;
}

export interface DupGroup {
  books: DupBook[];
  suggested_keep_id: number;
  reasons: string[];
}

/** An earlier merge on a kept book's timeline. Automatic ones were made by the old
 *  start-up fold; lost_title says which book went, so it can be added back. */
export interface PastMerge {
  book_id: number;
  keeper_title: string;
  detail: string;
  created_at: string;
  automatic: boolean;
  lost_title?: string;
  lost_key?: string;
}

export interface DupMergeOutcome {
  drop_id: number;
  drop_title: string;
  moved_ebook: boolean;
  moved_audiobook: boolean;
  merged_copy_id?: number;
  versions: number;
  left_files: string[];
  note?: string;
}

export const bookDupesApi = {
  list: () => req<{ groups: DupGroup[]; past_merges: PastMerge[] }>("/api/v1/books/duplicates"),
  merge: (keepId: number, dropIds: number[]) =>
    req<{ keep_id: number; results: DupMergeOutcome[]; left_files: string[] }>("/api/v1/books/duplicates/merge", {
      method: "POST",
      body: JSON.stringify({ keep_id: keepId, drop_ids: dropIds }),
    }),
  ignore: (ids: number[]) =>
    req<void>("/api/v1/books/duplicates/ignore", { method: "POST", body: JSON.stringify({ ids }) }),
};
