package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/arrimport"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

// Settings → Import & migrate → Radarr / Sonarr: bring a Radarr's or Sonarr's library over
// (internal/arrimport). The page connects, previews (what would be added, which folders and
// profiles, which files Arrmada can see), then imports in the background. The API key comes
// with each request and is used for it alone — never stored, logged or sent back. Admin
// only, like every other import.

// arrConnection is the app the person points at.
type arrConnection struct {
	App    string `json:"app"`
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// arrImportRequest is a preview or an import: the connection and the person's choices.
// Preview leaves out what it wants suggested (nil path_maps: matched folders' maps).
type arrImportRequest struct {
	arrConnection
	// Profiles maps the app's profile ids to Arrmada profile keys ("" = the default).
	Profiles map[int]string `json:"profiles"`
	// Folders maps the app's root folders to Arrmada folder ids (0 = the default folder).
	Folders       map[string]int64    `json:"folders"`
	PathMaps      []arrimport.PathMap `json:"path_maps"`
	ImportMissing bool                `json:"import_missing"`
	// ImportSetup also brings the app's indexers and download clients over; SkipTitles
	// leaves the library out (setup only).
	ImportSetup bool `json:"import_setup"`
	SkipTitles  bool `json:"skip_titles"`
}

// arrImporter is the import wired to the library.
func (a *api) arrImporter() *arrimport.Importer {
	imp := &arrimport.Importer{Log: a.deps.Log, TMDBForTVDB: a.deps.SeriesByTVDB}
	if a.deps.Indexers != nil {
		imp.Indexers = a.deps.Indexers
	}
	if a.deps.Downloads != nil {
		imp.Clients = a.deps.Downloads
	}
	if a.deps.Movies != nil {
		imp.Movies = a.deps.Movies
	}
	if a.deps.Series != nil {
		imp.Series = a.deps.Series
	}
	return imp
}

// arrReady checks the library the app fills can take titles: its service is wired and has
// metadata (adding a title is a TMDB fetch). The answer is a plain message, "" when ready.
func (a *api) arrReady(media string) string {
	if media == "series" {
		if a.deps.Series == nil {
			return "TV isn't available"
		}
		if !a.deps.Series.MetadataAvailable() {
			return "series metadata isn't configured — add a TMDB key in Settings → Connections (free from themoviedb.org)"
		}
		return ""
	}
	if a.deps.Movies == nil {
		return "Movies isn't available"
	}
	if !a.deps.Movies.MetadataAvailable() {
		return "movie metadata isn't configured — add a TMDB key in Settings → Connections (free from themoviedb.org)"
	}
	return ""
}

// arrTargets is Arrmada's quality profiles for the media, for the profile pickers.
func (a *api) arrTargets(ctx context.Context, media string) (targets []arrimport.Target, def string) {
	targets = []arrimport.Target{}
	if a.deps.Quality == nil {
		return targets, ""
	}
	def = a.deps.Quality.DefaultProfile(ctx, media)
	list, err := a.deps.Quality.ListStored(ctx, media)
	if err != nil {
		a.deps.Log.Warn("arr import: couldn't list quality profiles", "media", media, "err", err)
		return targets, def
	}
	for _, sp := range list {
		key := "custom:" + strconv.FormatInt(sp.ID, 10)
		targets = append(targets, arrimport.Target{Key: key, Name: sp.Name, MaxResolution: arrimport.MaxResolution(sp.AllowedResolutions), Default: key == def})
	}
	return targets, def
}

// arrFolders is the library's folders as the matcher sees them, and a check that a path is
// a folder Arrmada can see (never the data folder).
func (a *api) arrFolders(ctx context.Context, media string) ([]arrimport.Folder, func(string) bool) {
	var folders []arrimport.Folder
	for _, f := range a.foldersOf(ctx, media).Folders {
		folders = append(folders, arrimport.Folder{ID: f.ID, Path: f.Path})
	}
	dataDir := a.deps.Config.DataDir
	exists := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir() && !libroots.InDataDir(p, dataDir)
	}
	return folders, exists
}

