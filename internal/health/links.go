package health

// Every place a health problem links to. The keys are the web UI's LINKS names
// (web/src/lib/links.ts), which the UI resolves first; the paths are the same addresses
// for any other client. When a page moves, repoint it here and in links.ts together.
var (
	FixIndexers        = &Fix{Key: "indexers", Path: "/settings/connections#search", Label: "Add an indexer"}
	FixIndexerStatus   = &Fix{Key: "indexers", Path: "/settings/connections#search", Label: "Check indexers"}
	FixDownloadClients = &Fix{Key: "downloadClients", Path: "/settings/connections#download-clients", Label: "Check download clients"}
	FixLibraryFolders  = &Fix{Key: "libraryFolders", Path: "/settings/library#media-folders", Label: "Choose folders"}
	FixDiskGuard       = &Fix{Key: "diskGuard", Path: "/settings/downloads#disk-guard", Label: "Disk guard settings"}
	FixRecycleBin      = &Fix{Key: "recycleBin", Path: "/settings/downloads#recycle-bin", Label: "Recycle bin settings"}
	FixAPIKeys         = &Fix{Key: "apiKeys", Path: "/settings/connections#api-keys", Label: "API keys"}
	FixAudiobookServer = &Fix{Key: "audiobookServer", Path: "/audiobooks", Label: "Audiobook server"}
	FixPlexConnection  = &Fix{Key: "plexConnection", Path: "/settings/plex#plex-connection", Label: "Plex connection"}
	FixTasks           = &Fix{Key: "tasks", Path: "/settings/status#tasks", Label: "Tasks"}
	FixBackups         = &Fix{Key: "backups", Path: "/settings/system#backups", Label: "Backups"}
	FixStatus          = &Fix{Key: "status", Path: "/settings/status", Label: "Status"}

	// Where the Needs-you feed (internal/attention) sends each kind of item. Pending
	// requests open the Requests page on what's waiting for approval.
	FixRequests           = &Fix{Key: "requestsWaiting", Path: "/requests?tab=needs", Label: "Requests"}
	FixReview             = &Fix{Key: "review", Path: "/review", Label: "Review"}
	FixDownloadProblems   = &Fix{Key: "downloadProblems", Path: "/downloads?show=problems", Label: "Downloads"}
	FixDownloadsSearching = &Fix{Key: "downloadsSearching", Path: "/downloads?tab=searching", Label: "What's searching"}
)

// connectionsPath is Settings → Connections, where every integration has a card.
const connectionsPath = "/settings/connections"

// ConnectionFix links to one integration's card on Settings → Connections ("indexer-12",
// "download_client-1", "flaresolverr"). It carries no LINKS key: the card's anchor is the
// point, and a key would resolve to the page without it.
func ConnectionFix(card, label string) *Fix {
	return &Fix{Path: connectionsPath + "#" + card, Label: label}
}
