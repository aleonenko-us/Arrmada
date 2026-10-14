// Every deep link that copy points at lives here, and so does every page name a sentence
// uses. When a page or a setting moves, change it here and every banner, toast and hint
// follows — rather than hunting the old address down string by string.
export const LINKS = {
  apiKeys: "/settings/connections#api-keys",
  connections: "/settings/connections",
  diskGuard: "/settings/downloads#disk-guard",
  recycleBin: "/settings/downloads#recycle-bin",
  libraryFolders: "/settings/library#media-folders",
  // The folders a library scan couldn't identify, under the folder rows.
  libraryReview: "/settings/library#needs-review",
  users: "/settings/users",
  // Each media server's connection is its card on Connections (a link opens its editor
  // there); how Arrmada uses them — library updates, watch monitoring, sign-in, linked
  // accounts — is Settings → Media servers, one group per server.
  plexConnection: "/settings/connections#plex",
  jellyfinConnection: "/settings/connections#jellyfin",
  // Both cards side by side, for "connect a media server" when either will do.
  mediaServerCards: "/settings/connections#media-servers",
  mediaServers: "/settings/media-servers",
  // Each server's group starts with its server card.
  plexServer: "/settings/media-servers#plex-server",
  jellyfinServer: "/settings/media-servers#jellyfin-server",
  plexSignIn: "/settings/media-servers#plex-sign-in",
  jellyfinSignIn: "/settings/media-servers#jellyfin-sign-in",
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
  subtitlesSettings: "/settings/subtitles",
  convertSettings: "/settings/convert",
  requests: "/requests",
  requestsWaiting: "/requests?tab=needs",
  backups: "/settings/system#backups",
  // The outside address alerts and calendar feed links are built from.
  publicURL: "/settings/system#public-url",
  tautulliImport: "/settings/import#tautulli-import",
  audiobookshelfImport: "/settings/import#audiobookshelf-import",
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
