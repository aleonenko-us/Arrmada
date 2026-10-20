import { api, req, type AttemptSummary, type ReleaseList } from "./api";

// Music searching from the album and artist pages (MUS-09, MUS-16, MUS-17). Staff only,
// so a lazy module of its own, never part of a requester's first load.

/** Where an album stands against its profile's cutoff. */
export interface AlbumUpgrade {
  /** The profile's name. */
  profile: string;
  /** The lowest tier among its files ("" when it holds none, or one isn't measured). */
  held?: string;
  /** The ladder's top tier. */
  cutoff?: string;
  /** off: the profile doesn't upgrade · incomplete: missing tracks first · unknown: a
   *  file's tier isn't measured yet · below: upgrades are looked for · at: at its cutoff. */
  state: "off" | "incomplete" | "unknown" | "below" | "at";
  /** When the upgrade sweep may next look (below only). */
  next_at?: string;
}

/** Why an album is or isn't downloading. */
export interface AlbumSearchState {
  last_search?: AttemptSummary;
  next_search_at?: string;
  upgrade: AlbumUpgrade;
}

export interface JobStarted {
  status: string;
  job_id?: number;
  existing?: boolean;
}

export const musicSearchApi = {
  /** Search now for one album (Wanted's search: clears its backoff, runs as a job). */
  searchAlbum: (albumId: number) => api.wantedSearch("music", albumId),
  /** Search now for every monitored album of an artist that's missing tracks (a job). */
  searchArtist: (artistId: number) => req<JobStarted>(`/api/v1/music/artists/${artistId}/search`, { method: "POST" }),
  /** An album's interactive search: every release with why it would or wouldn't be grabbed. */
  albumReleases: (albumId: number) => req<ReleaseList>(`/api/v1/music/albums/${albumId}/releases`),
  /** Grab a release from the album's interactive search, by its token. */
  grabAlbum: (albumId: number, token: string) =>
    req<{ status: string; title: string }>(`/api/v1/music/albums/${albumId}/grab`, { method: "POST", body: JSON.stringify({ token }) }),
  albumSearchState: (albumId: number) => req<AlbumSearchState>(`/api/v1/music/albums/${albumId}/search-state`),
};
