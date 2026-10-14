import { req } from "./api";

// The Jellyfin connection's calls (Settings → Connections). They live here rather than in
// api.ts so they only download with the Connections page, not with every requester's
// first load. The API key goes up, never comes back: the server answers key_set only.

/** GET /api/v1/jellyfin: the saved connection and what the server said about itself. */
export interface JellyfinConfig {
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

/** PUT /api/v1/jellyfin. A missing api_key keeps the saved one. */
export interface JellyfinSave {
  url: string;
  public_url: string;
  api_key?: string;
}

/** POST /api/v1/jellyfin/test: blank fields test the saved ones. */
export interface JellyfinTestResult {
  ok: boolean;
  detail: string;
  server_name?: string;
  version?: string;
}

export const jellyfinApi = {
  get: () => req<JellyfinConfig>("/api/v1/jellyfin"),
  save: (body: JellyfinSave) => req<JellyfinConfig>("/api/v1/jellyfin", { method: "PUT", body: JSON.stringify(body) }),
  test: (body: { url?: string; api_key?: string }) =>
    req<JellyfinTestResult>("/api/v1/jellyfin/test", { method: "POST", body: JSON.stringify(body) }),
};
