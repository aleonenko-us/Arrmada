import { req, type DownloadClient, type DownloadClientList, type NewDownloadClient } from "./api";

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
};
