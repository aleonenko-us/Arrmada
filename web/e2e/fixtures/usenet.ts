import type { Page, Route } from "@playwright/test";
import type { ActivityDownload, ActivityFeed, AppSettings, DownloadClient, DownloadClientList, ReleaseList } from "../../src/lib/api";
import type { ConnectionSectionData } from "../../src/lib/connectionsApi";
import type { UsenetFacts } from "../../src/lib/downloadClientApi";
import type { MockedApi } from "../mockApi";
import { NOW } from "./clock";
import { activity, connections, downloadClients, movieReleases } from "./media";
import { settings } from "./system";

// Usenet (SABnzbd) beside the bundled qBittorrent: the client, its card's facts, usenet
// rows in the queue and in a release search, and the network preference. The shared
// fixtures have no usenet client, so a spec opts in with mockUsenet(page, api).

// Never shown anywhere: it goes out once, in the add request.
export const SAB_KEY = "0f1e2d3c4b5a69788796a5b4c3d2e1f0";
export const SAB_ID = 4;

export const sabClient: DownloadClient = { id: SAB_ID, name: "SABnzbd", kind: "sabnzbd", url: "http://sabnzbd:8080", enabled: true, priority: 25 };

export const sabFacts: UsenetFacts = {
  version: "4.5.5",
  categories: [
    { name: "arrmada", for: "movies", present: true },
    { name: "arrmada-tv", for: "tv", present: true },
    { name: "arrmada-books", for: "books", present: false },
    { name: "arrmada-music", for: "music", present: false },
  ],
  missing_categories: ["arrmada-books", "arrmada-music"],
  complete_folder: { what: "complete_folder", remote: "/downloads/complete", local: "/media/downloads/complete", visible: true },
  servers: [{ name: "news.example.net", optional: false, connections: 20, ok: true }],
  servers_configured: 1,
  servers_working: 1,
  language: "de",
  not_english: true,
  warnings: [
    'SABnzbd is missing Arrmada\'s arrmada-books, arrmada-music categories. Use "Add missing categories"',
    'SABnzbd\'s language is "de". Its failure reasons are translated, which makes them harder for Arrmada to read; English works best (SABnzbd → Config → General)',
  ],
};

export const sabFactsFixed: UsenetFacts = {
  ...sabFacts,
  categories: sabFacts.categories.map((c) => ({ ...c, present: true })),
  missing_categories: [],
  warnings: [sabFacts.warnings[1]],
};

const sabCard = {
  kind: "download_client" as const, ref: String(SAB_ID), card: `download_client-${SAB_ID}`, name: "SABnzbd", type: "sabnzbd", subtitle: "http://sabnzbd:8080",
  configured: true, enabled: true, state: "ok" as const, testable: true, last_ok_at: new Date(NOW - 2 * 60_000).toISOString(),
};

export function connectionsWithSab(): { sections: ConnectionSectionData[] } {
  return { sections: connections.sections.map((s) => (s.id === "downloads" ? { ...s, items: [...s.items, sabCard] } : s)) };
}

export const clientsWithSab: DownloadClientList = { ...downloadClients, clients: [...downloadClients.clients, sabClient] };

// The queue: a usenet job downloading, one SABnzbd failed, one unpacking and one imported,
// beside the shared torrents (one of which is seeding).
const usenetRow = (n: number, over: Partial<ActivityDownload>): ActivityDownload => ({
  hash: `sab-sabnzbd_nzo_e2e${n}`, name: `Usenet.Fixture.${n}`, state: "downloading", progress: 0.5, size_bytes: 4_000_000_000,
  down_speed: 0, up_speed: 0, eta_seconds: 0, ratio: 0, quality_profile: "hd-1080p", media_type: "movie", managed: true, transport: "usenet", ...over,
});
export const USENET_DOWNLOADING = "sab-sabnzbd_nzo_e2e1";
export const USENET_FAILED = "sab-sabnzbd_nzo_e2e2";
export const usenetQueue: ActivityDownload[] = [
  usenetRow(1, {
    name: "Tidewater.2024.1080p.WEB-DL.H264-NZB", progress: 0.75, down_speed: 48_000_000, eta_seconds: 300, phase: "downloading", stage: "downloading",
    item: { kind: "movie", id: 6, title: "Tidewater", year: 2024 },
  }),
  usenetRow(2, {
    name: "Glasshouse.2023.1080p.BluRay.x264-NZB", state: "error", phase: "error", progress: 0, stage: "failed",
    why: "The download client stopped it with an error", fail_message: "Out of retention: articles are missing on the server",
    item: { kind: "movie", id: 8, title: "Glasshouse", year: 2023 },
  }),
  usenetRow(3, {
    name: "Harbour.Lights.S02E05.1080p.WEB.H264-NZB", state: "unpacking", phase: "unpacking", progress: 1, stage: "unpacking", media_type: "series",
    why: "SABnzbd is checking and unpacking it", item: { kind: "series", id: 1, title: "Harbour Lights", year: 2024, scope: "S02E05" },
  }),
  usenetRow(4, {
    name: "Saltwater.2024.FLAC-NZB", state: "completed", phase: "completed", progress: 1, stage: "imported", imported: true, media_type: "music",
    item: { kind: "music", id: 2, title: "Saltwater", byline: "Low Tide" },
  }),
];
export const activityWithUsenet: ActivityFeed = { ...activity, downloads: [...activity.downloads, ...usenetQueue], clients: { configured: 2, enabled: 2, ok: true } };

