import { req, type InsightsActivity, type InsightsGraphs, type InsightsHistory, type InsightsLibrary, type InsightsStats, type LibraryStat, type PersonProfile, type PlexConfig, type PlexTestResult, type RecentItem, type Reliability, type UserEntry } from "./api";

// History's filters: user is a Plex user id, from/to epoch seconds (to exclusive), group
// folds resumed sittings into one row.
export interface HistoryQuery { type?: string; decision?: string; q?: string; user?: string; from?: number; to?: number; group?: boolean }

function historyParams(p: HistoryQuery): URLSearchParams {
  const qs = new URLSearchParams();
  if (p.type) qs.set("type", p.type);
  if (p.decision) qs.set("decision", p.decision);
  if (p.q) qs.set("q", p.q);
  if (p.user) qs.set("user", p.user);
  if (p.from) qs.set("from", String(p.from));
  if (p.to) qs.set("to", String(p.to));
  if (p.group) qs.set("group", "1");
  return qs;
}

// Insights' calls (Plex watch monitoring). They live here rather than in api.ts so they only
// download with the Insights and Plex settings pages, not with a requester's first load.
export const insightsApi = {
  insightsConfig: () => req<PlexConfig>("/api/v1/insights/plex"),
  // Each field is optional; a missing url or token keeps the saved one.
  updateInsightsConfig: (body: { url?: string; token?: string; enabled?: boolean; poll_seconds?: number }) =>
    req<PlexConfig>("/api/v1/insights/plex", { method: "PUT", body: JSON.stringify(body) }),
  testInsights: (body: { url?: string; token?: string }) =>
    req<PlexTestResult>("/api/v1/insights/plex/test", { method: "POST", body: JSON.stringify(body) }),
  insightsActivity: () => req<InsightsActivity>("/api/v1/insights/activity"),
  insightsHistory: (p: HistoryQuery & { page?: number; page_size?: number }) => {
    const qs = historyParams(p);
    qs.set("page", String(p.page ?? 1));
    qs.set("page_size", String(p.page_size ?? 50));
    return req<InsightsHistory>(`/api/v1/insights/history?${qs.toString()}`);
  },
  // The CSV download's address, for a plain link (the session cookie authenticates it).
  insightsHistoryCSVURL: (p: HistoryQuery) => {
    const qs = historyParams(p).toString();
    return `/api/v1/insights/history.csv${qs ? `?${qs}` : ""}`;
  },
  insightsStats: (window = 30, metric?: "plays" | "duration") =>
    req<InsightsStats>(`/api/v1/insights/stats?window=${window}${metric ? `&metric=${metric}` : ""}`),
  insightsUsers: () => req<{ users: UserEntry[] }>("/api/v1/insights/users").then((r) => r.users),
  insightsLibrary: (window = 90, metric: "plays" | "duration" = "plays") => req<InsightsLibrary>(`/api/v1/insights/library?window=${window}&metric=${metric}`),
  // window = days, 0 = all time.
  insightsPerson: (id: string, window = 0) => req<PersonProfile>(`/api/v1/insights/users/${encodeURIComponent(id)}?window=${window}`),
  insightsLibraries: () => req<{ libraries: LibraryStat[] }>("/api/v1/insights/libraries").then((r) => r.libraries),
  insightsRecentlyAdded: (limit = 20) => req<{ items: RecentItem[] }>(`/api/v1/insights/recently-added?limit=${limit}`).then((r) => r.items),
  insightsGraphs: (window = 30, metric: "plays" | "duration" = "plays") => req<InsightsGraphs>(`/api/v1/insights/graphs?window=${window}&metric=${metric}`),
  insightsReliability: (window = 30) => req<Reliability>(`/api/v1/insights/reliability?window=${window}`),
};
