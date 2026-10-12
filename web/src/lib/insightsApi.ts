import { req, type InsightsActivity, type InsightsGraphs, type InsightsHistory, type InsightsStats, type LibraryStat, type PlexConfig, type PlexTestResult, type RecentItem, type Reliability, type UserEntry } from "./api";

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
  insightsHistory: (p: { type?: string; decision?: string; q?: string; page?: number; page_size?: number }) => {
    const qs = new URLSearchParams();
    if (p.type) qs.set("type", p.type);
    if (p.decision) qs.set("decision", p.decision);
    if (p.q) qs.set("q", p.q);
    qs.set("page", String(p.page ?? 1));
    qs.set("page_size", String(p.page_size ?? 50));
    return req<InsightsHistory>(`/api/v1/insights/history?${qs.toString()}`);
  },
  insightsStats: (window = 30, metric?: "plays" | "duration") =>
    req<InsightsStats>(`/api/v1/insights/stats?window=${window}${metric ? `&metric=${metric}` : ""}`),
  insightsUsers: () => req<{ users: UserEntry[] }>("/api/v1/insights/users").then((r) => r.users),
  insightsLibraries: () => req<{ libraries: LibraryStat[] }>("/api/v1/insights/libraries").then((r) => r.libraries),
  insightsRecentlyAdded: (limit = 20) => req<{ items: RecentItem[] }>(`/api/v1/insights/recently-added?limit=${limit}`).then((r) => r.items),
  insightsGraphs: (window = 30) => req<InsightsGraphs>(`/api/v1/insights/graphs?window=${window}`),
  insightsReliability: (window = 30) => req<Reliability>(`/api/v1/insights/reliability?window=${window}`),
};
