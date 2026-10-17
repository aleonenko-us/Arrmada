package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/arrimport"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// More than one folder for Movies and TV (internal/libfolders). Staff read the folders
// (the add, approve and detail pages show a picker only when a library has two or more);
// only admins add, change, order or remove them, like every other folder setting.

// folderView is one folder as the pages see it.
type folderView struct {
	libfolders.Folder
	Titles int `json:"titles"`
	// DownloadsWarning says when the folder's own downloads folder isn't on its
	// filesystem, so imports from it copy rather than hardlink.
	DownloadsWarning string `json:"downloads_warning,omitempty"`
	// Disk and OnDisk are filled for the extra folders only when asked (?disk=1, Settings →
	// Library): adding a folder doesn't import what's already in it, so the card offers a
	// scan. Disk is "ok" with OnDisk the entries a scan would look at, "empty" (nothing
	// there: a new folder, or a share that isn't mounted — never counted or scanned) or
	// "missing".
	Disk   string `json:"disk,omitempty"`
	OnDisk int    `json:"on_disk,omitempty"`
}

// What a folder looks like on disk (folderView.Disk).
const (
	diskOK      = "ok"
	diskEmpty   = "empty"
	diskMissing = "missing"
)

// folderOnDisk says whether dir is there and, when it looks mounted, how many entries a
// library scan of it would look at: for Movies every entry but the hidden ones (a film
// can sit loose), for TV the show folders. One listing of the folder.
func folderOnDisk(media, dir string) (string, int) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return diskMissing, 0
	}
	if !library.FolderLooksMounted(dir) {
		return diskEmpty, 0
	}
	entries, err := movies.ScanFolders(dir) // every entry but the bins and hidden folders
	if err != nil {
		return diskMissing, 0
	}
	n := 0
	for _, e := range entries {
		if media == libfolders.MediaSeries && !e.IsDir() {
			continue
		}
		n++
	}
	return diskOK, n
}

// withDisk fills Disk and OnDisk on a library's extra folders.
func withDisk(set libraryFolders, media string) libraryFolders {
	for i, f := range set.Folders {
		if !f.Default {
			set.Folders[i].Disk, set.Folders[i].OnDisk = folderOnDisk(media, f.Path)
		}
	}
	return set
}

// scanScope is what one library scan covers: Root "" for every folder of the library,
// else the one folder asked for (ID, Label).
type scanScope struct {
	Root  string
	ID    int64
	Label string
}

// target is the scan job's target: "all", or the folder's id — never its path, which
// job.updated would carry to every staff socket.
func (s scanScope) target() string {
	if s.Root == "" {
		return "all"
	}
	return "folder:" + strconv.FormatInt(s.ID, 10)
}

// folders is the library folders the scan reads: all of them, or the one.
func (s scanScope) folders(all []string) []string {
	if s.Root == "" {
		return all
	}
	return []string{s.Root}
}

// running is the 409's wording when this scan is already running.
func (s scanScope) running(all string) string {
	if s.Root == "" {
		return all
	}
	return "a scan of " + s.Label + " is already running"
}

// scanScope resolves a scan's optional root (POST /movies/scan or /series/scan {root}) to
// one of the library's folders. A root that isn't one is refused, and so is a folder that
// is missing or looks unmounted: a scan of an empty mount point finds nothing and would
// settle the folder's needs-review rows as if it had.
func (a *api) scanScope(w http.ResponseWriter, r *http.Request, media, root string) (scanScope, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return scanScope{}, true
	}
	noun := "Movies"
	if media == libfolders.MediaSeries {
		noun = "TV"
	}
	for _, f := range a.foldersOf(r.Context(), media).Folders {
		if f.Path == "" || filepath.Clean(f.Path) != filepath.Clean(root) {
			continue
		}
		switch state, _ := folderOnDisk(media, f.Path); state {
		case diskMissing:
			a.writeError(w, http.StatusConflict, f.Path+" can't be found — is its drive or share mounted? Nothing was scanned.")
			return scanScope{}, false
		case diskEmpty:
			a.writeError(w, http.StatusConflict, f.Path+" is empty (or its drive or share isn't mounted), so there's nothing to scan.")
			return scanScope{}, false
		}
		return scanScope{Root: f.Path, ID: f.ID, Label: f.Label}, true
	}
	a.writeError(w, http.StatusBadRequest, "that isn't one of the "+noun+" folders — reload and try again")
	return scanScope{}, false
}

