import { req } from "./api";

// Music calls that only the staff music pages make. Their own module, not part of
// lib/api.ts, so none of it lands in a requester's first load.

/** One edition of an album in the picker (MUS-30). */
export interface AlbumEditionView {
  id: number;
  mbid?: string;
  title: string;
  status?: string;
  release_date?: string;
  country?: string;
  /** The record label. */
  label?: string;
  media_summary?: string;
  disambiguation?: string;
  track_count: number;
  legacy?: boolean;
  target?: boolean;
  tracks_fetched: boolean;
  /** "GB CD 1997 · 12 tracks · deluxe edition" */
  words: string;
  acceptable: boolean;
  /** The target's tracks under another edition: a download of it satisfies the album. */
  same_as_target?: boolean;
  /** How many of the album's files sit on this edition's tracks. */
  files: number;
}

export const musicApi = {
  /** Mark one of an album's upgrade notes as looked at; the sweep searches the album again. */
  resolveAlbumNote: (albumId: number, noteId: number) =>
    req<{ status: string }>(`/api/v1/music/albums/${albumId}/notes/${noteId}/resolve`, { method: "POST" }),

  /** Every edition of an album, the target first. */
  albumEditions: (albumId: number) =>
    req<{ editions: AlbumEditionView[]; pinned: boolean }>(`/api/v1/music/albums/${albumId}/editions`),

  /** Make an edition the album's target (its files move with it). A 202 with job_id when
   *  its tracks must first be read from MusicBrainz. */
  setAlbumEdition: (albumId: number, releaseId: number, pinned: boolean) =>
    req<{ moved?: number; kept?: number; job_id?: number; existing?: boolean }>(`/api/v1/music/albums/${albumId}/edition`, {
      method: "PUT",
      body: JSON.stringify({ release_id: releaseId, pinned }),
    }),
};
