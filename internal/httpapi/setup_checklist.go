package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Getting started: the steps between a fresh install and the first imported download, each
// ticked off by looking at what's actually configured rather than by anyone clicking "done".
// The Dashboard shows it as a card until everything is ticked or the admin dismisses it, and
// the setup wizard's last step shows the same list.

const keyGettingStartedDismissed = "getting_started_dismissed"

// checklistProbeTimeout bounds the one check that talks to something else (the download
// client), so a hung client can't hold the Dashboard card open.
const checklistProbeTimeout = 3 * time.Second

// checklistItem is one step. Link and LinkKey say where to do it, the same way a health
// warning's Fix does: LinkKey names the web UI's LINKS entry, Link is the path for anyone else.
type checklistItem struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Done    bool   `json:"done"`
	Link    string `json:"link"`
	LinkKey string `json:"link_key,omitempty"`
	// Action, when set, is something the step does itself from the checklist rather than a
	// page to open: "import_library" starts the library scans and shows their progress.
	Action string `json:"action,omitempty"`
	// Detail is a line under the step ("84 folders on disk, 3 titles in Arrmada").
	Detail string `json:"detail,omitempty"`
}

type setupChecklist struct {
	Items     []checklistItem `json:"items"`
	Done      bool            `json:"done"` // every item ticked
	Dismissed bool            `json:"dismissed"`
}

func fixItem(key, label string, done bool, fix *health.Fix) checklistItem {
	return checklistItem{Key: key, Label: label, Done: done, Link: fix.Path, LinkKey: fix.Key}
}

// setupChecklist works out each step. A check that can't be answered (a service not wired,
// a failed read) counts as not done: the card then points at the page, which says more.
func (a *api) setupChecklist(ctx context.Context) setupChecklist {
	items := []checklistItem{
		fixItem("tmdb", "Add your TMDB key, for titles, artwork and Discover", a.deps.APIKeys != nil && a.deps.APIKeys.Value(ctx, "tmdb") != "", health.FixAPIKeys),
		fixItem("folders", "Choose your library folders", a.librariesChosen(ctx), health.FixLibraryFolders),
		fixItem("download_client", "Connect a download client", a.downloadClientAnswers(ctx), health.FixDownloadClients),
		fixItem("indexer", "Add an indexer, or sync them from Prowlarr", a.enabledIndexers(ctx) > 0, health.FixIndexers),
		// Plex, Jellyfin or both: either one ticks it.
		fixItem("media_server", "Connect your media server (Plex or Jellyfin)", a.plexConnected(ctx) || a.jellyfinConfigured(ctx), health.FixMediaServerCards),
		{Key: "first_title", Label: "Add your first movie or show", Done: a.hasTitles(ctx), Link: "/discover"},
		{Key: "first_import", Label: "Get your first download into the library", Done: a.anyImported(ctx), Link: "/activity/history", LinkKey: "history"},
	}
	// A library that's already on disk: scanning it in is the step that matters most to
	// someone moving over from another setup, and it comes before adding titles by hand.
	if it, ok := a.importLibraryItem(ctx); ok {
		for i, x := range items {
			if x.Key == "first_title" {
				items = append(items[:i], append([]checklistItem{it}, items[i:]...)...)
				break
			}
		}
	}
	// The bundled SABnzbd comes without a news server: its step follows the client's.
	if it, ok := a.usenetProviderItem(ctx); ok {
		items = append(items[:3], append([]checklistItem{it}, items[3:]...)...)
	}
	out := setupChecklist{Items: items, Done: true, Dismissed: a.deps.Settings.GetBool(ctx, keyGettingStartedDismissed, false)}
	for _, it := range items {
		if !it.Done {
			out.Done = false
		}
	}
	return out
}

// importLibraryMinShare is how much of what's on disk must be in Arrmada for "import the
// library you already have" to count as done: half the folders. A library of 400 shows with
// three titles in Arrmada hasn't been imported; one with the odd folder the scan couldn't
// match has.
const importLibraryMinShare = 2

