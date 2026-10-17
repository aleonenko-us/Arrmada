import type {
  APIKeyStatus, AppSettings, Attention, AuthUser, BackupsState, MyQuota, AudioListening, FolderCheck, Health, Job, LibraryPaths, MyAudio, PendingRestart, Person, RecycleStats, SetupState, Status, SystemHealth, SystemStatus,
  RecentJob, ReviewTarget, TaskStatus, UnmanagedDownload,
} from "../../src/lib/api";
import type { LogLevelState, LogPage } from "../../src/lib/systemApi";
import type { SetupChecklist } from "../../src/lib/setupApi";
import type { LibraryFolders } from "../../src/lib/foldersApi";
import type { ArrPreview, ArrResult } from "../../src/lib/importApi";
import type { PersonaInfo } from "./users";
import { NOW } from "./clock";

export function status(p: PersonaInfo): Status {
  return {
    app: "arrmada", version: "e2e", commit: "e2e0000", started_at: "2026-10-09T08:00:00Z", uptime_seconds: 14400,
    needs_setup: false, authenticated: true, external: p.external,
    modules: [
      { id: "movies", name: "Movies", enabled: true, status: "ok" },
      { id: "series", name: "Series", enabled: true, status: "ok" },
    ],
    books_enabled: true, music_enabled: true, plex_login: false, jellyfin_login: false, jellyfin_quickconnect: false, metadata_ready: true,
  };
}

export const health: Health = { status: "ok", version: "e2e", commit: "e2e0000", uptime_seconds: 14400, checks: { db: "ok" } };

export const systemHealth: SystemHealth = { status: "ok", warnings: [], disk: { free_gb: "812.4", path: "/media" } };

