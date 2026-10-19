import { req } from "./api";

// The MusicBrainz card's calls (Settings → Connections, admin only). They live here rather
// than in api.ts so they only download with the Connections section.

export interface MusicBrainzSettings {
  /** "" = the public server (default_base_url). */
  base_url: string;
  default_base_url: string;
  /** Requests a second to a self-hosted mirror; the public server is always one. */
  mirror_rate: number;
  max_mirror_rate: number;
  contact: string;
  cache: { entries: number; bytes: number; stale: number };
  /** MusicBrainz refused recent background lookups; Music runs on the cache until then. */
  paused: boolean;
  paused_until?: string;
}

export interface MusicBrainzUpdate {
  base_url?: string;
  mirror_rate?: number;
  contact?: string;
}

export const musicBrainzApi = {
  get: () => req<MusicBrainzSettings>("/api/v1/settings/musicbrainz"),
  save: (u: MusicBrainzUpdate) =>
    req<MusicBrainzSettings>("/api/v1/settings/musicbrainz", { method: "PUT", body: JSON.stringify(u) }),
  test: (base_url?: string) =>
    req<{ ok: boolean; detail: string }>("/api/v1/settings/musicbrainz/test", {
      method: "POST",
      body: JSON.stringify(base_url === undefined ? {} : { base_url }),
    }),
};
