import { req, type DownloadClient, type DownloadClientList, type NewDownloadClient, type PathMap } from "./api";

/** One path the client reported: its save folder or its newest finished download. */
export interface PathCheck {
  what: "save_folder" | "newest_download";
  remote: string;
  local: string;
  visible: boolean;
  /** Missing although its folder is visible: moved or deleted, not a mapping problem. */
  gone?: boolean;
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
  downloadClientStatus: (id: number) =>
    req<{ listen_port: number }>(`/api/v1/downloadclients/${id}/status`),
  /** Logs in and checks which of its paths Arrmada can see. */
  testDownloadClient: (id: number) =>
    req<DownloadClientTest>(`/api/v1/downloadclients/${id}/test`, { method: "POST" }),
};