// A release search with the same film on Usenet and a torrent tracker.
export const releasesWithUsenet: ReleaseList = {
  ...movieReleases,
  releases: [
    {
      title: "Anchor.Point.2022.1080p.WEB-DL.H264-NZBGRP", indexer: "Fixture Newznab", token: "tok-nzb", size_gb: 5, bitrate_mbps: 6, seeders: 0, peers: 0,
      summary: "1080p WEB-DL", eligible: true, recommended: true, transport: "usenet",
    },
    ...movieReleases.releases.slice(0, 3).map((r) => ({ ...r, recommended: false, transport: "torrent" as const, lost_on: "a torrent, and you prefer Usenet", lost_on_key: "network" })),
  ],
};
// The same search with no usenet client: the usenet row says why it can't be grabbed.
export const releasesNoUsenetClient: ReleaseList = {
  ...releasesWithUsenet,
  releases: releasesWithUsenet.releases.map((r) => (r.transport === "usenet" ? { ...r, eligible: false, recommended: false, reject_reason: "Usenet release — no usenet download client is set up" } : r)),
};

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

/**
 * mockUsenet answers the usenet endpoints a spec needs, over the shared fixtures. With
 * withClient false there is no SABnzbd yet (until one is added through the form). Calls
 * it answers are recorded on api.calls like the shared ones.
 */
export async function mockUsenet(page: Page, api: MockedApi, { withClient = true } = {}) {
  let hasClient = withClient;
  let facts = sabFacts;
  let pref: AppSettings["download_network_preference"] = "best";
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    const method = req.method();
    let body: unknown = undefined;
    try { body = req.postDataJSON(); } catch { body = req.postData(); }
    const record = () => api.calls.push({ method, path: p + url.search, body });

    if (method === "GET" && p === "/api/v1/downloadclients") { record(); return json(route, hasClient ? clientsWithSab : downloadClients); }
    if (method === "POST" && p === "/api/v1/downloadclients") { record(); hasClient = true; return json(route, sabClient, 201); }
    if (method === "GET" && p === `/api/v1/downloadclients/${SAB_ID}/status`) { record(); return json(route, { listen_port: 0, usenet: facts }); }
    if (method === "POST" && p === `/api/v1/downloadclients/${SAB_ID}/categories`) {
      record();
      facts = sabFactsFixed;
      return json(route, { created: ["arrmada-books", "arrmada-music"], usenet: facts });
    }
    if (method === "GET" && p === "/api/v1/connections") { record(); return json(route, hasClient ? connectionsWithSab() : connections); }
    if (method === "POST" && p === `/api/v1/connections/download_client/${SAB_ID}/test`) {
      record();
      return json(route, { ok: true, detail: "Connected · SABnzbd 4.5.5", item: { ...sabCard, checked_at: new Date(NOW).toISOString() } });
    }
    if (method === "GET" && p === "/api/v1/downloads") { record(); return json(route, hasClient ? activityWithUsenet : activity); }
    if (method === "POST" && /^\/api\/v1\/queue\/sab-[a-z0-9_]+\/action$/.test(p)) { record(); return json(route, { status: "ok" }); }
    if (method === "GET" && /^\/api\/v1\/movies\/\d+\/releases$/.test(p)) { record(); return json(route, hasClient ? releasesWithUsenet : releasesNoUsenetClient); }
    if (method === "GET" && p === "/api/v1/settings") { record(); return json(route, { ...settings, download_network_preference: pref }); }
    if (method === "PUT" && p === "/api/v1/settings") {
      record();
      const b = body as Partial<AppSettings>;
      if (b.download_network_preference) pref = b.download_network_preference;
      return json(route, { ...settings, download_network_preference: pref });
    }
    return route.fallback();
  });
}
