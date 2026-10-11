package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Unmanaged completed torrents: finished downloads nothing in Arrmada grabbed or imported,
// outside its categories (added by hand in the client, or left from another tool). They
// sit in Seeding as "Not managed"; Needs you lists them so each can be imported into a
// library title the user picks, or ignored for good.

// ignoredHashesKey is the settings key holding the ignored torrents: a JSON list of
// lowercased info hashes, pruned to what is still in the client whenever it's read.
const ignoredHashesKey = "downloads_ignored_hashes"

// unmanagedRow is one unmanaged torrent on Needs you.
type unmanagedRow struct {
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Category  string `json:"category,omitempty"`
	AddedOn   int64  `json:"added_on,omitempty"`
	// Kind is a guess at what it is (its category, else whether the name reads as TV),
	// so the picker opens on the right kind; the user can change it.
	Kind string `json:"kind"`
	// Title is the name parsed into a title, to start the picker's search with.
	Title string `json:"title,omitempty"`
}

// unmanagedState is one read of the queue against the record: the unmanaged torrents, and
// how many more were ignored.
type unmanagedState struct {
	items   []download.Item
	ignored int
}

// readUnmanaged finds the unmanaged torrents now. It fails rather than guess when the
// client or the record can't be read: offering to import one of Arrmada's own downloads
// as a stranger's would file it twice.
func (a *api) readUnmanaged(ctx context.Context) (unmanagedState, error) {
	if a.deps.Automation == nil || a.deps.Downloads == nil {
		return unmanagedState{}, nil
	}
	snap, err := a.deps.Downloads.Snapshot(ctx)
	if err != nil {
		return unmanagedState{}, err
	}
	grabbed, err := a.deps.Automation.GrabbedHashes(ctx)
	if err != nil {
		return unmanagedState{}, err
	}
	imported := map[string]bool{}
	if a.deps.Library != nil {
		if imported, err = a.deps.Library.ImportedHashes(ctx); err != nil {
			return unmanagedState{}, err
		}
	}
	ignored := a.ignoredHashes(ctx, snap)
	cats := download.FixedCategories(a.deps.Config.DownloadCategory)
	own := map[string]bool{cats.Movies: true, cats.TV: true, cats.Books: true, cats.Music: true}
	misfiled := map[string]bool{}
	for _, it := range a.deps.Automation.WrongCategoryDownloads() {
		misfiled[strings.ToLower(it.Hash)] = true
	}
	var st unmanagedState
	for _, it := range snap.Items {
		h := strings.ToLower(it.Hash)
		// Its own categories are the import sweeps' to handle, and a misfiled TV download
		// has Needs you's "Move to TV" instead.
		if h == "" || !it.Complete() || grabbed[h] || imported[it.Hash] || imported[h] || own[it.Category] || misfiled[h] {
			continue
		}
		if ignored[h] {
			st.ignored++
			continue
		}
		st.items = append(st.items, it)
	}
	sort.Slice(st.items, func(i, j int) bool { return st.items[i].Name < st.items[j].Name })
	return st, nil
}

// ignoredHashes reads the ignore list, dropping hashes no longer in the client when the
// read covered every client (a client that didn't answer may still hold its torrents).
func (a *api) ignoredHashes(ctx context.Context, snap download.Snapshot) map[string]bool {
	if a.deps.Settings == nil {
		return map[string]bool{}
	}
	a.transfers.ignoreMu.Lock()
	defer a.transfers.ignoreMu.Unlock()
	list := a.readIgnored(ctx)
	out := make(map[string]bool, len(list))
	if !snap.Complete {
		for _, h := range list {
			out[h] = true
		}
		return out
	}
	present := make(map[string]bool, len(snap.Items))
	for _, it := range snap.Items {
		present[strings.ToLower(it.Hash)] = true
	}
	var kept []string
	for _, h := range list {
		if present[h] {
			kept = append(kept, h)
			out[h] = true
		}
	}
	if len(kept) != len(list) {
		a.writeIgnored(ctx, kept)
	}
	return out
}

// readIgnored is the stored list; the caller holds ignoreMu. Unreadable is empty.
func (a *api) readIgnored(ctx context.Context) []string {
	var list []string
	if raw := a.deps.Settings.Get(ctx, ignoredHashesKey, ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			a.deps.Log.Warn("ignored downloads list unreadable — starting it again", "err", err)
			return nil
		}
	}
	return list
}

// writeIgnored stores the list; the caller holds ignoreMu.
func (a *api) writeIgnored(ctx context.Context, list []string) error {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return a.deps.Settings.Set(ctx, ignoredHashesKey, string(b))
}

// guessKind is what an unmanaged torrent probably is, for the picker's first kind.
func guessKind(it download.Item, p parser.Release) string {
	switch it.Category {
	case download.CategoryBooks:
		return "book"
	case download.CategoryMusic:
		return "music"
	}
	if p.IsTV() {
		return "series"
	}
	return "movie"
}