// The Needs-you feed: two requests waiting, a held import, a stalled download, an import
// that keeps failing, a TV download in the wrong category, stuck searches and one health
// warning — enough for every badge, card row and Needs-you section to render.
const ago = (min: number) => NOW - min * 60_000;
export const STALLED_HASH = "abc1230000000000000000000000000000000001";
export const FAILING_HASH = "abc1230000000000000000000000000000000002";
export const MISFILED_HASH = "abc1230000000000000000000000000000000003";
// Finished torrents Arrmada didn't grab, for Needs you's Not managed section.
export const unmanaged: { unmanaged: UnmanagedDownload[]; ignored: number } = {
  unmanaged: [
    { hash: "e2e0000000000000000000000000000000000005", name: "Some.Linux.Distro.ISO-FIXTURE", size_bytes: 3_000_000_000, category: "linux", kind: "movie", title: "Some Linux Distro ISO" },
    { hash: "e2e0000000000000000000000000000000000006", name: "Night.Train.1999.1080p.BluRay-HAND", size_bytes: 9_000_000_000, kind: "movie", title: "Night Train" },
  ],
  ignored: 1,
};
// The library items Import into… offers, of the kind asked for.
export function unmanagedTargets(url: URL): { targets: ReviewTarget[]; truncated: boolean } {
  const kind = url.searchParams.get("kind");
  const targets: ReviewTarget[] = kind === "series"
    ? [{ id: 1, kind: "series", title: "Harbour Lights", year: 2024 }]
    : [{ id: 7, kind: "movie", title: "Night Train", year: 1999 }, { id: 8, kind: "movie", title: "Night Moves", year: 1975 }];
  const q = (url.searchParams.get("q") ?? "").toLowerCase();
  return { targets: targets.filter((t) => !q || t.title.toLowerCase().includes(q)), truncated: false };
}
// Import into… starts a download.import job (202); this is how it ends by default.
export const unmanagedImportJob: Job = {
  id: 913, kind: "download.import", target: "torrent:e2e0000000000000000000000000000000000006", trigger: "user:1", status: "succeeded", progress: 1,
  message: "Imported into Night Train.", error: "",
  created_at: new Date(NOW).toISOString(), started_at: new Date(NOW).toISOString(), finished_at: new Date(NOW).toISOString(),
};
export const unmanagedImportStarted = { status: "importing", job_id: unmanagedImportJob.id, existing: false, title: "Night Train" };
// Needs you's "Try another release" job, finished.
export const failOverJob: Job = {
  id: 901, kind: "download.failover", target: `torrent:${STALLED_HASH}`, trigger: "user:1", status: "succeeded", progress: 1,
  message: "Grabbed Old.Film.1080p.WEB-DL-NEW instead — the stuck download was removed and blocklisted.", error: "",
  created_at: new Date(NOW).toISOString(), started_at: new Date(NOW).toISOString(), finished_at: new Date(NOW).toISOString(),
};
export const attention: Attention = {
  at: new Date(NOW - 10_000).toISOString(),
  stale: false,
  counts: { requests: 2, reviews: 1, downloads: 1, imports: 2, searches: 3, health: 1, health_errors: 0, total: 10 },
  groups: [
    { kind: "health", level: "warning", count: 1, title: "1 health problem", link: "/settings/status", link_key: "status" },
    { kind: "request", level: "warning", count: 2, title: "2 requests are waiting for approval", link: "/requests?tab=needs", link_key: "requestsWaiting", sample: ["deckhand requested Dune (2021)", "deckhand requested Severance (2022)"] },
    { kind: "review", level: "warning", count: 1, title: "1 import needs review", link: "/activity/needs-you", link_key: "review", sample: ["Alien is held: it looks like a different title"] },
    { kind: "stalled", level: "warning", count: 1, title: "1 download has stalled", link: "/activity/needs-you#stalled", link_key: "downloadProblems", sample: ["Old.Film.1080p has stalled"] },
    { kind: "import", level: "warning", count: 1, title: "1 import keeps failing", link: "/activity/needs-you#import-failures", link_key: "importProblems", sample: ["Heat.1995.1080p"] },
    { kind: "wrongcat", level: "warning", count: 1, title: "1 download is in the wrong category", link: "/activity/needs-you#wrong-category", link_key: "wrongCategory", sample: ["Harbour.Lights.S03E01.1080p"] },
    { kind: "search", level: "warning", count: 3, title: "3 titles still haven't found a release", link: "/activity/wanted", link_key: "downloadsSearching" },
  ],
  items: [
    { key: "health:backups.stale", kind: "health", level: "warning", title: "The last backup is 3 days old", link: "/settings/system#backups", link_key: "backups", link_label: "Backups", since: ago(600) },
    { key: "request:11", kind: "request", level: "warning", title: "deckhand requested Dune (2021)", detail: "Movie", link: "/requests?tab=needs", link_key: "requestsWaiting", since: ago(90) },
    { key: "request:12", kind: "request", level: "warning", title: "deckhand requested Severance (2022)", detail: "Series", link: "/requests?tab=needs", link_key: "requestsWaiting", since: ago(30) },
    { key: "review:4", kind: "review", level: "warning", title: "Alien is held: it looks like a different title", link: "/activity/needs-you", link_key: "review", since: ago(240) },
    { key: "stalled:abc123", kind: "stalled", level: "warning", title: "Old.Film.1080p has stalled", detail: "Nobody has sent it any data for 2h", link: "/activity/needs-you#stalled", link_key: "downloadProblems", since: ago(120), hash: STALLED_HASH, grabbed: true },
    { key: `import:${FAILING_HASH}`, kind: "import", level: "warning", title: "Importing Heat.1995.1080p keeps failing (3 tries)", detail: "permission denied", link: "/activity/needs-you#import-failures", link_key: "importProblems", since: ago(45), hash: FAILING_HASH, grabbed: true },
    { key: `wrongcat:${MISFILED_HASH}`, kind: "wrongcat", level: "warning", title: "Harbour.Lights.S03E01.1080p finished in “linux”", detail: "Arrmada only imports TV from “arrmada-tv”", link: "/activity/needs-you#wrong-category", link_key: "wrongCategory", since: ago(20), hash: MISFILED_HASH },
    { key: "search:stuck", kind: "search", level: "warning", title: "3 titles still haven't found a release", link: "/activity/wanted", link_key: "downloadsSearching", since: ago(2000), count: 3 },
  ],
};

const paths: LibraryPaths = { movies: "/media/movies", tv: "/media/tv", ebooks: "/media/ebooks", audiobooks: "/media/audiobooks", music: "/media/music", downloads: "/media/downloads" };

