import { req, type ImportOverlaps, type ImportRun, type JobRef, type TautulliConfig } from "./api";
import type { ArrRoot } from "./foldersApi";

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

// Radarr / Sonarr: bring a library over (Settings → Import & migrate). The API key goes with
// each call and is never kept; the preview changes nothing, the import is a job to follow.
export type ArrApp = "radarr" | "sonarr";

export interface ArrPathMap {
  from: string;
  to: string;
}

export interface ArrImportChoices {
  /** The app's profile id → an Arrmada profile key ("" = the default profile). */
  profiles?: Record<number, string>;
  /** The app's root folder → an Arrmada folder id (0 = the default folder). */
  folders?: Record<string, number>;
  /** Left out, the preview suggests maps from the matched folders. */
  path_maps?: ArrPathMap[];
  import_missing?: boolean;
}

export interface ArrCounts {
  titles: number;
  existing: number;
  new: number;
  unmatchable: number;
  with_files: number;
  files_found: number;
  not_found: number;
  attachable: number;
  wanted: number;
}

export interface ArrSample {
  title: string;
  year?: number;
  path?: string;
  reason?: string;
}

export interface ArrProfileUse {
  id: number;
  name: string;
  max_resolution: number;
  titles: number;
  suggested: string;
}

export interface ArrTarget {
  key: string;
  name: string;
  max_resolution: number;
  default: boolean;
}

export interface ArrPreview {
  preview: {
    app: ArrApp;
    media: "movie" | "series";
    name: string;
    version: string;
    counts: ArrCounts;
    profiles: ArrProfileUse[];
    not_found: ArrSample[];
    unmatched: ArrSample[];
  };
  roots: ArrRoot[];
  path_maps: ArrPathMap[];
  targets: ArrTarget[];
  default_profile: string;
  /** Why nothing can be added yet (no TMDB key); "" when ready. */
  not_ready: string;
}

export interface ArrNote {
  title: string;
  year?: number;
  outcome: "skipped" | "failed" | "imported" | "attached";
  reason: string;
}

/** The import job's result. */
export interface ArrResult {
  app: ArrApp;
  media: "movie" | "series";
  name: string;
  total: number;
  imported: number;
  files_attached: number;
  existing: number;
  skipped: number;
  failed: number;
  paced: number;
  /** Added unmonitored because they were unmonitored over there (a show: with its searching off). */
  unmonitored?: number;
  notes: ArrNote[];
  more?: number;
}

export const arrImportApi = {
  preview: (app: ArrApp, url: string, apiKey: string, choices: ArrImportChoices = {}) =>
    req<ArrPreview>("/api/v1/import/arr/preview", { method: "POST", body: JSON.stringify({ app, url, api_key: apiKey, ...choices }) }),
  run: (app: ArrApp, url: string, apiKey: string, choices: ArrImportChoices) =>
    req<{ status: string; name: string } & JobRef>("/api/v1/import/arr/run", { method: "POST", body: JSON.stringify({ app, url, api_key: apiKey, ...choices }) }),
};
