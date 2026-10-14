import { req } from "./api";
import type { WatchLink } from "../components/brand/servers";

// The Plex library calls staff pages make (library updates, Watch links on every media
// server, Watched by). They live apart from lib/api.ts because only lazily loaded pages import them: the
// requester's first load is right at its budget. Plex sign-in and account linking are
// lib/plexApi.ts.

export type PlexScanHow = "direct" | "mapped" | "guessed" | "section" | "none";

export interface PlexPathMap { from: string; to: string }

export interface PlexScanRoot {
  kind: "movie" | "show";
  arrmada_root: string;
  sections: string[];
  plex_path: string;
  how?: PlexScanHow;
  note?: string;
}

export interface PlexScanStatus {
  at: string;
  kind?: string;
  path?: string;
  section?: string;
  how?: PlexScanHow;
  error?: string;
}

export interface PlexScanView {
  enabled: boolean;
  configured: boolean;
  path_map: PlexPathMap[];
  roots: PlexScanRoot[];
  last_scan: PlexScanStatus | null;
  pending: number;
  error?: string;
}

/** Who has played a title on Plex or Jellyfin (staff only). available is false with no
 *  server connected. Each person is one server identity, with the server it's on. */
export interface WatchStats {
  available: boolean;
  plays: number;
  last_played: number; // unix seconds
  users: { id: string; name: string; plays: number; last_played: number; server?: string }[];
}

export const plexLibraryApi = {
  watchStats: (kind: "movies" | "series", id: number) => req<WatchStats>(`/api/v1/${kind}/${id}/watch-stats`),
  // The title's Watch link on each media server that has it (none: an empty list), the
  // viewer's own server first.
  watchLinks: (media: "movie" | "series", tmdbId: number) =>
    req<{ links?: WatchLink[] }>(`/api/v1/media/links?media=${media}&tmdb=${tmdbId}`).then((r) => r?.links ?? []),
  scanView: () => req<PlexScanView>("/api/v1/insights/plex/scan"),
  saveScan: (body: { enabled: boolean; path_map: PlexPathMap[] }) =>
    req<PlexScanView>("/api/v1/insights/plex/scan", { method: "PUT", body: JSON.stringify(body) }),
  // run=false only resolves the folder (Test); run=true also asks Plex to scan it.
  testScan: (kind: "movie" | "show", run: boolean) =>
    req<{ root: PlexScanRoot; last_scan: PlexScanStatus }>("/api/v1/insights/plex/scan/test", { method: "POST", body: JSON.stringify({ kind, run }) }),
};