// The Library section of the Settings hub reads the same folders.
export const libraryPaths: LibraryPaths = paths;

// The Movies and TV folders: one each, the default — what an install that never added a
// folder answers, so no picker or Move to folder shows anywhere. Specs for extra folders
// override it with libraryFoldersMulti.
export const libraryFolders: LibraryFolders = {
  movie: { multi: false, folders: [{ id: 0, media: "movie", path: paths.movies, label: "Movies", rule: "", position: 0, default: true, titles: 12 }] },
  series: { multi: false, folders: [{ id: 0, media: "series", path: paths.tv, label: "TV", rule: "", position: 0, default: true, titles: 4 }] },
};

// Settings → Import & migrate → Radarr / Sonarr: what a Radarr with a Movies and a Kids
// root folder answers (Radarr calls the share /movies, Arrmada /media/movies), and the
// finished import job.
export const arrPreview: ArrPreview = {
  preview: {
    app: "radarr", media: "movie", name: "Radarr 4K", version: "6.4.4",
    counts: { titles: 4, existing: 1, new: 3, unmatchable: 0, with_files: 2, files_found: 1, not_found: 1, attachable: 0, wanted: 1 },
    profiles: [
      { id: 4, name: "HD-1080p", max_resolution: 1080, titles: 3, suggested: "custom:1" },
      { id: 5, name: "Ultra-HD", max_resolution: 2160, titles: 1, suggested: "custom:2" },
      { id: 6, name: "SD", max_resolution: 576, titles: 0, suggested: "custom:1" },
    ],
    not_found: [{ title: "Up", year: 2009, path: "/kids/Up (2009)/Up.mkv", reason: "file not found at /kids/Up (2009)/Up.mkv" }],
    unmatched: [],
  },
  roots: [
    { path: "/kids", titles: 1, free_bytes: 0, folder_id: null, suggested_path: "" },
    { path: "/movies", titles: 3, free_bytes: 400e9, folder_id: 0, matched_by: "name", suggested_path: "" },
  ],
  path_maps: [{ from: "/movies", to: "/media/movies" }],
  targets: [
    { key: "custom:1", name: "1080p Sensible", max_resolution: 1080, default: true },
    { key: "custom:2", name: "4K Sensible", max_resolution: 2160, default: false },
  ],
  default_profile: "custom:1",
  not_ready: "",
};
export const arrImportResult: ArrResult = {
  app: "radarr", media: "movie", name: "Radarr 4K", total: 4, imported: 3, files_attached: 2, existing: 1, skipped: 0, failed: 0, paced: 1,
  notes: [{ title: "Up", year: 2009, outcome: "imported", reason: "added without its file (file not found at /kids/Up (2009)/Up.mkv)" }],
};
export const arrImportJob: Job = {
  id: 812, kind: "import.arr", target: "arr", trigger: "user:1", status: "succeeded", progress: 1,
  message: "Imported 3 films · 2 files recorded · 1 already here · 0 skipped · 0 failed · 1 wanted, searched a few at a time", error: "",
  result: arrImportResult,
  created_at: new Date(NOW).toISOString(), started_at: new Date(NOW).toISOString(), finished_at: new Date(NOW).toISOString(),
};

// Movies with a Kids folder (rule: kids) and TV with an Anime folder (rule: anime).
export const libraryFoldersMulti: LibraryFolders = {
  movie: { multi: true, folders: [...libraryFolders.movie.folders, { id: 3, media: "movie", path: "/media/kids", label: "Kids", rule: "kids", position: 1, default: false, titles: 2 }] },
  series: { multi: true, folders: [...libraryFolders.series.folders, { id: 4, media: "series", path: "/media/anime", label: "Anime", rule: "anime", position: 1, default: false, titles: 1 }] },
};

// Every folder checks out: there, writable, and hardlink-able with downloads.
export function folderCheck(path: string): FolderCheck {
  return {
    path, exists: true, is_dir: true, writable: true, hardlink_with_downloads: true, under_data_dir: false,
    free_bytes: 2.4e12, total_bytes: 8e12, entries: 12, entries_capped: false,
  };
}

