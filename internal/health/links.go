package health

// Every place a health problem links to. The keys are the web UI's LINKS names
// (web/src/lib/links.ts), which the UI resolves first; the paths are the same addresses
// for any other client. When a page moves, repoint it here and in links.ts together.
var (
	FixIndexers      = &Fix{Key: "indexers", Path: "/settings/connections#search", Label: "Add an indexer"}
	FixIndexerStatus = &Fix{Key: "indexers", Path: "/settings/connections#search", Label: "Check indexers"}
	// Prowlarr and FlareSolverr each have a card; no LINKS key, so the link keeps its anchor.
	FixProwlarr        = &Fix{Path: "/settings/connections#prowlarr", Label: "Prowlarr sync"}
	FixFlareSolverr    = &Fix{Path: "/settings/connections#flaresolverr", Label: "FlareSolverr setting"}
	FixMusicBrainz     = &Fix{Path: "/settings/connections#musicbrainz", Label: "MusicBrainz settings"}
	FixDownloadClients = &Fix{Key: "downloadClients", Path: "/settings/connections#download-clients", Label: "Check download clients"}
	FixLibraryFolders  = &Fix{Key: "libraryFolders", Path: "/settings/library#media-folders", Label: "Choose folders"}
	// The extra Movies and TV folders, and each folder's own downloads folder.
	FixMoreFolders     = &Fix{Key: "moreFolders", Path: "/settings/library#more-folders", Label: "Library folders"}
	FixDiskGuard       = &Fix{Key: "diskGuard", Path: "/settings/downloads#disk-guard", Label: "Disk guard settings"}
	FixRecycleBin      = &Fix{Key: "recycleBin", Path: "/settings/downloads#recycle-bin", Label: "Recycle bin settings"}
	FixAPIKeys         = &Fix{Key: "apiKeys", Path: "/settings/connections#api-keys", Label: "API keys"}
	FixAudiobookServer = &Fix{Key: "audiobookServer", Path: "/settings/audiobook-server", Label: "Audiobook server"}
	FixPlexConnection  = &Fix{Key: "plexConnection", Path: "/settings/connections#plex", Label: "Plex connection"}
	FixTasks           = &Fix{Key: "tasks", Path: "/settings/status#tasks", Label: "Tasks"}
	FixBackups         = &Fix{Key: "backups", Path: "/settings/system#backups", Label: "Backups"}
	FixStatus          = &Fix{Key: "status", Path: "/settings/status", Label: "Status"}

	// The Jellyfin card on Settings → Connections (a link to it opens its editor while
	// Jellyfin isn't connected).
	FixJellyfinConnection = &Fix{Key: "jellyfinConnection", Path: "/settings/connections#jellyfin", Label: "Jellyfin connection"}
	// Both media server cards on Connections (Plex and Jellyfin side by side), for "connect
	// a media server" when either will do.
	// (How Arrmada uses them is Settings → Media servers, /settings/media-servers: LINKS.mediaServers.)
	FixMediaServerCards = &Fix{Key: "mediaServerCards", Path: "/settings/connections#media-servers", Label: "Media servers"}

	// Where the Needs-you feed (internal/attention) sends each kind of item. Pending
	// requests open the Requests page on what's waiting for approval; stuck downloads open
	// their section of Activity → Needs you (#stalled, #import-failures, #wrong-category),
	// where each has the action that fits it.
	FixRequests           = &Fix{Key: "requestsWaiting", Path: "/requests?tab=needs", Label: "Requests"}
	FixReview             = &Fix{Key: "review", Path: "/activity/needs-you", Label: "Needs you"}
	FixDownloadProblems   = &Fix{Key: "downloadProblems", Path: "/activity/needs-you#stalled", Label: "Needs you"}
	FixImportProblems     = &Fix{Key: "importProblems", Path: "/activity/needs-you#import-failures", Label: "Needs you"}
	FixWrongCategory      = &Fix{Key: "wrongCategory", Path: "/activity/needs-you#wrong-category", Label: "Needs you"}
	FixDownloadsSearching = &Fix{Key: "downloadsSearching", Path: "/activity/wanted", Label: "What's searching"}
	// Convert's Problems tab: files it gave up on, and files waiting for disk space. No LINKS
	// key: the path is followed as it is (LINKS is in the requester's first load, full).
	FixConvertProblems = &Fix{Path: "/convert?tab=problems", Label: "Convert problems"}
	// Problems requesters reported: Requests → Issues. No LINKS key, for the same reason.
	FixIssues = &Fix{Path: "/requests?tab=issues", Label: "Issues"}
	// The Subtitles Overview's needs-attention board. No LINKS key, for the same reason.
	FixSubtitles = &Fix{Path: "/subtitles", Label: "Subtitles"}
)

// connectionsPath is Settings → Connections, where every integration has a card.
const connectionsPath = "/settings/connections"

// ConnectionFix links to one integration's card on Settings → Connections ("indexer-12",
// "download_client-1", "flaresolverr"). It carries no LINKS key: the card's anchor is the
// point, and a key would resolve to the page without it.
func ConnectionFix(card, label string) *Fix {
	return &Fix{Path: connectionsPath + "#" + card, Label: label}
}
