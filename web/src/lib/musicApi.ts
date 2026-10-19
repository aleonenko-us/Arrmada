import { req, type ReviewImportStarted } from "./api";

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

/** The music matcher's thresholds and the edition policy (Settings → Naming & metadata). */
export interface MusicMatching {
  strong: number;
  expected: number;
  review: number;
  track_max: number;
  gap: number;
  partial_min: number;
  edition_policy: "original" | "most_complete" | "preferred";
  edition_countries: string[];
  edition_media: string[];
}

/** Which release groups an artist lists (MUS-14): the primary type must be allowed AND
 *  every secondary type. */
export interface ReleaseTypes {
  primary: string[];
  secondary: string[];
}

export type MonitorNew = "all" | "none";

/** What "Want matching albums" would do: nothing happens until it's confirmed. */
export interface ApplyPlan {
  want: { id: number; title: string; type?: string; year?: number }[];
  unwant: { id: number; title: string; type?: string; year?: number }[];
  singles: { id: number; title: string; type?: string; year?: number }[];
}

/** An artist's options: its own choices ("" / null follow the library default). */
export interface ArtistOptions {
  monitor_new: "" | MonitorNew;
  release_types: ReleaseTypes | null;
  effective_monitor_new: MonitorNew;
  effective_release_types: ReleaseTypes;
  default_monitor_new: MonitorNew;
  default_release_types: ReleaseTypes;
  quality_profile: string;
  apply: ApplyPlan;
}

export interface ReleaseTypeChoices {
  primary: string[];
  secondary: string[];
}

export interface ReleaseDefaults {
  monitor_new: MonitorNew;
  release_types: ReleaseTypes;
}

/** An album a refresh found, with why it is or isn't wanted (found_as). */
export interface NewRelease {
  id: number;
  artist_id: number;
  artist_name: string;
  title: string;
  album_type?: string;
  release_date?: string;
  year?: number;
  monitored: boolean;
  found_at?: string;
  found_as?: string;
}

export const musicApi = {
  artistOptions: (artistId: number) =>
    req<{ options: ArtistOptions; choices: ReleaseTypeChoices }>(`/api/v1/music/artists/${artistId}/options`),
  /** Save an artist's options. release_types null = follow the library default. */
  setArtistOptions: (artistId: number, patch: { monitor_new?: "" | MonitorNew; release_types?: ReleaseTypes | null; quality_profile?: string }) =>
    req<{ options: ArtistOptions; choices: ReleaseTypeChoices; refreshed: boolean }>(`/api/v1/music/artists/${artistId}/options`, {
      method: "PUT",
      body: JSON.stringify(patch),
    }),
  /** Carry out the plan the person saw (its albums); 409 when it changed. */
  applyArtistOptions: (artistId: number, plan: ApplyPlan) =>
    req<{ applied: ApplyPlan }>(`/api/v1/music/artists/${artistId}/options/apply`, {
      method: "POST",
      body: JSON.stringify({ want: plan.want.map((a) => a.id), unwant: plan.unwant.map((a) => a.id), singles: plan.singles.map((a) => a.id) }),
    }),
  releaseDefaults: () =>
    req<{ settings: ReleaseDefaults; defaults: ReleaseDefaults; choices: ReleaseTypeChoices }>("/api/v1/music/release-defaults"),
  setReleaseDefaults: (patch: Partial<ReleaseDefaults>) =>
    req<{ settings: ReleaseDefaults }>("/api/v1/music/release-defaults", { method: "PUT", body: JSON.stringify(patch) }),
  newReleases: (days = 30) => req<{ albums: NewRelease[]; days: number }>(`/api/v1/music/new-releases?days=${days}`),

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

  matching: () => req<{ settings: MusicMatching; defaults: MusicMatching }>("/api/v1/music/matching"),
  setMatching: (patch: Partial<MusicMatching>) =>
    req<{ settings: MusicMatching }>("/api/v1/music/matching", { method: "PUT", body: JSON.stringify(patch) }),

  /** Reviews: import a held music download into one edition of an album. */
  importReviewEdition: (reviewId: number, albumId: number, releaseId: number) =>
    req<ReviewImportStarted>(`/api/v1/reviews/${reviewId}/import`, {
      method: "POST",
      body: JSON.stringify({ target_id: albumId, target_kind: "music", release_id: releaseId }),
    }),
};
