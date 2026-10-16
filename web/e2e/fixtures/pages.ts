import { jellyfinConnected } from "./media";
import { jellyfinScan } from "./modules";

// Every page the whole-app specs walk (screens.spec.ts screenshots, contrast.spec.ts
// colour audit): each staff page and Settings section as an admin, and the requester's
// own pages.
export const STAFF_PAGES: string[] = [
  "/",
  "/activity/queue",
  "/activity/needs-you",
  "/activity/wanted",
  "/activity/history",
  "/activity/blocklist",
  "/discover",
  "/movies",
  "/movies/wanted",
  "/movies/1",
  "/series",
  "/series/1",
  "/requests",
  "/calendar",
  "/music",
  "/books",
  "/subtitles",
  "/subtitles?tab=library",
  "/convert",
  "/insights",
  "/audiobooks",
  "/me",
  "/quality",
  "/quality/movie/hd-1080p",
  "/logs",
  "/settings/library",
  "/settings/media",
  "/settings/connections",
  "/settings/downloads",
  "/settings/subtitles",
  "/settings/convert",
  "/settings/alerts",
  "/settings/media-servers",
  "/settings/audiobook-server",
  "/settings/users",
  "/settings/import",
  "/settings/system",
  "/settings/status",
];
export const REQUESTER_PAGES: string[] = ["/discover", "/discover?tab=books", "/requests", "/calendar", "/shelf", "/audiobooks", "/me"];

// Pages that read an endpoint the shared fixtures leave to the specs that test them
// (method and path → body).
export const PAGE_EXTRA: Record<string, Record<string, unknown>> = {
  "/settings/downloads": { "GET /api/v1/downloads/disk-guard": { path: "/media/downloads", measurable: true, used_pct: 40, holding: 0, shared_with: [] } },
  "/settings/audiobook-server": {
    "GET /api/v1/audioserver": { enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0, users: [], devices: [] },
  },
  // Settings → Media servers with Jellyfin connected and recording (contrast.spec's extra
  // shot): its status line, Watch links address and library updates lines.
  "/settings/media-servers#jellyfin-server": {
    "GET /api/v1/jellyfin": jellyfinConnected,
    "GET /api/v1/insights/servers": { servers: [
      { server: "plex", configured: false, monitoring: false, status: "unconfigured", poll_seconds: 30 },
      { server: "jellyfin", name: "Attic", configured: true, monitoring: true, status: "recording", poll_seconds: 10, last_poll_at: 1_700_000_000 },
    ] },
    "GET /api/v1/jellyfin/scan": {
      ...jellyfinScan, configured: true, path_map: [{ from: "/movies", to: "/media/movies" }],
      roots: [
        { kind: "movie", arrmada_root: "/movies", sections: ["Films"], plex_path: "/media/movies", how: "mapped" },
        { kind: "show", arrmada_root: "/tv", sections: ["Shows"], plex_path: "", how: "section" },
      ],
    },
  },
  // The movie target-file builder, as quality-editor.spec opens it.
  "/quality/movie/hd-1080p": {
    "GET /api/v1/quality/profiles/hd-1080p": {
      id: 7, media_type: "movie", name: "HD 1080p", allowed_resolutions: ["1080p"], min_source: "WEB-DL", max_source: "",
      bitrate_cap_mbps: 0, small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 1, stall_minutes: 0,
      upgrades_enabled: true, upgrade_min_percent: 0, ideal: { language: { original: "want", de: "must" } },
    },
    "POST /api/v1/quality/preview": { profile: "", decision: { winner: null, eligible: [], rejected: [] } },
    "POST /api/v1/library/fit/preview": { scope: "library", counts: { titles: 0, files: 0, fits: 0, over: 0, under: 0, mismatch: 0 } },
    "POST /api/v1/quality/impact": { replace: { files: 0, bytes: 0, examples: [] }, search: { files: 0, bytes: 0, examples: [] } },
  },
};