// The keys setup reads: TMDB and Hardcover saved (Run setup again shows them masked), the
// rest not set. freshKeys is the same list on a new install, nothing set.
const setupKey = (id: string, label: string, hint?: string): APIKeyStatus => ({
  id, label, purpose: "", help_url: "", steps: "", secret: id !== "opensubtitles_username", testable: id !== "opensubtitles_username" && id !== "opensubtitles_password",
  tests_candidate: true, configured: !!hint, source: hint ? "settings" : "", hint, env_set: false,
});
export const setupKeys: APIKeyStatus[] = [
  setupKey("tmdb", "TMDB", "…9e14"), setupKey("tvdb", "TheTVDB"), setupKey("omdb", "OMDb"), setupKey("hardcover", "Hardcover", "…c0de"),
  setupKey("opensubtitles_api", "OpenSubtitles API key"), setupKey("opensubtitles_username", "OpenSubtitles username"), setupKey("opensubtitles_password", "OpenSubtitles password"),
];
export const freshKeys: APIKeyStatus[] = setupKeys.map((k) => ({ ...k, configured: false, source: "", hint: undefined }));

// Setup is finished, so the staff console opens straight onto its pages.
export const setup: SetupState = {
  needed: false, complete: true, tmdb_configured: true, libraries_chosen: true, keys: setupKeys,
  library: paths, running: paths, restart_needed: false, can_restart: true, mounts: ["/media"], suggestions: {},
};

// Settings → System's Backups card: no copies yet, the nightly on at 3 am.
export const backups: BackupsState = {
  backups: [], total_bytes: 0, free_bytes: 5e11, dir: "/appdata/backups",
  settings: { enabled: true, hour: 3, keep_nightly: 7 },
  last_nightly_at: null, can_restart: true, pending_restore: null, last_restore: null,
};

// Every Getting started step is done on this install, so the Dashboard shows no card.
export const setupChecklist: SetupChecklist = {
  items: [
    { key: "tmdb", label: "Add your TMDB key, for titles, artwork and Discover", done: true, link: "/settings/connections#api-keys", link_key: "apiKeys" },
    { key: "folders", label: "Choose your library folders", done: true, link: "/settings/library#media-folders", link_key: "libraryFolders" },
    { key: "download_client", label: "Connect a download client", done: true, link: "/settings/connections#download-clients", link_key: "downloadClients" },
    { key: "indexer", label: "Add an indexer, or sync them from Prowlarr", done: true, link: "/settings/connections#search", link_key: "indexers" },
    { key: "media_server", label: "Connect your media server (Plex or Jellyfin)", done: true, link: "/settings/connections#media-servers", link_key: "mediaServerCards" },
    { key: "first_title", label: "Add your first movie or show", done: true, link: "/discover" },
    { key: "first_import", label: "Get your first download into the library", done: true, link: "/activity/history", link_key: "history" },
  ],
  done: true,
  dismissed: false,
};

export const pendingRestart: PendingRestart = {
  restart_needed: false, can_restart: true, changed: [],
  busy: { convert_running: 0, convert_longest_sec: 0, convert_progress: 0, subtitles_running: 0, subtitles_queued: 0 },
};

export function myAudio(p: PersonaInfo): MyAudio {
  return {
    enabled: true, running: true, host_port: "13378", public_url: "",
    username: p.user.username, allowed: true, has_password: false, min_password_length: 8, devices: [], places: [], removed: [],
  };
}

// Your own listening, never anyone's titles: an empty month is enough here.
export const myListening: AudioListening = { days: 30, since: "2026-09-09", daily: [], totals: [], sessions: [] };

