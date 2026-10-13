// The Settings hub's map: which sections exist, who sees them, where every control lives
// (for the search box) and where an old link should land. Kept free of components so it
// can be tested on its own; pages/Settings.tsx pairs each id with the component it renders.

export type SectionId = "library" | "media" | "connections" | "downloads" | "tools" | "alerts" | "plex" | "audiobook-server" | "users" | "import" | "system" | "status";

export interface SettingsSection {
  id: SectionId;
  label: string;
  /** Hidden from managers (and from their search results); the server refuses them anyway. */
  adminOnly: boolean;
}

// Rail order. The names match the paths copy already uses ("Settings → Library",
// "Settings → Downloads", "Settings → Connections", "Settings → People & access").
export const SECTIONS: readonly SettingsSection[] = [
  { id: "library", label: "Library", adminOnly: false },
  { id: "media", label: "Naming & metadata", adminOnly: false },
  // Every integration with its live status. Managers run the indexers and download clients
  // day to day; the metadata keys on it are read-only for them.
  { id: "connections", label: "Connections", adminOnly: false },
  { id: "downloads", label: "Downloads", adminOnly: true },
  // The Subtitles and Convert modules' settings (#subtitles, #convert); managers run both.
  { id: "tools", label: "Subtitles & Convert", adminOnly: false },
  // Managers see the alert connections read-only; only admins change them.
  { id: "alerts", label: "Alerts", adminOnly: false },
  // How Arrmada uses Plex: library updates for managers, sign-in for admins. The connection
  // itself is the Plex card on Connections.
  { id: "plex", label: "Plex", adminOnly: false },
  { id: "audiobook-server", label: "Audiobook server", adminOnly: true },
  // The old Users section: one list of everyone with their access (CFG-18).
  { id: "users", label: "People & access", adminOnly: true },
  // Every one-time migration from the apps Arrmada replaces.
  { id: "import", label: "Import & migrate", adminOnly: true },
  { id: "system", label: "System", adminOnly: true },
  // Slot for System → Status (pages/settings/Status.tsx, built by the health work).
  { id: "status", label: "Status", adminOnly: true },
];

export const DEFAULT_SECTION: SectionId = "library";

export function visibleSections(admin: boolean): SettingsSection[] {
  return SECTIONS.filter((s) => admin || !s.adminOnly);
}

export interface SearchEntry {
  label: string;
  keywords: string;
  section: SectionId;
  /** The card's element id, so a result scrolls straight to it. */
  anchor: string;
  /** An admin-only card in a section managers also see (Plex sign-in under Plex). */
  adminOnly?: true;
}

