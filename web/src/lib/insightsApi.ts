import { req, type InsightsActivity, type InsightsGraphs, type InsightsHistory, type InsightsLibrary, type InsightsServer, type InsightsServerStatus, type InsightsStats, type LibraryStat, type PersonProfile, type PlexConfig, type PlexTestResult, type RecentItem, type Reliability, type UserEntry } from "./api";

// History's filters: user is a person's id on their server, from/to epoch seconds (to
// exclusive), group folds resumed sittings into one row, server narrows to one server.
export interface HistoryQuery { type?: string; decision?: string; q?: string; user?: string; from?: number; to?: number; group?: boolean; server?: InsightsServer }

function historyParams(p: HistoryQuery): URLSearchParams {
  const qs = new URLSearchParams();
  if (p.type) qs.set("type", p.type);
  if (p.decision) qs.set("decision", p.decision);
  if (p.q) qs.set("q", p.q);
  if (p.user) qs.set("user", p.user);
  if (p.from) qs.set("from", String(p.from));
  if (p.to) qs.set("to", String(p.to));
  if (p.group) qs.set("group", "1");
  if (p.server) qs.set("server", p.server);
  return qs;
}

// srv is the "&server=…" a read narrowed to one server adds (nothing for both).
function srv(server?: InsightsServer, first = false): string {
  return server ? `${first ? "?" : "&"}server=${server}` : "";
}

// Insights' calls (watch monitoring on Plex and Jellyfin). They live here rather than in
// api.ts so they only download with the Insights and Plex settings pages, not with a
// requester's first load. Every read takes an optional server: absent means both.
export const insightsApi = {
  insightsConfig: () => req<PlexConfig>("/api/v1/insights/plex"),
  // Each field is optional; a missing url or token keeps the saved one.
  updateInsightsConfig: (body: { url?: string; token?: string; enabled?: boolean; poll_seconds?: number }) =>
    req<PlexConfig>("/api/v1/insights/plex", { method: "PUT", body: JSON.stringify(body) }),
  testInsights: (body: { url?: string; token?: string }) =>
    req<PlexTestResult>("/api/v1/insights/plex/test", { method: "POST", body: JSON.stringify(body) }),
  // Each server Insights can watch, connected or not, and what its monitoring is doing.
  insightsServers: () => req<{ servers: InsightsServerStatus[] }>("/api/v1/insights/servers").then((r) => r.servers),
  // Jellyfin's "record what's played" switch and poll interval (a manager's, like Plex's).
  updateJellyfinMonitoring: (body: { monitoring_enabled?: boolean; poll_seconds?: number }) =>
    req<unknown>("/api/v1/jellyfin/monitoring", { method: "PUT", body: JSON.stringify(body) }),
  insightsActivity: (server?: InsightsServer) => req<InsightsActivity>(`/api/v1/insights/activity${srv(server, true)}`),
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
  insightsStats: (window = 30, metric?: "plays" | "duration", server?: InsightsServer) =>
    req<InsightsStats>(`/api/v1/insights/stats?window=${window}${metric ? `&metric=${metric}` : ""}${srv(server)}`),
  insightsUsers: (server?: InsightsServer) => req<{ users: UserEntry[] }>(`/api/v1/insights/users${srv(server, true)}`).then((r) => r.users),
  insightsLibrary: (window = 90, metric: "plays" | "duration" = "plays", server?: InsightsServer) =>
    req<InsightsLibrary>(`/api/v1/insights/library?window=${window}&metric=${metric}${srv(server)}`),
  // window = days, 0 = all time.
  insightsPerson: (id: string, window = 0) => req<PersonProfile>(`/api/v1/insights/users/${encodeURIComponent(id)}?window=${window}`),
  insightsLibraries: (server?: InsightsServer) => req<{ libraries: LibraryStat[] }>(`/api/v1/insights/libraries${srv(server, true)}`).then((r) => r.libraries),
  insightsRecentlyAdded: (limit = 20, server?: InsightsServer) =>
    req<{ items: RecentItem[] }>(`/api/v1/insights/recently-added?limit=${limit}${srv(server)}`).then((r) => r.items),
  insightsGraphs: (window = 30, metric: "plays" | "duration" = "plays", server?: InsightsServer) =>
    req<InsightsGraphs>(`/api/v1/insights/graphs?window=${window}&metric=${metric}${srv(server)}`),
  insightsReliability: (window = 30, server?: InsightsServer) => req<Reliability>(`/api/v1/insights/reliability?window=${window}${srv(server)}`),
};