// arrOptions turns a request into import options, checking each choice: profiles must be
// Arrmada's, folders the library's, path maps well formed.
func (a *api) arrOptions(ctx context.Context, media string, in arrImportRequest, folders []arrimport.Folder, def string) (arrimport.Options, error) {
	maps, err := arrimport.CleanPathMaps(in.PathMaps)
	if err != nil {
		return arrimport.Options{}, err
	}
	opts := arrimport.Options{Profiles: map[int]string{}, Folders: map[string]int64{}, PathMaps: maps,
		ImportMissing: in.ImportMissing, DefaultProfile: def, ImportSetup: in.ImportSetup, SkipTitles: in.SkipTitles}
	for id, ref := range in.Profiles {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if ref == "n/a" || a.deps.Quality == nil || !a.deps.Quality.Known(ctx, ref) {
			return arrimport.Options{}, fmt.Errorf("that quality profile doesn't exist (%s) — reload and pick again", ref)
		}
		opts.Profiles[id] = ref
	}
	valid := map[int64]bool{}
	for _, f := range folders {
		valid[f.ID] = true
	}
	for root, id := range in.Folders {
		if !valid[id] {
			return arrimport.Options{}, fmt.Errorf("the folder chosen for %s doesn't exist — reload and pick again", root)
		}
		opts.Folders[root] = id
	}
	return opts, nil
}

// handleArrPreview reads a Radarr's or Sonarr's library and says what importing it would
// do: POST /api/v1/import/arr/preview {app, url, api_key, profiles?, folders?, path_maps?,
// import_missing?}. It changes nothing. The answer carries the root folders matched to
// Arrmada's, the suggested path maps (when the request had none), the app's profiles with
// a suggested Arrmada profile each, and the counts.
func (a *api) handleArrPreview(w http.ResponseWriter, r *http.Request) {
	var in arrImportRequest
	if !a.decodeJSON(w, r, &in) {
		return
	}
	c, err := arrimport.New(in.App, in.URL, in.APIKey)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	snap, err := arrimport.Load(ctx, c)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	media := snap.Media()
	folders, exists := a.arrFolders(ctx, media)
	roots := arrimport.Map(snap.Roots, folders, exists)
	if in.PathMaps == nil {
		in.PathMaps = arrimport.SuggestPathMaps(roots, folders)
	}
	targets, def := a.arrTargets(ctx, media)
	opts, err := a.arrOptions(ctx, media, in, folders, def)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	notReady := a.arrReady(media)
	imp := a.arrImporter()
	if opts.SkipTitles {
		notReady = ""
	} else if (media == "series" && imp.Series == nil) || (media == "movie" && imp.Movies == nil) {
		a.writeError(w, http.StatusServiceUnavailable, notReady)
		return
	}
	pv, err := imp.Preview(ctx, snap, opts, targets)
	if err != nil {
		a.deps.Log.Warn("arr import: preview failed", "app", snap.App, "err", err)
		a.writeError(w, http.StatusInternalServerError, "couldn't work out the import: "+err.Error())
		return
	}
	pathMaps := opts.PathMaps
	if pathMaps == nil {
		pathMaps = []arrimport.PathMap{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"preview": pv, "roots": roots, "path_maps": pathMaps, "targets": targets,
		"default_profile": def, "not_ready": notReady,
	})
}