// Every card in every section, with the words someone might type to find it.
export const SEARCH_INDEX: readonly SearchEntry[] = [
  { label: "Media folders", keywords: "movies tv shows ebooks audiobooks music downloads folder path browse scan mount directory unmatched needs review", section: "library", anchor: "media-folders" },
  { label: "Folders that need review", keywords: "scan unmatched needs review identify pick title folder", section: "library", anchor: "needs-review" },
  { label: "Adding titles", keywords: "search on add automatic default", section: "library", anchor: "adding-titles" },
  { label: "Series monitoring", keywords: "series tv show monitor monitoring preset default all future missing existing first latest season new seasons add request", section: "library", anchor: "series-monitoring" },
  { label: "Books", keywords: "book books ebook audiobook language languages english foreign translation mam myanonamouse grab", section: "library", anchor: "books" },
  { label: "Plex recommended naming", keywords: "preset plex tmdb tvdb imdb id edition folder naming", section: "media", anchor: "naming-presets" },
  { label: "Movie naming", keywords: "folder file name format template tokens rename", section: "media", anchor: "movie-naming" },
  { label: "Series naming", keywords: "tv show season episode folder file name format template tokens specials rename", section: "media", anchor: "series-naming" },
  { label: "Metadata", keywords: "nfo movie.nfo artwork poster fanart kodi plex jellyfin emby sidecar", section: "media", anchor: "metadata" },
  { label: "Indexers and Prowlarr", keywords: "indexer indexers tracker trackers torznab prowlarr sync torrentleech 1337x myanonamouse flaresolverr cloudflare search status test", section: "connections", anchor: "search" },
  { label: "Download clients", keywords: "qbittorrent torrent client clients download order bundled status test", section: "connections", anchor: "download-clients" },
  { label: "Other connections", keywords: "plex alerts apprise audiobook server listening apps status connected", section: "connections", anchor: "other-connections" },
  { label: "Plex connection", keywords: "plex server url token connect sign in with plex test", section: "connections", anchor: "plex" },
  { label: "Download clients and indexers", keywords: "qbittorrent prowlarr torrent client indexer", section: "downloads", anchor: "download-links" },
  { label: "Search categories", keywords: "indexer categories category cat newznab torznab book audiobook music filter usual default", section: "downloads", anchor: "search-categories" },
  { label: "Download disk guard", keywords: "disk full space free pause resume threshold percent drive cache", section: "downloads", anchor: "disk-guard" },
  { label: "Stalled downloads", keywords: "stall stuck timeout no progress replacement fail over hours", section: "downloads", anchor: "stalled-downloads" },
  { label: "Upgrades per sweep", keywords: "upgrade limit budget sweep quality profile replace better release grabs at once", section: "downloads", anchor: "upgrade-limit" },
  { label: "Recycle bin", keywords: "trash deleted restore empty retention size cap purge undo", section: "downloads", anchor: "recycle-bin" },
  { label: "Subtitles", keywords: "subtitles srt languages kept auto ensure whisper ai model transcription opensubtitles minimum match score", section: "tools", anchor: "subtitles" },
  { label: "Convert", keywords: "convert transcode encode hevc av1 gpu hours schedule audio tracks commentary image subtitles pgs black bars crop transcode folder cpu cores dolby vision dv hdr", section: "tools", anchor: "convert" },
  { label: "Audiobook server", keywords: "audiobooks audiobookshelf lissen listening apps server switch on off address port public url tunnel trace", section: "audiobook-server", anchor: "audiobook-server" },
  { label: "Alerts", keywords: "alert alerts notification notifications apprise discord telegram ntfy email slack pushover push phone message", section: "alerts", anchor: "alerts" },
  { label: "Outgoing email", keywords: "email mail smtp send outgoing server gmail app password from address kindle test", section: "alerts", anchor: "outgoing-email", adminOnly: true },
  { label: "People & access", keywords: "add user users people accounts roles admin manager requester read-only password auto-approve block delete disable sign in sign out everywhere sessions last seen plex link audiobook access", section: "users", anchor: "users" },
  { label: "Plex server", keywords: "plex server connected status", section: "plex", anchor: "plex-server" },
  { label: "Watch monitoring", keywords: "plex monitoring record watched insights poll interval seconds activity history", section: "plex", anchor: "plex-monitoring" },
  { label: "Plex library updates", keywords: "plex scan partial refresh path mapping map folder update library after import rename delete convert", section: "plex", anchor: "plex-library-updates" },
  { label: "Plex sign-in", keywords: "plex login sign in with plex auto-approve home shared users staff admin owner policy", section: "plex", anchor: "plex-sign-in", adminOnly: true },
  { label: "Linked Plex accounts", keywords: "plex linked accounts link unlink who family users", section: "plex", anchor: "plex-accounts", adminOnly: true },
  { label: "Request limits", keywords: "quota quotas request limits per week movies seasons books fair use household", section: "users", anchor: "request-limits" },
  { label: "Import from Overseerr / Jellyseerr", keywords: "overseerr jellyseerr requests migrate import", section: "import", anchor: "overseerr-import" },
  { label: "Import from Tautulli", keywords: "tautulli watch history plex insights migrate import", section: "import", anchor: "tautulli-import" },
  { label: "Import from Audiobookshelf", keywords: "audiobookshelf abs absdatabase listening places progress bookmarks migrate import audiobooks", section: "import", anchor: "audiobookshelf-import" },
  { label: "Modules", keywords: "books music enable disable turn on off preview module", section: "system", anchor: "modules" },
  { label: "API keys", keywords: "tmdb tvdb omdb hardcover opensubtitles key token metadata credentials connections", section: "connections", anchor: "api-keys" },
  { label: "Discovery region", keywords: "country region tmdb discover localize popular upcoming", section: "connections", anchor: "discovery-region", adminOnly: true },
  { label: "Outside address", keywords: "public url address domain tunnel external link alerts calendar feed outside", section: "system", anchor: "public-url" },
  { label: "Restart", keywords: "restart reboot logs", section: "system", anchor: "restart" },
  { label: "Run setup again", keywords: "setup wizard first run getting started onboarding", section: "system", anchor: "setup" },
  { label: "Backups", keywords: "backup restore database copy download upload nightly schedule", section: "system", anchor: "backups" },
  { label: "Status", keywords: "status health version uptime", section: "status", anchor: "status" },
  { label: "Updates", keywords: "update updates upgrade release releases new version check github notes changelog", section: "status", anchor: "updates" },
];

