package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libfolders"
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
		out.Folders = append(out.Folders, folderView{Folder: f, Titles: counts[f.ID]})
	}
	return out
}

// handleLibraryFolders lists both libraries' folders: GET /api/v1/library/folders.
func (a *api) handleLibraryFolders(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"movie":  a.foldersOf(r.Context(), libfolders.MediaMovie),
		"series": a.foldersOf(r.Context(), libfolders.MediaSeries),
	})
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
		Kind: "library.move", Target: jobTarget(media, id), Class: jobs.ClassImport,
		// A copy onto another drive runs at disk speed: a 4K remux or a long show can take
		// hours, and stopping half-way leaves the originals in place anyway.
		Timeout: 12 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			res, err := mover.Move(ctx, media, id, to, p)
			if err != nil {
				return res, err
			}
			if p != nil {
				p.SetMessage(title + " moved to " + res.Folder)
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

// publishMoved tells open film pages the film changed (its paths and folder did). The
// page that asked follows the job; a show's page reloads when the job ends.
func (a *api) publishMoved(media string, id int64) {
	if a.deps.Bus == nil || media != libfolders.MediaMovie {
		return
	}
	a.deps.Bus.Publish("movie.refreshed", map[string]any{"id": id})
}