// handleArrImport brings the library over in the background: POST /api/v1/import/arr/run
// with the preview's body and the person's choices. One import runs at a time (two at once
// would race to add the same titles); the page follows the job, whose result is the
// summary. Nothing is searched: wanted titles join the missing sweeps a few per run.
func (a *api) handleArrImport(w http.ResponseWriter, r *http.Request) {
	var in arrImportRequest
	if !a.decodeJSON(w, r, &in) {
		return
	}
	c, err := arrimport.New(in.App, in.URL, in.APIKey)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	media := c.Media()
	if msg := a.arrReady(media); msg != "" && !in.SkipTitles {
		a.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if in.SkipTitles && !in.ImportSetup {
		a.writeError(w, http.StatusBadRequest, "nothing to import: choose the library, the indexers and download clients, or both")
		return
	}
	ctx := r.Context()
	folders, _ := a.arrFolders(ctx, media)
	_, def := a.arrTargets(ctx, media)
	opts, err := a.arrOptions(ctx, media, in, folders, def)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A wrong address or key answers now, not as a failed job.
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	st, err := c.Status(sctx)
	cancel()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	name := st.Name(strings.ToLower(in.App))
	imp := a.arrImporter()
	log := a.deps.Log
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{
		Kind: "import.arr", Target: "arr", Class: jobs.ClassExternalImport,
		// Each new title is a metadata fetch, and a show a few Sonarr reads too: a library of
		// thousands takes a while.
		Timeout: 6 * time.Hour,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			p.Set(0, "Reading "+name+"…")
			snap, err := arrimport.Load(ctx, c)
			if err != nil {
				return nil, err
			}
			res, err := imp.Run(ctx, c, snap, opts, func(done, total int, title string) {
				if total == 0 {
					return
				}
				msg := fmt.Sprintf("%d of %d", done, total)
				if title != "" {
					msg += " · " + title
				}
				p.Set(float64(done)/float64(total), msg)
			})
			log.Info("arr import finished", "app", snap.App, "from", res.Name, "titles", res.Total,
				"imported", res.Imported, "files", res.FilesAttached, "existing", res.Existing,
				"skipped", res.Skipped, "failed", res.Failed, "paced", res.Paced, "err", err)
			p.SetMessage(arrSummary(res))
			// Stopped part-way, the result still says what it did.
			return res, err
		}})
	if !ok {
		return
	}
	if existing {
		a.alreadyRunning(w, jobID, "an import from Radarr or Sonarr is already running — wait for it to finish")
		return
	}
	a.accepted(w, jobID, false, map[string]any{"status": "started", "name": name})
}

// arrSummary is the job's closing line: "Imported 120 films · 95 files recorded · 30 already
// here · 4 skipped · 0 failed · 25 wanted, searched a few at a time".
func arrSummary(res arrimport.Result) string {
	count := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var setup []string
	if res.Setup != nil {
		setup = setupParts(*res.Setup)
	}
	if res.Total == 0 && len(setup) > 0 {
		return strings.Join(setup, " · ")
	}
	parts := []string{"Imported " + count(res.Imported, "film", "films")}
	files := count(res.FilesAttached, "file", "files")
	if res.Media == "series" {
		parts[0] = "Imported " + count(res.Imported, "show", "shows")
		files = count(res.FilesAttached, "episode file", "episode files")
	}
	if res.FilesAttached > 0 {
		parts = append(parts, files+" recorded")
	}
	parts = append(parts, fmt.Sprintf("%d already here", res.Existing), fmt.Sprintf("%d skipped", res.Skipped), fmt.Sprintf("%d failed", res.Failed))
	if res.Paced > 0 {
		parts = append(parts, fmt.Sprintf("%d wanted, searched a few at a time", res.Paced))
	}
	// Titles that were unmonitored over there come over that way: nothing is searched for
	// them, which reads as "the import did nothing" unless the summary says it.
	if res.Unmonitored > 0 {
		from := res.Name
		if from == "" {
			from = "Radarr"
			if res.Media == "series" {
				from = "Sonarr"
			}
		}
		if res.Media == "series" {
			parts = append(parts, fmt.Sprintf("%s added with searching off (as they were in %s) — turn searching on from the Series list", count(res.Unmonitored, "show", "shows"), from))
		} else {
			parts = append(parts, fmt.Sprintf("%s added unmonitored (as they were in %s) — monitor them from the Movies list", count(res.Unmonitored, "film", "films"), from))
		}
	}
	return strings.Join(append(parts, setup...), " · ")
}

// setupParts is the indexer and download client part of the closing line.
func setupParts(s arrimport.SetupResult) []string {
	out := []string{}
	add := func(label string, items []arrimport.SetupItem) {
		a, e, k, f := arrimport.Count(items, arrimport.ActionAdded), arrimport.Count(items, arrimport.ActionExists),
			arrimport.Count(items, arrimport.ActionSkip), arrimport.Count(items, arrimport.ActionFailed)
		out = append(out, fmt.Sprintf("%d %s added (%d already here, %d not brought over, %d failed)", a, label, e, k, f))
	}
	add("indexers", s.Indexers)
	add("download clients", s.Clients)
	return out
}
