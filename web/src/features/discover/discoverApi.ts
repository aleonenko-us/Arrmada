import { req, type DiscoverCard } from "../../lib/api";

// Discover's newer calls: hidden titles ("Not interested"), the Plex watchlist and the
// region's age ratings. Kept out of lib/api so they load with the lazy parts that use
// them, not in every requester's first load.

/** A title hidden from this person's Discover. */
export interface HiddenTitle {
  media_type: "movie" | "series";
  tmdb_id: number;
  title: string;
  year?: number;
  poster_url?: string;
  at?: string;
}

/** An age rating in the discovery region's scale, mildest first by order. */
export interface Certification { code: string; meaning?: string; order: number }

export const discoverApi = {
  hidden: () => req<{ items: HiddenTitle[] }>("/api/v1/discover/hidden").then((r) => r.items),
  hide: (c: DiscoverCard) =>
    req<{ status: string }>("/api/v1/discover/hidden", {
      method: "POST",
      body: JSON.stringify({ media_type: c.media_type, tmdb_id: c.tmdb_id, title: c.title, year: c.year, poster_url: c.poster_url }),
    }),
  restore: (media: string, id: number) => req<{ status: string }>(`/api/v1/discover/hidden/${media}/${id}`, { method: "DELETE" }),
  plexWatchlist: () => req<{ items: DiscoverCard[]; auto: boolean; available: boolean }>("/api/v1/discover/plex-watchlist"),
  setWatchlistAuto: (on: boolean) =>
    req<{ auto: boolean }>("/api/v1/discover/plex-watchlist/auto", { method: "PUT", body: JSON.stringify({ on }) }),
  certifications: (media: string) =>
    req<{ country: string; certifications: Certification[] }>(`/api/v1/discover/certifications?media=${media}`),
};
