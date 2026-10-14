import { req } from "./api";

// Settings → Connections' calls. They live here rather than in api.ts so they only
// download with the Connections section, not with every requester's first load.

export type ConnectionKind =
  | "indexer"
  | "download_client"
  | "prowlarr"
  | "flaresolverr"
  | "apikey"
  | "plex"
  | "jellyfin"
  | "notifications"
  | "audioserver";

/** ok working · failing (still asked) · backing_off (paused) · warn (works, but look) ·
 *  disabled (switched off) · unknown (never asked, or not set up). */
export type ConnectionState = "ok" | "failing" | "backing_off" | "warn" | "disabled" | "unknown";

/** One integration's card. card is its anchor on the page ("indexer-12"). Nothing in it is
 *  a secret: no key, password, token, or URL credentials or query string. */
export interface ConnectionItem {
  kind: ConnectionKind;
  ref: string;
  card: string;
  name: string;
  /** torznab, qbittorrent…: the mono kind label. */
  type?: string;
  /** Its address (scheme, host and path only) or what it's for. */
  subtitle?: string;
  configured: boolean;
  enabled: boolean;
  state: ConnectionState;
  detail?: string;
  /** A key or URL saved in the app ("settings") or from the install ("env"). */
  source?: "settings" | "env" | "";
  last_ok_at?: string;
  last_error?: string;
  last_error_at?: string;
  failing_since?: string;
  backoff_until?: string;
  checked_at?: string;
  consecutive_failures?: number;
  counts?: { queries_24h: number; failures_24h: number; grabs_24h: number };
  testable: boolean;
  managed_by?: "prowlarr";
  /** Where it's edited, when that isn't its own card (Plex, alerts, the audiobook server). */
  fix_path?: string;
  /** Media servers only: what Arrmada uses this server for. Absent means all of it. */
  used_for?: MediaServerUse[];
}

/** What a connected media server does for Arrmada (the "Used for" chips on its card). */
export type MediaServerUse = "watch" | "library" | "signin" | "insights";

export type ConnectionSectionId = "search" | "downloads" | "metadata" | "other";

export interface ConnectionSectionData {
  id: ConnectionSectionId;
  title: string;
  items: ConnectionItem[];
}

export interface ConnectionTestResult {
  ok: boolean;
  detail: string;
  item: ConnectionItem;
}

export const connectionsApi = {
  list: () => req<{ sections: ConnectionSectionData[] }>("/api/v1/connections").then((r) => r.sections),
  /** A real check of one connection, recorded like any other use; answers with its refreshed card. */
  test: (kind: ConnectionKind, ref: string) =>
    req<ConnectionTestResult>(`/api/v1/connections/${kind}/${encodeURIComponent(ref)}/test`, { method: "POST" }),
};