// libraryFolders is one library's folders.
type libraryFolders struct {
	Multi   bool         `json:"multi"`
	Folders []folderView `json:"folders"`
}

// foldersOf lists a library's folders with how many titles each holds.
func (a *api) foldersOf(ctx context.Context, media string) libraryFolders {
	if a.deps.Folders == nil {
		def := libfolders.Folder{ID: libfolders.DefaultID, Media: media, Default: true, Label: "Movies"}
		if media == libfolders.MediaSeries {
			def.Path, def.Label = a.roots().TV(ctx), "TV"
		} else {
			def.Path = a.roots().Movies(ctx)
		}
		return libraryFolders{Folders: []folderView{{Folder: def}}}
	}
	counts, err := a.deps.Folders.Counts(ctx, media)
	if err != nil {
		a.deps.Log.Warn("library folders: couldn't count titles per folder", "media", media, "err", err)
	}
	out := libraryFolders{Multi: a.deps.Folders.Multi(ctx, media), Folders: []folderView{}}
	for _, f := range a.deps.Folders.List(ctx, media) {
		out.Folders = append(out.Folders, folderView{Folder: f, Titles: counts[f.ID], DownloadsWarning: libfolders.DownloadsWarning(f.Downloads, f.Path)})
	}
	return out
}

// handleSetFolderDownloads gives a Movies or TV folder (id 0: the default one) a
// downloads folder of its own, or clears it with "": PUT
// /api/v1/system/library/folders/downloads {media, id, path}. Torrent grabs for the
// folder's titles save there from the next grab on. A downloads folder on another
// filesystem from its library is saved with a warning, not refused.
func (a *api) handleSetFolderDownloads(w http.ResponseWriter, r *http.Request) {
	if !a.foldersReady(w) {
		return
	}
	var in struct {
		Media string `json:"media"`
		ID    int64  `json:"id"`
		Path  string `json:"path"`
	}
	if !a.decodeJSON(w, r, &in) {
		return
	}
	res, err := a.deps.Folders.SetDownloads(r.Context(), in.Media, in.ID, in.Path)
	if err != nil {
		a.writeFolderErr(w, err)
		return
	}
	a.deps.Log.Info("library folders: downloads folder set", "media", in.Media, "folder", res.Folder.Path, "downloads", res.Folder.Downloads)
	a.recheckHealth(r, "library", "library.downloads", "disk.free")
	a.writeJSON(w, http.StatusOK, res)
}

// handleLibraryFolders lists both libraries' folders: GET /api/v1/library/folders.
// ?disk=1 (Settings → Library only, not every add dialog) also looks at each extra folder
// on disk.
func (a *api) handleLibraryFolders(w http.ResponseWriter, r *http.Request) {
	movie, tv := a.foldersOf(r.Context(), libfolders.MediaMovie), a.foldersOf(r.Context(), libfolders.MediaSeries)
	if r.URL.Query().Get("disk") == "1" {
		movie, tv = withDisk(movie, libfolders.MediaMovie), withDisk(tv, libfolders.MediaSeries)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"movie": movie, "series": tv})
}

// handleSuggestFolder is the folder a title would go to by the rules, for the add and
// approve dialogs to preselect: GET /api/v1/library/folders/suggest?media=&tmdb_id=. With
// one folder it answers the default without looking anything up.
func (a *api) handleSuggestFolder(w http.ResponseWriter, r *http.Request) {
	media := r.URL.Query().Get("media")
	tmdbID, _ := strconv.Atoi(r.URL.Query().Get("tmdb_id"))
	if !libfolders.ValidMedia(media) || tmdbID <= 0 {
		a.writeError(w, http.StatusBadRequest, "media (movie or series) and tmdb_id are required")
		return
	}
	if a.deps.Folders == nil || !a.deps.Folders.Multi(r.Context(), media) {
		a.writeJSON(w, http.StatusOK, map[string]any{"folder_id": libfolders.DefaultID})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var (
		id  int64
		err error
	)
	if media == libfolders.MediaSeries {
		id, err = a.deps.Series.SuggestFolder(ctx, tmdbID)
	} else {
		id, err = a.deps.Movies.SuggestFolder(ctx, tmdbID)
	}
	if err != nil {
		// Metadata being slow or down isn't worth an error on an add dialog: the default
		// is preselected and the person picks.
		a.deps.Log.Debug("library folders: couldn't suggest a folder", "media", media, "tmdb_id", tmdbID, "err", err)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"folder_id": id})
}

