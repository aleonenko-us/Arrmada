import { req, type Indexer, type IndexerTestResult, type NewIndexer, type ProwlarrInfo, type ProwlarrSyncResult } from "./api";

// Indexer and Prowlarr calls (Settings → Connections). They live here rather than in api.ts
// so they only download with the staff pages that use them, not with a requester's first load.
export const indexerApi = {
  indexers: () => req<{ indexers: Indexer[] }>("/api/v1/indexers").then((r) => r.indexers),
  createIndexer: (body: NewIndexer) =>
    req<Indexer>("/api/v1/indexers", { method: "POST", body: JSON.stringify(body) }),
  updateIndexer: (id: number, body: NewIndexer) =>
    req<void>(`/api/v1/indexers/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  deleteIndexer: (id: number) => req<void>(`/api/v1/indexers/${id}`, { method: "DELETE" }),
  /** Tests settings without saving them; with id, a blank key or password means the saved one. */
  testIndexerSettings: (body: NewIndexer & { id?: number }) =>
    req<IndexerTestResult>("/api/v1/indexers/test", { method: "POST", body: JSON.stringify(body) }),
  prowlarrInfo: () => req<ProwlarrInfo>("/api/v1/indexers/prowlarr"),
  saveProwlarrUIURL: (ui_url: string) =>
    req<{ ui_url: string }>("/api/v1/indexers/prowlarr", { method: "PUT", body: JSON.stringify({ ui_url }) }),
  syncProwlarr: (body: { url: string; api_key: string; add_flaresolverr_proxy?: boolean }) =>
    req<ProwlarrSyncResult>("/api/v1/indexers/prowlarr/sync", { method: "POST", body: JSON.stringify(body) }),
};
