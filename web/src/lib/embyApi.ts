import { req } from "./api";
import type { PlexPathMap, PlexScanRoot, PlexScanStatus, PlexScanView } from "./plexLibraryApi";

// The Emby connection's calls (its card on Settings → Connections, its group on
// Settings → Media servers). They live here rather than in api.ts so they only download
// with those pages, not with every requester's first load. The API key goes up, never
// comes back: the server answers key_set only.

/** GET /api/v1/emby: the saved connection and what the server said about itself. */
export interface EmbyConfig {
  url: string;
  /** The address Watch links use; blank means the connection URL. */
  public_url: string;
  key_set: boolean;
  server_name: string;
  server_id: string;
  version: string;
  monitoring_enabled: boolean;
  poll_seconds: number;
}

/** PUT /api/v1/emby. A missing api_key keeps the saved one. */
export interface EmbySave {
  url: string;
  public_url: string;
  api_key?: string;
}

/** POST /api/v1/emby/test: blank fields test the saved ones. */
export interface EmbyTestResult {
  ok: boolean;
  detail: string;
  server_name?: string;
  version?: string;
}

export const embyApi = {
  get: () => req<EmbyConfig>("/api/v1/emby"),
  save: (body: EmbySave) => req<EmbyConfig>("/api/v1/emby", { method: "PUT", body: JSON.stringify(body) }),
  test: (body: { url?: string; api_key?: string }) =>
    req<EmbyTestResult>("/api/v1/emby/test", { method: "POST", body: JSON.stringify(body) }),
  // Forgets the address, the key and the identity (admin). How Emby was used stays.
  disconnect: () => req<EmbyConfig>("/api/v1/emby", { method: "DELETE" }),
  // Library updates: the same shapes as Plex's (plex_path is the folder on Emby's side).
  scanView: () => req<PlexScanView>("/api/v1/emby/scan"),
  saveScan: (body: { enabled: boolean; path_map: PlexPathMap[] }) =>
    req<PlexScanView>("/api/v1/emby/scan", { method: "PUT", body: JSON.stringify(body) }),
  testScan: (kind: "movie" | "show", run: boolean, root = "") =>
    req<{ root: PlexScanRoot; last_scan: PlexScanStatus }>("/api/v1/emby/scan/test", { method: "POST", body: JSON.stringify({ kind, run, root }) }),
};