// searchSettings finds the cards whose name or keywords contain every word typed, among
// the sections this viewer can open (admin-only cards for admins only).
export function searchSettings(query: string, visible: readonly string[], admin = true): SearchEntry[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  const sectionLabel = new Map(SECTIONS.map((s) => [s.id, s.label]));
  return SEARCH_INDEX.filter((e) => {
    if (!visible.includes(e.section) || (e.adminOnly && !admin)) return false;
    const hay = `${e.label} ${e.keywords} ${sectionLabel.get(e.section) ?? ""}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

// Settings used to be one page of tabs (?tab=system with #api-keys and friends). Those links live on
// in bookmarks and old copy, so each tab and each card anchor has a new home.
const TAB_SECTION: Record<string, SectionId> = { media: "media", library: "library", system: "system", users: "users" };
const ANCHOR_SECTION: Record<string, SectionId> = Object.fromEntries(SEARCH_INDEX.map((e) => [e.anchor, e.section]));
// Cards that moved and changed their anchor on the way: the old anchor's new section and id.
// The Plex connection editor left Settings → Plex for the Plex card on Connections (CFG-16).
const RENAMED_ANCHOR: Record<string, { section: SectionId; anchor: string }> = {
  "plex-connection": { section: "connections", anchor: "plex" },
};

// sectionFromPath is the section named in /settings/<section>[/…], or "" for bare /settings.
export function sectionFromPath(pathname: string): string {
  const m = /^\/settings\/([^/]+)/.exec(pathname);
  if (!m) return "";
  try { return decodeURIComponent(m[1]); } catch { return m[1]; }
}

// settingsRedirect says where a /settings URL should go instead, or null when it already
// names a section this viewer can open. A card anchor wins over ?tab= (it's the more
// specific of the two), and over the section in the path when the card has moved to
// another section (/settings/system#api-keys → /settings/connections#api-keys); a section
// this viewer can't open falls back to the default, like an unknown ?tab= used to. ?tab=
// is dropped; other query params and the #anchor are kept, except a renamed card's old
// anchor, which becomes its new one.
export function settingsRedirect(pathname: string, search: string, hash: string, visible: readonly string[]): string | null {
  const renamed = RENAMED_ANCHOR[hash.replace(/^#/, "")];
  if (renamed && visible.includes(renamed.section)) {
    const params = new URLSearchParams(search);
    params.delete("tab");
    const qs = params.toString();
    return `/settings/${renamed.section}${qs ? `?${qs}` : ""}#${renamed.anchor}`;
  }
  const current = sectionFromPath(pathname);
  const fromAnchor = ANCHOR_SECTION[hash.replace(/^#/, "")];
  if (current && visible.includes(current) && (!fromAnchor || fromAnchor === current || !visible.includes(fromAnchor))) return null;
  const params = new URLSearchParams(search);
  const fromTab = TAB_SECTION[params.get("tab") ?? ""];
  const target = [fromAnchor, fromTab].find((s) => s && visible.includes(s)) ?? DEFAULT_SECTION;
  params.delete("tab");
  const qs = params.toString();
  return `/settings/${target}${qs ? `?${qs}` : ""}${hash}`;
}