// foldersReady answers 503 when extra folders aren't wired (tests, tools).
func (a *api) foldersReady(w http.ResponseWriter) bool {
	if a.deps.Folders == nil {
		a.writeError(w, http.StatusServiceUnavailable, "library folders aren't available")
		return false
	}
	return true
}

// writeFolderErr maps a libfolders error to a status and a plain message.
func (a *api) writeFolderErr(w http.ResponseWriter, err error) {
	var use *libfolders.UseError
	switch {
	case errors.As(err, &use):
		a.writeError(w, http.StatusConflict, use.Error())
	case errors.Is(err, libfolders.ErrMoving):
		a.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, libfolders.ErrNotFound):
		a.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, libfolders.ErrBadMedia), errors.Is(err, libfolders.ErrBadRule),
		errors.Is(err, libfolders.ErrBadPath), errors.Is(err, libfolders.ErrDuplicates):
		a.writeError(w, http.StatusBadRequest, plainFolderError(err))
	default:
		a.deps.Log.Warn("library folders: couldn't save", "err", err)
		a.writeError(w, http.StatusInternalServerError, "couldn't save the library folders")
	}
}

// plainFolderError drops the sentinel's own wording from a wrapped error ("invalid folder:
// /x doesn't exist" → "/x doesn't exist").
func plainFolderError(err error) string {
	msg := err.Error()
	for _, s := range []error{libfolders.ErrBadPath, libfolders.ErrDuplicates} {
		if p := s.Error() + ": "; strings.HasPrefix(msg, p) {
			msg = strings.TrimPrefix(msg, p)
		}
	}
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return msg
}

// handleAddLibraryFolder adds an extra folder: POST /api/v1/system/library/folders.
func (a *api) handleAddLibraryFolder(w http.ResponseWriter, r *http.Request) {
	if !a.foldersReady(w) {
		return
	}
	var in libfolders.AddInput
	if !a.decodeJSON(w, r, &in) {
		return
	}
	f, err := a.deps.Folders.Add(r.Context(), in)
	if err != nil {
		a.writeFolderErr(w, err)
		return
	}
	a.deps.Log.Info("library folders: added", "media", f.Media, "folder", f.Path, "rule", f.Rule)
	a.recheckHealth(r, "library", "recycle.drive")
	a.writeJSON(w, http.StatusCreated, f)
}

