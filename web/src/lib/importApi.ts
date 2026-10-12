import { req, type ImportOverlaps, type ImportRun, type JobRef, type TautulliConfig } from "./api";

// Import & migrate calls (Overseerr requests, Tautulli history). They live here rather than in
// api.ts so they only download with that settings section, not with a requester's first load.
export const importApi = {
  importOverseerr: (url: string, api_key: string) =>
    req<{ status: string; found: number } & JobRef>("/api/v1/requests/import/overseerr", { method: "POST", body: JSON.stringify({ url, api_key }) }),
  // before (epoch seconds) imports only plays that started earlier. An empty api_key uses
  // the saved one, but only for the saved URL.
  importTautulli: (url: string, api_key: string, before?: number) =>
    req<{ status: string } & JobRef>("/api/v1/insights/import/tautulli", { method: "POST", body: JSON.stringify({ url, api_key, before }) }),
  // Imported plays that double up plays Arrmada recorded live (admin), and removing them:
  // a job that backs the database up first and deletes nothing unless `expected` still holds.
  importOverlaps: () => req<ImportOverlaps>("/api/v1/insights/import/overlaps"),
  removeImportOverlaps: (expected: number) =>
    req<JobRef>("/api/v1/insights/import/overlaps/remove", { method: "POST", body: JSON.stringify({ expected }) }),
  tautulliConfig: () => req<TautulliConfig>("/api/v1/insights/import/tautulli"),
  importRuns: () => req<{ runs: ImportRun[] }>("/api/v1/insights/import/runs").then((r) => r.runs),
  // Retry runs the import again from the saved connection (already-imported plays are skipped).
  retryImportRun: (id: number) => req<JobRef>(`/api/v1/insights/import/runs/${id}/retry`, { method: "POST" }),
  // Undo one import: a job that backs the database up, then deletes exactly that run's plays
  // — nothing, if the count is no longer `expected`.
  removeImportRun: (id: number, expected: number) =>
    req<JobRef>(`/api/v1/insights/import/runs/${id}/rows?expected=${expected}`, { method: "DELETE" }),
};