export const settings: AppSettings = {
  search_on_add: true,
  indexer_default_categories: true,
  series_monitor_default: "all",
  naming_movie_folder: "{Title} ({Year})",
  naming_movie_file: "{Title} ({Year}) {Quality}",
  naming_series_folder: "{Title} ({Year})",
  naming_series_season: "Season {Season:00}",
  naming_series_episode: "{Title} - S{Season:00}E{Episode:00}",
  write_nfo: false,
  download_artwork: true,
  books_enabled: true,
  music_enabled: true,
  plex_login_enabled: false,
  plex_signin_staff: false,
  two_factor_admins: false,
  plex_login_auto_approve: false,
  plex_login_auto_approve_types: "movie",
  jellyfin_login_enabled: false,
  jellyfin_quickconnect_enabled: true,
  jellyfin_signin_staff: false,
  jellyfin_login_auto_approve_types: "movie",
  request_quota_days: 7,
  request_quota_movies: 0,
  request_quota_seasons: 0,
  request_quota_books: 0,
  tmdb_region: "US",
  recycle_max_gb: "50",
  recycle_retention_days: "30",
  downloads_disk_guard: true,
  downloads_disk_guard_pause_pct: "95",
  downloads_disk_guard_resume_pct: "90",
  downloads_stall_minutes: 60,
  upgrade_max_grabs_per_sweep: 10,
  app_public_url: "",
  books_languages: ["en"],
  match_title_languages: [],
};

export const recycle: RecycleStats = {
  enabled: true, dir: "/media/.recycle", files: 3, bytes: 4_200_000_000, max_gb: 50, retention_days: 30,
  over_cap_bytes: 0, protected_bytes: 0, largest_item_bytes: 2_100_000_000,
  bins: [
    {
      key: "movies", dir: "/media/movies/.arrmada-recycle", label: "Movies", legacy: false, files: 3, bytes: 4_200_000_000,
      free_bytes: 900_000_000_000, free_known: true, other_drive: false,
    },
  ],
};

// Settings → Status's Tasks table (GET /api/v1/system/tasks): one task that ran fine and
// one failing, so both result chips render.
const iso = (ms: number) => new Date(ms).toISOString();
export const tasks: TaskStatus[] = [
  {
    name: "rss-sync", label: "Check indexer feeds for new movies", description: "Reads each indexer's recent uploads and grabs any wanted movie.",
    interval_seconds: 900, running: false, last_start: iso(NOW - 120_000), last_end: iso(NOW - 118_000), last_duration_ms: 2000,
    last_status: "ok", last_ok: true, last_error: "", last_error_at: null, runs: 16, failures: 0, consecutive_failures: 0, skipped: 0,
    next_run: iso(NOW + 780_000),
  },
  {
    name: "db-backup", label: "Back up the database", description: "Takes the nightly database backup and keeps the newest seven.",
    interval_seconds: 3600, running: false, last_start: iso(NOW - 600_000), last_end: iso(NOW - 599_000), last_duration_ms: 1000,
    last_status: "failed", last_ok: false, last_error: "disk full", last_error_at: iso(NOW - 599_000), runs: 4, failures: 3,
    consecutive_failures: 3, skipped: 0, next_run: iso(NOW + 3_000_000),
  },
];

// The job runner (GET /api/v1/jobs): nothing recent.
export const jobs: { jobs: Job[] } = { jobs: [] };

// Recent jobs under the tasks (OBS-20): one search that worked, one that failed.
export const recentJobs: { jobs: RecentJob[] } = {
  jobs: [
    {
      id: 12, kind: "movie.search", label: "Search for a movie", status: "succeeded", message: "Grabbed a 1080p release", target: "movie:1",
      target_label: "Arrival (2016)", target_path: "/movies/1", trigger_label: "admin", created_at: iso(NOW - 90_000),
      started_at: iso(NOW - 90_000), finished_at: iso(NOW - 88_000), duration_ms: 2000,
    },
    {
      id: 11, kind: "series.search", label: "Search for a show", status: "failed", error: "every indexer failed", target: "series:2",
      target_label: "Severance", target_path: "/series/2", trigger_label: "Request #4", trigger_path: "/requests?id=4",
      created_at: iso(NOW - 600_000), started_at: iso(NOW - 600_000), finished_at: iso(NOW - 597_000), duration_ms: 3000,
    },
  ],
};

export const logs: LogPage = {
  entries: [
    { seq: 1, time_ms: NOW - 60_000, level: "INFO", msg: "library scan finished", attrs: "movies=42" },
    { seq: 2, time_ms: NOW - 30_000, level: "WARN", msg: "indexer slow to answer", attrs: "indexer=Fixture", module: "indexer" },
  ],
  last_seq: 2,
  run: 1,
  capacity: 50000,
};