// importLibraryItem is the step that scans the existing library in. It exists only while the
// library folders hold media (folders with something in them) — a fresh, empty library has
// nothing to import — and is ticked when Arrmada knows about at least half of them.
func (a *api) importLibraryItem(ctx context.Context) (checklistItem, bool) {
	if a.deps.Store == nil || a.deps.Settings == nil {
		return checklistItem{}, false
	}
	onDisk := a.libraryFoldersOnDisk(ctx)
	if onDisk == 0 {
		return checklistItem{}, false
	}
	titles := a.titleCount(ctx)
	done := titles*importLibraryMinShare >= onDisk
	return checklistItem{
		Key: "import_library", Label: "Import the library you already have", Done: done, Action: "import_library",
		Detail: fmt.Sprintf("%s on disk, %s in Arrmada", countNoun(onDisk, "folder"), countNoun(titles, "title")),
	}, true
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// titleCount is the movies and shows Arrmada has.
func (a *api) titleCount(ctx context.Context) int {
	var n int
	err := a.deps.Store.DB().QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM movies) + (SELECT COUNT(*) FROM series)`).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}

// libraryFoldersOnDisk counts the top-level entries of the Movies and TV folders that look
// like titles: a visible sub-folder, or a loose video file. It reads two directory listings,
// so the answer is kept for a minute (the Dashboard asks every 30 seconds).
func (a *api) libraryFoldersOnDisk(ctx context.Context) int {
	roots := a.roots()
	dirs := append(roots.MovieFolders(ctx), roots.TVFolders(ctx)...)
	a.onDiskMu.Lock()
	defer a.onDiskMu.Unlock()
	key := strings.Join(dirs, "\x00")
	if key == a.onDiskKey && time.Since(a.onDiskAt) < time.Minute {
		return a.onDiskN
	}
	n := 0
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if library.SkipScanDir(e.Name()) {
				continue
			}
			if e.IsDir() || library.IsVideoFile(e.Name()) {
				n++
			}
		}
	}
	a.onDiskKey, a.onDiskN, a.onDiskAt = key, n, time.Now()
	return n
}

// downloadClientAnswers: at least one switched-on client answered its last queue read.
func (a *api) downloadClientAnswers(ctx context.Context) bool {
	if a.deps.Downloads == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, checklistProbeTimeout)
	defer cancel()
	snap, err := a.deps.Downloads.Snapshot(ctx)
	if err != nil {
		return false
	}
	for _, h := range snap.Health {
		if h.Enabled && h.Reachable {
			return true
		}
	}
	return false
}

// enabledIndexers counts the indexers searches can use.
func (a *api) enabledIndexers(ctx context.Context) int {
	if a.deps.Indexers == nil {
		return 0
	}
	list, err := a.deps.Indexers.List(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, ix := range list {
		if ix.Enabled {
			n++
		}
	}
	return n
}

// plexConnected: a server address and token are saved (the Dashboard's own test for it).
func (a *api) plexConnected(ctx context.Context) bool {
	if a.deps.Insights == nil {
		return false
	}
	cfg := a.deps.Insights.Config(ctx)
	return cfg.URL != "" && cfg.TokenSet
}

// jellyfinConfigured: a Jellyfin address and API key are saved (Plex's test, for Jellyfin).
func (a *api) jellyfinConfigured(ctx context.Context) bool {
	return a.deps.Jellyfin != nil && a.deps.Jellyfin.Configured(ctx)
}

// hasTitles: any movie or show in the library, added or scanned in.
func (a *api) hasTitles(ctx context.Context) bool {
	if a.deps.Store == nil {
		return false
	}
	var n int
	err := a.deps.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM movies) OR EXISTS(SELECT 1 FROM series)`).Scan(&n)
	return err == nil && n == 1
}

// anyImported: a download has been imported — a movie import that placed a file, or any
// grab marked imported (series and books record theirs there).
func (a *api) anyImported(ctx context.Context) bool {
	if a.deps.Library != nil {
		if recs, err := a.deps.Library.Recent(ctx, 1); err == nil && len(recs) > 0 {
			return true
		}
	}
	if a.deps.Automation != nil {
		if ok, err := a.deps.Automation.AnyImported(ctx); err == nil && ok {
			return true
		}
	}
	return false
}

// handleSetupChecklist — GET /api/v1/setup/checklist (admin). The first time every step
// reads as done, the card is put away for good: a step that later comes undone (Plex
// unplugged for a day) is a health warning's job, not a reason to bring "Getting started"
// back.
func (a *api) handleSetupChecklist(w http.ResponseWriter, r *http.Request) {
	c := a.setupChecklist(r.Context())
	if c.Done && !c.Dismissed {
		if err := a.deps.Settings.SetBool(r.Context(), keyGettingStartedDismissed, true); err == nil {
			c.Dismissed = true
		}
	}
	a.writeJSON(w, http.StatusOK, c)
}

// handleDismissSetupChecklist — POST /api/v1/setup/checklist/dismiss (admin). Hides the
// Dashboard card for good; the wizard's Finish step still shows the list.
func (a *api) handleDismissSetupChecklist(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Settings.SetBool(r.Context(), keyGettingStartedDismissed, true); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save")
		return
	}
	a.writeJSON(w, http.StatusOK, a.setupChecklist(r.Context()))
}