// importKinds are the library kinds a download can be imported into.
var importKinds = map[string]bool{"movie": true, "series": true, "book": true, "music": true}

// handleUnmanaged lists the unmanaged completed torrents, not ignored ones.
//
//	GET /api/v1/downloads/unmanaged
func (a *api) handleUnmanaged(w http.ResponseWriter, r *http.Request) {
	st, err := a.readUnmanaged(r.Context())
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "couldn't read the download client just now")
		return
	}
	rows := make([]unmanagedRow, 0, len(st.items))
	for _, it := range st.items {
		p := parser.Parse(it.Name)
		rows = append(rows, unmanagedRow{Hash: it.Hash, Name: it.Name, SizeBytes: it.SizeBytes, Category: it.Category,
			AddedOn: it.AddedOn, Kind: guessKind(it, p), Title: p.Title})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"unmanaged": rows, "ignored": st.ignored})
}

// unmanagedItem is the unmanaged torrent {hash} names, or an answer saying why it isn't one.
func (a *api) unmanagedItem(w http.ResponseWriter, r *http.Request) (download.Item, bool) {
	hash, ok := a.torrentHash(w, r)
	if !ok {
		return download.Item{}, false
	}
	st, err := a.readUnmanaged(r.Context())
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "couldn't read the download client just now")
		return download.Item{}, false
	}
	for _, it := range st.items {
		if strings.EqualFold(it.Hash, hash) {
			return it, true
		}
	}
	a.writeError(w, http.StatusNotFound, "that isn't a finished download Arrmada doesn't manage (any more)")
	return download.Item{}, false
}

// handleIgnoreDownload hides an unmanaged torrent from Needs you for as long as it stays
// in the client. It stays in the queue, untouched.
//
//	POST /api/v1/queue/{hash}/ignore
func (a *api) handleIgnoreDownload(w http.ResponseWriter, r *http.Request) {
	it, ok := a.unmanagedItem(w, r)
	if !ok {
		return
	}
	if a.deps.Settings == nil {
		a.writeError(w, http.StatusServiceUnavailable, "settings aren't available")
		return
	}
	h := strings.ToLower(it.Hash)
	a.transfers.ignoreMu.Lock()
	list := a.readIgnored(r.Context())
	seen := false
	for _, x := range list {
		seen = seen || x == h
	}
	var err error
	if !seen {
		err = a.writeIgnored(r.Context(), append(list, h))
	}
	a.transfers.ignoreMu.Unlock()
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "couldn't save that")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "ignored"})
}

// handleUnmanagedTargets lists the library items of ?kind= an unmanaged torrent could be
// imported into, filtered by ?q=.
//
//	GET /api/v1/queue/{hash}/targets?kind=movie&q=
func (a *api) handleUnmanagedTargets(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.torrentHash(w, r); !ok {
		return
	}
	if !importKinds[r.URL.Query().Get("kind")] {
		a.writeError(w, http.StatusBadRequest, "kind must be movie, series, book or music")
		return
	}
	list, truncated, err := a.deps.Automation.ImportTargets(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("q"), 0)
	if err != nil {
		a.writeReviewError(w, err, "could not list library items")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"targets": list, "truncated": truncated})
}

// handleImportUnmanaged imports an unmanaged torrent into the library item the user
// picked. Its files must lie inside the download or library folders — the same rule as
// every manual import — so a torrent saved anywhere else on the host is refused.
//
//	POST /api/v1/queue/{hash}/import  {kind, target_id}
func (a *api) handleImportUnmanaged(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind     string `json:"kind"`
		TargetID int64  `json:"target_id"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if !importKinds[req.Kind] || req.TargetID <= 0 {
		a.writeError(w, http.StatusBadRequest, "pick a movie, show, book or album to import it into")
		return
	}
	it, ok := a.unmanagedItem(w, r)
	if !ok {
		return
	}
	if it.ContentPath == "" {
		a.writeError(w, http.StatusUnprocessableEntity, "the download client doesn't say where this download's files are")
		return
	}
	src, err := a.checkImportPath(r.Context(), it.ContentPath)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "its files are outside your downloads and library folders, so Arrmada won't import them — "+err.Error())
		return
	}
	if err := a.deps.Automation.ImportUnmanaged(r.Context(), it, src, req.Kind, req.TargetID); err != nil {
		switch {
		case errors.Is(err, automation.ErrAlreadyImported):
			a.writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, automation.ErrDownloadGone):
			a.writeError(w, http.StatusGone, "its files aren't on disk any more")
		default:
			a.writeReviewError(w, err, "could not import that download")
		}
		return
	}
	a.queueChanged(strings.ToLower(it.Hash))
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "imported"})
}