// The Logs page's debug switch: at the install's level.
export const logLevel: LogLevelState = { level: "info", env_level: "info", max_minutes: 240 };

// Settings → Users: the owner, and a Plex sign-in from before per-type auto-approve who
// still auto-approves every type (shows included).
export const users: { users: AuthUser[] } = {
  users: [
    { id: 1, username: "owner@example.com", role: "admin", auto_approve: false, created_at: "2026-01-01 10:00:00" },
    {
      id: 5, username: "Grandad", role: "requester", plex_linked: true, created_at: "2026-02-01 10:00:00",
      auto_approve: true, auto_approve_movie: true, auto_approve_series: true, auto_approve_book: true,
    },
  ],
};

// System → Status: a build with its commit, a 180 MB database, a nightly backup from three
// hours ago, and every library on one array with the downloads on their own cache drive.
export const systemStatus: SystemStatus = {
  version: "2026.10.08", commit: "abc1234", go: "go1.25.1", started_at: "2026-10-09T08:00:00Z", uptime_seconds: 14400,
  data_dir: "/data", db_bytes: 180 * 1024 ** 2,
  last_backup: { at: new Date(NOW - 3 * 3_600_000).toISOString(), kind: "nightly", file: "arrmada-nightly-20261011-030000.db" },
  disks: [
    { name: "Movies", path: "/media/movies", measurable: true, total: 16e12, free: 4e12, used_pct: 75, shared_with: [{ name: "TV", path: "/media/tv" }, { name: "Books & audiobooks", path: "/media/books" }] },
    { name: "Downloads", path: "/media/downloads", measurable: true, total: 1e12, free: 0.08e12, used_pct: 92, shared_with: [] },
    { name: "Database & backups", path: "/data", measurable: true, total: 0.5e12, free: 0.4e12, used_pct: 20, shared_with: [] },
  ],
  // A build of main, three commits behind it; the newest release is v1.3.0.
  update: {
    enabled: true, current: "2026.10.08", channel: "main", checked_at: new Date(NOW - 2 * 3_600_000).toISOString(),
    latest: {
      tag: "v1.3.0", name: "v1.3.0", url: "https://github.com/tristenlammi/Arrmada/releases/tag/v1.3.0",
      notes: "## What's Changed\n* Faster imports by @someone", notes_truncated: false, published_at: "2026-10-01T10:00:00Z",
    },
    available: false, behind: 3,
  },
};

// Settings → People & access: the same two, with when they were last around and their
// audiobook access. Grandad is signed in on two browsers and listens in an app.
const noLimit = { movies: -1, seasons: -1, books: -1 };
export const people: { people: Person[] } = {
  people: [
    {
      id: 1, username: "owner@example.com", role: "admin", disabled: false, created_at: "2026-01-01 10:00:00",
      auto_approve_movie: false, auto_approve_series: false, auto_approve_book: false, plex_linked: false, quota: noLimit,
      sessions: 1, web_last: NOW - 60_000, audio_last: 0, listen_last: 0, last_seen: NOW - 60_000,
      audio_eligible: true, audio_allowed: true, audio_password_set: false, audio_devices: 0,
    },
    {
      id: 5, username: "Grandad", role: "requester", disabled: false, created_at: "2026-02-01 10:00:00",
      auto_approve_movie: true, auto_approve_series: true, auto_approve_book: true, plex_linked: true, plex_username: "grandadplex", quota: noLimit,
      sessions: 2, web_last: NOW - 3 * 86_400_000, audio_last: NOW - 7_200_000, listen_last: NOW - 3_600_000, last_seen: NOW - 3_600_000,
      audio_eligible: true, audio_allowed: true, audio_password_set: true, audio_devices: 1,
    },
  ],
};

// GET /me/quota: a requester may ask for three movies a week and has asked for one;
// seasons and books are unlimited. Staff are never limited.
export function quota(p: PersonaInfo): MyQuota {
  const none = { limit: 0, used: 0 };
  if (p.user.role !== "requester") return { days: 7, movie: none, season: none, book: none };
  return { days: 7, movie: { limit: 3, used: 1, resets_at: "2026-10-14T09:00:00Z" }, season: none, book: none };
}