// handleUpdateLibraryFolder renames an extra folder or changes its rule:
// PUT /api/v1/system/library/folders/{id}.
func (a *api) handleUpdateLibraryFolder(w http.ResponseWriter, r *http.Request) {
	if !a.foldersReady(w) {
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var in libfolders.UpdateInput
	if !a.decodeJSON(w, r, &in) {
		return
	}
	f, err := a.deps.Folders.Update(r.Context(), id, in)
	if err != nil {
		a.writeFolderErr(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, f)
}

// handleOrderLibraryFolders sets the order rules are tried in:
// PUT /api/v1/system/library/folders/order {media, ids}.
func (a *api) handleOrderLibraryFolders(w http.ResponseWriter, r *http.Request) {
	if !a.foldersReady(w) {
		return
	}
	var in struct {
		Media string  `json:"media"`
		IDs   []int64 `json:"ids"`
	}
	if !a.decodeJSON(w, r, &in) {
		return
	}
	if err := a.deps.Folders.Reorder(r.Context(), in.Media, in.IDs); err != nil {
		if errors.Is(err, libfolders.ErrNotFound) {
			a.writeError(w, http.StatusBadRequest, "the order must list each of the library's extra folders once — reload and try again")
			return
		}
		a.writeFolderErr(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, a.foldersOf(r.Context(), in.Media))
}

// handleRemoveLibraryFolder removes an extra folder from Arrmada (never from disk):
// DELETE /api/v1/system/library/folders/{id}. Refused (409) while titles live in it.
func (a *api) handleRemoveLibraryFolder(w http.ResponseWriter, r *http.Request) {
	if !a.foldersReady(w) {
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	f, found := a.deps.Folders.ByID(r.Context(), id)
	if err := a.deps.Folders.Remove(r.Context(), id); err != nil {
		a.writeFolderErr(w, err)
		return
	}
	if found {
		a.deps.Log.Info("library folders: removed", "media", f.Media, "folder", f.Path)
	}
	a.recheckHealth(r, "library", "recycle.drive")
	w.WriteHeader(http.StatusNoContent)
}

// handleMoveMovieFolder moves a film's files to another Movies folder (a job):
// POST /api/v1/movies/{id}/move {folder_id}.
func (a *api) handleMoveMovieFolder(w http.ResponseWriter, r *http.Request) {
	a.moveToFolder(w, r, libfolders.MediaMovie)
}

// handleMoveSeriesFolder moves a show's files to another TV folder (a job):
// POST /api/v1/series/{id}/move {folder_id}.
func (a *api) handleMoveSeriesFolder(w http.ResponseWriter, r *http.Request) {
	a.moveToFolder(w, r, libfolders.MediaSeries)
}

func (a *api) moveToFolder(w http.ResponseWriter, r *http.Request, media string) {
	if a.deps.FolderMover == nil || a.deps.Folders == nil {
		a.writeError(w, http.StatusServiceUnavailable, "moving between folders isn't available")
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		FolderID *int64 `json:"folder_id"`
	}
	if !a.decodeJSON(w, r, &in) {
		return
	}
	if in.FolderID == nil {
		a.writeError(w, http.StatusBadRequest, "folder_id is required")
		return
	}
	ctx := r.Context()
	dest, ok := a.deps.Folders.Get(ctx, media, *in.FolderID)
	if !ok {
		a.writeError(w, http.StatusBadRequest, "that folder doesn't exist — reload and pick again")
		return
	}
	// The title must exist (a 404 now beats a failed job later).
	var title string
	switch media {
	case libfolders.MediaSeries:
		s, err := a.deps.Series.Get(ctx, id)
		if errors.Is(err, series.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "series not found")
			return
		} else if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not read the series")
			return
		}
		title = s.Title
	default:
		m, err := a.deps.Movies.Get(ctx, id)
		if errors.Is(err, movies.ErrNotFound) {
			a.writeError(w, http.StatusNotFound, "movie not found")
			return
		} else if err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not read the movie")
			return
		}
		title = m.Title
	}
	to := dest.ID
	mover := a.deps.FolderMover
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{
		Kind: "library.move", Target: jobTarget(media, id), Class: jobs.ClassLibraryMove,
		// A copy onto another drive runs at disk speed: a 4K remux or a long show can take
		// hours, and stopping half-way leaves the originals in place anyway.
		Timeout: 12 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			res, err := mover.Move(ctx, media, id, to, p)
			if err != nil {
				return res, err
			}
			if p != nil {
				p.SetMessage(strings.TrimSpace(title + " moved to " + res.Folder + ". " + res.Warning))
			}
			a.publishMoved(media, id)
			return res, nil
		}})
	if !ok {
		return
	}
	if existing {
		a.alreadyRunning(w, jobID, "a move of this title is already running")
		return
	}
	a.accepted(w, jobID, false, map[string]any{"status": "moving", "folder": dest.Label})
}

// handleArrRoots reads a Radarr's or Sonarr's root folders and pairs each with the
// Arrmada folder it already is (or where it looks to be here), for Settings → Import &
// migrate: POST /api/v1/import/arr/roots {app, url, api_key}. It only reads; the key isn't
// kept. Adding the folders and scanning them are the page's next steps, through the
// ordinary folder and scan routes.
func (a *api) handleArrRoots(w http.ResponseWriter, r *http.Request) {
	var in struct {
		App    string `json:"app"`
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if !a.decodeJSON(w, r, &in) {
		return
	}
	c, err := arrimport.New(in.App, in.URL, in.APIKey)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	roots, err := c.Roots(ctx)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	media := c.Media()
	var folders []arrimport.Folder
	for _, f := range a.foldersOf(ctx, media).Folders {
		folders = append(folders, arrimport.Folder{ID: f.ID, Path: f.Path})
	}
	dataDir := a.deps.Config.DataDir
	exists := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir() && !libroots.InDataDir(p, dataDir)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"media": media, "app": strings.ToLower(in.App), "roots": arrimport.Map(roots, folders, exists)})
}

// publishMoved tells open film pages the film changed (its paths and folder did). The
// page that asked follows the job; a show's page reloads when the job ends.
func (a *api) publishMoved(media string, id int64) {
	if a.deps.Bus == nil || media != libfolders.MediaMovie {
		return
	}
	a.deps.Bus.Publish("movie.refreshed", map[string]any{"id": id})
}
