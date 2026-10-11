// Every deep link that copy points at lives here, and so does every page name a sentence
// uses. When a page or a setting moves, change it here and every banner, toast and hint
// follows — rather than hunting the old address down string by string.
export const LINKS = {
  apiKeys: "/settings/connections#api-keys",
  connections: "/settings/connections",
  diskGuard: "/settings/downloads#disk-guard",
  recycleBin: "/settings/downloads#recycle-bin",
  libraryFolders: "/settings/library#media-folders",
  users: "/settings/users",
  plexConnection: "/settings/plex#plex-connection",
  plexSignIn: "/settings/plex#plex-sign-in",
  // The Activity page (lib/activity.ts): one tab each, sub-views in ?show=.
  activityQueue: "/activity/queue",
  downloadsSearching: "/activity/wanted",
  // Stuck downloads: their section of Activity → Needs you, each with its own action.
  downloadProblems: "/activity/needs-you#stalled",
  importProblems: "/activity/needs-you#import-failures",
  wrongCategory: "/activity/needs-you#wrong-category",
  unmanaged: "/activity/needs-you#unmanaged",
  history: "/activity/history",
  blocklist: "/activity/blocklist",
  downloadClients: "/settings/connections#download-clients",
  indexers: "/settings/connections#search",
  subtitlesSettings: "/settings/tools#subtitles",
  convertSettings: "/settings/tools#convert",
  requests: "/requests",
  requestsWaiting: "/requests?tab=needs",
  backups: "/settings/system#backups",
  audiobookServer: "/settings/audiobook-server",
  status: "/settings/status",
  tasks: "/settings/status#tasks",
  review: "/activity/needs-you",
  alerts: "/settings/alerts",
} as const;

// fixLink is where a health warning sends you: the LINKS entry the server named, else the
// server's own path. Only in-app paths are followed; anything else gets no link.
export function fixLink(w: { link?: string; link_key?: string }): string | undefined {
  const known = w.link_key && Object.prototype.hasOwnProperty.call(LINKS, w.link_key)
    ? LINKS[w.link_key as keyof typeof LINKS]
    : undefined;
  const to = known ?? w.link;
  return to && to.startsWith("/") && !to.startsWith("//") ? to : undefined;
}

// Page names as they appear in the sidebar, for sentences like "follow it in Activity".
export const PAGE = {
  activity: "Activity",
  settings: "Settings",
  insights: "Insights",
} as const;
