import { req, type DownloadClient, type DownloadClientList, type NewDownloadClient, type PathMap } from "./api";

/**
 * One path the client reported: its save folder or newest finished download (qBittorrent),
 * or its completed folder or a category's own folder (SABnzbd).
 */
export interface PathCheck {
  what: "save_folder" | "newest_download" | "complete_folder" | "category_folder";
  remote: string;
  local: string;
  visible: boolean;
  /** Missing although its folder is visible: moved or deleted, not a mapping problem. */
  gone?: boolean;
  /** The category a "category_folder" check is for. */
  category?: string;
}

/** A client Test's path check: which paths Arrmada can see, and a mapping that would fix the rest. */
export interface PathReport {
  checks: PathCheck[];
  other_drive?: string[];
  suggestion?: PathMap;
  ok: boolean;
  summary: string;
}

export interface DownloadClientTest {
  ok: boolean;
  error?: string;
  /** Present once it connected and could say where it saves. */
  paths?: PathReport;
  paths_error?: string;
}

/** One of Arrmada's categories in a usenet client, and whether it has it. */
export interface UsenetCategory {
  name: string;
  /** What Arrmada files under it: movies, tv, books, music. */
  for: string;
  present: boolean;
  /** Set when the category has a folder of its own, with whether Arrmada sees it. */
  folder?: PathCheck;
}

/** A news server SABnzbd uses: only whether it works, never its host or login. */
export interface UsenetServer {
  name: string;
  optional: boolean;
  connections: number;
  ok: boolean;
  error?: string;
}

/** What a usenet client's card shows beside its connection state (download.UsenetFacts). */
export interface UsenetFacts {
  version: string;
  categories: UsenetCategory[];
  missing_categories: string[];
  complete_folder: PathCheck;
  servers: UsenetServer[];
  servers_configured: number;
  servers_working: number;
  language: string;
  not_english: boolean;
  /** The problems above as plain sentences, worst first. */
  warnings: string[];
}

/** A client's live status: qBittorrent's incoming port; a usenet client's card facts. */
export interface DownloadClientLiveStatus {
  listen_port: number;
  usenet?: UsenetFacts;
  /** The usenet client couldn't be read just now. */
  usenet_error?: string;
}

// Download client calls. They live here rather than in api.ts so they only download with the
// staff pages that use them, not with a requester's first load.
export const downloadClientApi = {
  downloadClients: () =>
    req<DownloadClientList>("/api/v1/downloadclients").then((r) => r.clients),
  downloadClientList: () => req<DownloadClientList>("/api/v1/downloadclients"),
  createDownloadClient: (body: NewDownloadClient) =>
    req<DownloadClient>("/api/v1/downloadclients", { method: "POST", body: JSON.stringify(body) }),
  updateDownloadClient: (id: number, body: NewDownloadClient) =>
    req<DownloadClient>(`/api/v1/downloadclients/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  restoreBundledClient: () =>
    req<{ restored: boolean }>("/api/v1/downloadclients/restore-bundled", { method: "POST" }),
  deleteDownloadClient: (id: number) =>
    req<void>(`/api/v1/downloadclients/${id}`, { method: "DELETE" }),
  /** For a usenet client this asks SABnzbd four or five things: read it once, never poll. */
  downloadClientStatus: (id: number) =>
    req<DownloadClientLiveStatus>(`/api/v1/downloadclients/${id}/status`),
  /** Logs in and checks which of its paths Arrmada can see. */
  testDownloadClient: (id: number) =>
    req<DownloadClientTest>(`/api/v1/downloadclients/${id}/test`, { method: "POST" }),
  /** Creates the Arrmada categories a usenet client lacks; only ever on the owner's click. */
  addMissingCategories: (id: number) =>
    req<{ created: string[]; usenet?: UsenetFacts }>(`/api/v1/downloadclients/${id}/categories`, { method: "POST" }),
};
