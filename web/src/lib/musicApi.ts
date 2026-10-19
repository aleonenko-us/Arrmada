import { req } from "./api";

// Music calls that only the staff music pages make. Their own module, not part of
// lib/api.ts, so none of it lands in a requester's first load.

export const musicApi = {
  /** Mark one of an album's upgrade notes as looked at; the sweep searches the album again. */
  resolveAlbumNote: (albumId: number, noteId: number) =>
    req<{ status: string }>(`/api/v1/music/albums/${albumId}/notes/${noteId}/resolve`, { method: "POST" }),
};
