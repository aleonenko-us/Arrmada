package arrimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/safego"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The import itself: a snapshot of the app's library (Load), what bringing it over would do
// (Preview), and doing it (Run). It is idempotent — a title already in Arrmada is never
// added twice, and a file already recorded is never recorded again — so running it twice,
// or once per instance (Sonarr and Sonarr-anime), only adds what's new. It never searches:
// wanted titles it adds are marked so the missing sweeps take them a few at a time.

// MovieLibrary is what the import needs of Arrmada's films (movies.Service).
type MovieLibrary interface {
	LibraryVersionRows(ctx context.Context) (map[int64][]movies.Version, []movies.Movie, error)
	InLibrary(ctx context.Context, path string) bool
	AddImported(ctx context.Context, tmdbID int, o movies.AddOptions, file, source string) (movies.Movie, error)
	AttachImported(ctx context.Context, id int64, file, source string) error
}

// SeriesLibrary is what the import needs of Arrmada's shows (series.Service).
type SeriesLibrary interface {
	List(ctx context.Context) ([]series.Series, error)
	Get(ctx context.Context, id int64) (series.Series, error)
	LibraryEpisodeFiles(ctx context.Context) ([]series.LibraryEpisodeFile, error)
	LibraryFolderOf(ctx context.Context, path string) (int64, bool)
	AddWith(ctx context.Context, tmdbID int, qualityProfile string, o series.AddOptions) (series.Series, error)
	SetType(ctx context.Context, id int64, seriesType string) error
	SetEpisodesMonitored(ctx context.Context, seriesID int64, ids []int64, monitored bool) (int, error)
	ResolveEpisodes(ctx context.Context, seriesID int64, rel parser.Release) []series.EpisodeRef
	AdoptEpisodeFile(ctx context.Context, seriesID int64, season, episode int, path string, size int64) (bool, error)
	SetSearchPaced(ctx context.Context, id int64, on bool) error
	SetMonitored(ctx context.Context, id int64, monitored bool) error
	AddEvent(ctx context.Context, id int64, event, detail string)
}

// Importer brings one Radarr's or Sonarr's library into Arrmada.
type Importer struct {
	Movies MovieLibrary
	Series SeriesLibrary
	// TMDBForTVDB finds a show's TMDB id from its TVDB id, for Sonarr 3 (which has no TMDB
	// ids) and the odd show Sonarr 4 has none for. nil = such shows can't be matched.
	TMDBForTVDB func(ctx context.Context, tvdbID int) (int, error)
	// Indexers and Clients are Arrmada's indexers and download clients, for bringing the
	// app's over (setup.go); nil = that part is left out.
	Indexers IndexerStore
	Clients  ClientStore
	// Stat looks at a file; nil = os.Stat.
	Stat func(path string) (os.FileInfo, error)
	Log  *slog.Logger
}

// Options are the person's choices for one import.
type Options struct {
	// Profiles maps each of the app's profile ids to an Arrmada profile key; a profile left
	// out gets DefaultProfile.
	Profiles       map[int]string `json:"profiles"`
	DefaultProfile string         `json:"-"`
	// Folders maps each of the app's root folders to an Arrmada folder id, for titles whose
	// files aren't (yet) on disk; a title with its file belongs to the folder the file is in.
	Folders map[string]int64 `json:"folders"`
	// PathMaps say where the app's paths are inside Arrmada's container.
	PathMaps []PathMap `json:"path_maps"`
	// ImportMissing also brings over titles whose files Arrmada can't find, without them —
	// they'll be searched for again. Off (the default) skips them, so a wrong path mapping
	// never turns into downloading a library the person already has.
	ImportMissing bool `json:"import_missing"`
	// SkipTitles leaves the films or shows out: only the indexers and download clients (when
	// ImportSetup) come over.
	SkipTitles bool `json:"skip_titles"`
	// ImportSetup also brings the app's indexers and download clients over (setup.go).
	ImportSetup bool `json:"import_setup"`
}

// Snapshot is the app's library as one Load read it.
type Snapshot struct {
	App      string
	Status   Status
	Profiles []Profile
	Roots    []Root
	Movies   []Movie
	Series   []Series
	// Setup is the app's indexers and download clients; nil when they couldn't be read
	// (SetupErr says why), which never stops the titles coming over.
	Setup    *SetupSnapshot
	SetupErr string
}

// Media is the Arrmada library the app fills ("movie" or "series").
func (s *Snapshot) Media() string {
	if s.App == Sonarr {
		return "series"
	}
	return "movie"
}

// Load reads what the import needs from the app: its name, profiles, root folders and
// titles (shows' episodes are read one show at a time during the import).
func Load(ctx context.Context, c *Client) (*Snapshot, error) {
	st, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{App: c.app, Status: st}
	if snap.Profiles, err = c.Profiles(ctx); err != nil {
		return nil, err
	}
	folders, err := c.rootFolders(ctx)
	if err != nil {
		return nil, err
	}
	var roots []string
	if c.app == Sonarr {
		if snap.Series, err = c.AllSeries(ctx); err != nil {
			return nil, err
		}
		for _, s := range snap.Series {
			roots = append(roots, s.Root())
		}
	} else {
		if snap.Movies, err = c.Movies(ctx); err != nil {
			return nil, err
		}
		for _, m := range snap.Movies {
			roots = append(roots, m.Root())
		}
	}
	snap.Roots = countRoots(folders, roots)
	if snap.Setup, err = LoadSetup(ctx, c); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		snap.Setup, snap.SetupErr = nil, err.Error()
	}
	return snap, nil
}

// Counts are what an import would do, by title.
type Counts struct {
	Titles      int `json:"titles"`
	Existing    int `json:"existing"`    // already in Arrmada (left as they are)
	New         int `json:"new"`         // would be added
	Unmatchable int `json:"unmatchable"` // no TMDB id to add them by
	// WithFiles is how many new titles the app has files for, FilesFound how many of those
	// Arrmada can see inside one of its folders (films: the file; shows: the show's folder),
	// and NotFound the rest — skipped unless ImportMissing.
	WithFiles  int `json:"with_files"`
	FilesFound int `json:"files_found"`
	NotFound   int `json:"not_found"`
	// Attachable is how many titles already here, without a file, would get the app's file.
	Attachable int `json:"attachable"`
	// Wanted is how many new titles would be monitored without a file: they're searched a
	// few per sweep, never all at once.
	Wanted int `json:"wanted"`
}

// Sample is one title worth naming in the preview or the summary.
type Sample struct {
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ProfileUse is one of the app's profiles, how many titles use it and the Arrmada profile
// suggested for it.
type ProfileUse struct {
	Profile
	Titles    int    `json:"titles"`
	Suggested string `json:"suggested"`
}

// Preview is what importing the snapshot with the given options would do.
type Preview struct {
	App      string       `json:"app"`
	Media    string       `json:"media"`
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Counts   Counts       `json:"counts"`
	Profiles []ProfileUse `json:"profiles"`
	// NotFound and Unmatched name the first few titles of each, so the person can see what a
	// path mapping misses.
	NotFound  []Sample `json:"not_found"`
	Unmatched []Sample `json:"unmatched"`
	// Setup is what the app's indexers and download clients would become; SetupError says
	// why it's missing when they couldn't be read.
	Setup      *SetupPlan `json:"setup,omitempty"`
	SetupError string     `json:"setup_error,omitempty"`
}

// maxSamples bounds the titles a preview names per list, and maxNotes the notes a result
// keeps (the counts are always complete).
const (
	maxSamples = 25
	maxNotes   = 500
)

func (imp *Importer) stat(p string) (os.FileInfo, error) {
	if imp.Stat != nil {
		return imp.Stat(p)
	}
	return os.Stat(p)
}

func (imp *Importer) log() *slog.Logger {
	if imp.Log != nil {
		return imp.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Preview works out what Run would do with these options, reading only Arrmada's database
// and looking at the files on disk.
func (imp *Importer) Preview(ctx context.Context, snap *Snapshot, opts Options, targets []Target) (Preview, error) {
	pv := Preview{App: snap.App, Media: snap.Media(), Name: snap.Status.Name(snap.App), Version: snap.Status.Version,
		NotFound: []Sample{}, Unmatched: []Sample{}}
	uses := map[int]int{}
	if opts.ImportSetup {
		pv.SetupError = snap.SetupErr
		plan, err := imp.PlanSetup(ctx, snap, snap.Setup)
		if err != nil {
			return pv, err
		}
		pv.Setup = &plan
	}
	if opts.SkipTitles {
		for _, p := range snap.Profiles {
			pv.Profiles = append(pv.Profiles, ProfileUse{Profile: p, Suggested: SuggestProfile(p, targets)})
		}
		return pv, nil
	}
	if snap.App == Sonarr {
		if err := imp.previewSeries(ctx, snap, opts, &pv, uses); err != nil {
			return pv, err
		}
	} else {
		if err := imp.previewMovies(ctx, snap, opts, &pv, uses); err != nil {
			return pv, err
		}
	}
	for _, p := range snap.Profiles {
		pv.Profiles = append(pv.Profiles, ProfileUse{Profile: p, Titles: uses[p.ID], Suggested: SuggestProfile(p, targets)})
	}
	return pv, nil
}

func sample(list *[]Sample, s Sample) {
	if len(*list) < maxSamples {
		*list = append(*list, s)
	}
}

// movieIndex is Arrmada's films by TMDB id, and every path a film's track holds.
type movieIndex struct {
	byTMDB map[int]movies.Movie
	owned  map[string]bool
}

func (imp *Importer) movieIndex(ctx context.Context) (movieIndex, error) {
	tracks, all, err := imp.Movies.LibraryVersionRows(ctx)
	if err != nil {
		return movieIndex{}, fmt.Errorf("couldn't read Arrmada's films: %w", err)
	}
	idx := movieIndex{byTMDB: make(map[int]movies.Movie, len(all)), owned: map[string]bool{}}
	for _, m := range all {
		idx.byTMDB[m.TMDBID] = m
		if m.HasFile && m.MovieFilePath != "" {
			idx.owned[filepath.Clean(m.MovieFilePath)] = true
		}
	}
	for _, vs := range tracks {
		for _, v := range vs {
			if v.HasFile && v.FilePath != "" {
				idx.owned[filepath.Clean(v.FilePath)] = true
			}
		}
	}
	return idx, nil
}

// noFilePath is a title Radarr says has a file without saying where: it's treated like a file
// that can't be found, never as a title to download.
const noFilePath = "Radarr says it has a file but didn't say where"

// movieFile is where a Radarr film's file is inside Arrmada ("" when Radarr has none) and,
// when Arrmada can't take it, why.
func (imp *Importer) movieFile(ctx context.Context, m Movie, opts Options, idx movieIndex) (local, problem string) {
	if !m.HasFile {
		return "", ""
	}
	arr := m.FilePath()
	if arr == "" {
		return "", noFilePath
	}
	local = filepath.FromSlash(Remap(arr, opts.PathMaps))
	fi, err := imp.stat(local)
	switch {
	case err != nil:
		return local, "file not found at " + local
	case fi.IsDir():
		return local, local + " is a folder, not a file"
	case !imp.Movies.InLibrary(ctx, local):
		return local, "the file is at " + local + ", which isn't inside one of Arrmada's Movies folders"
	case idx.owned[filepath.Clean(local)]:
		return local, "that file already belongs to another film in Arrmada"
	}
	return local, ""
}

func (imp *Importer) previewMovies(ctx context.Context, snap *Snapshot, opts Options, pv *Preview, uses map[int]int) error {
	idx, err := imp.movieIndex(ctx)
	if err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, m := range snap.Movies {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &pv.Counts
		c.Titles++
		uses[m.ProfileID]++
		if m.TMDBID <= 0 {
			c.Unmatchable++
			sample(&pv.Unmatched, Sample{Title: m.Title, Year: m.Year, Reason: "Radarr has no TMDB id for it"})
			continue
		}
		local, problem := imp.movieFile(ctx, m, opts, idx)
		if have, ok := idx.byTMDB[m.TMDBID]; ok || seen[m.TMDBID] {
			c.Existing++
			if ok && !have.HasFile && local != "" && problem == "" {
				c.Attachable++
			}
			continue
		}
		seen[m.TMDBID] = true
		c.New++
		if m.HasFile {
			c.WithFiles++
			if problem == "" {
				c.FilesFound++
			} else {
				c.NotFound++
				sample(&pv.NotFound, Sample{Title: m.Title, Year: m.Year, Path: m.FilePath(), Reason: problem})
				if opts.ImportMissing && m.Monitored {
					c.Wanted++
				}
			}
		} else if m.Monitored {
			c.Wanted++
		}
	}
	return nil
}

// seriesIndex is Arrmada's shows by TMDB and TVDB id, and every episode file path held.
type seriesIndex struct {
	byTMDB map[int]int64
	byTVDB map[int]int64
	owned  map[string]bool
	// tvdbTMDB is TMDB's id for the shows Sonarr names only by TVDB id (lookupTVDB).
	tvdbTMDB map[int]int
}

func (imp *Importer) seriesIndex(ctx context.Context) (seriesIndex, error) {
	all, err := imp.Series.List(ctx)
	if err != nil {
		return seriesIndex{}, fmt.Errorf("couldn't read Arrmada's shows: %w", err)
	}
	idx := seriesIndex{byTMDB: map[int]int64{}, byTVDB: map[int]int64{}, owned: map[string]bool{}}
	for _, s := range all {
		idx.byTMDB[s.TMDBID] = s.ID
		if s.TVDBID > 0 {
			idx.byTVDB[s.TVDBID] = s.ID
		}
	}
	files, err := imp.Series.LibraryEpisodeFiles(ctx)
	if err != nil {
		return seriesIndex{}, fmt.Errorf("couldn't read Arrmada's episode files: %w", err)
	}
	for _, f := range files {
		if f.Path != "" {
			idx.owned[filepath.Clean(f.Path)] = true
		}
	}
	return idx, nil
}

// showMatch finds a Sonarr show in Arrmada (existing id > 0) or the TMDB id to add it by
// (0: no match, with why).
func (imp *Importer) showMatch(s Series, idx seriesIndex) (existing int64, tmdb int, why string) {
	if s.TMDBID > 0 {
		if id, ok := idx.byTMDB[s.TMDBID]; ok {
			return id, s.TMDBID, ""
		}
	}
	if s.TVDBID > 0 {
		if id, ok := idx.byTVDB[s.TVDBID]; ok {
			return id, s.TMDBID, ""
		}
	}
	if s.TMDBID > 0 {
		return 0, s.TMDBID, ""
	}
	if s.TVDBID <= 0 {
		return 0, 0, "Sonarr has no TVDB or TMDB id for it"
	}
	if imp.TMDBForTVDB == nil {
		return 0, 0, "Sonarr has no TMDB id for it"
	}
	id := idx.tvdbTMDB[s.TVDBID]
	if id <= 0 {
		return 0, 0, fmt.Sprintf("TMDB has no show for TVDB id %d", s.TVDBID)
	}
	if ex, ok := idx.byTMDB[id]; ok {
		return ex, id, ""
	}
	return 0, id, ""
}

// lookupTVDB finds the TMDB ids of the shows Sonarr names only by TVDB id (Sonarr 3 has no
// TMDB ids at all), a few lookups at a time: one after another, a big Sonarr 3 library
// would outlast the preview.
func (imp *Importer) lookupTVDB(ctx context.Context, shows []Series, idx *seriesIndex) {
	idx.tvdbTMDB = map[int]int{}
	if imp.TMDBForTVDB == nil {
		return
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 6)
	)
	for _, s := range shows {
		if s.TMDBID > 0 || s.TVDBID <= 0 || idx.byTVDB[s.TVDBID] > 0 {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		tvdb := s.TVDBID
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_ = safego.Call(imp.log(), "arr import: TVDB lookup", func() error {
				if id, err := imp.TMDBForTVDB(ctx, tvdb); err == nil && id > 0 {
					mu.Lock()
					idx.tvdbTMDB[tvdb] = id
					mu.Unlock()
				}
				return nil
			})
		}()
	}
	wg.Wait()
}

func (imp *Importer) previewSeries(ctx context.Context, snap *Snapshot, opts Options, pv *Preview, uses map[int]int) error {
	idx, err := imp.seriesIndex(ctx)
	if err != nil {
		return err
	}
	imp.lookupTVDB(ctx, snap.Series, &idx)
	seen := map[int]bool{}
	for _, s := range snap.Series {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &pv.Counts
		c.Titles++
		uses[s.ProfileID]++
		existing, tmdb, why := imp.showMatch(s, idx)
		switch {
		case existing > 0:
			c.Existing++
			continue
		case tmdb <= 0:
			c.Unmatchable++
			sample(&pv.Unmatched, Sample{Title: s.Title, Year: s.Year, Reason: why})
			continue
		case seen[tmdb]:
			c.Existing++
			continue
		}
		seen[tmdb] = true
		c.New++
		if s.Statistics.EpisodeFileCount == 0 {
			if s.Monitored {
				c.Wanted++
			}
			continue
		}
		// The preview looks at the show's folder; the import checks each episode file.
		c.WithFiles++
		dir := filepath.FromSlash(Remap(s.Path, opts.PathMaps))
		fi, err := imp.stat(dir)
		_, inside := imp.Series.LibraryFolderOf(ctx, dir)
		switch {
		case err != nil || !fi.IsDir():
			c.NotFound++
			sample(&pv.NotFound, Sample{Title: s.Title, Year: s.Year, Path: s.Path, Reason: "folder not found at " + dir})
		case !inside:
			c.NotFound++
			sample(&pv.NotFound, Sample{Title: s.Title, Year: s.Year, Path: s.Path, Reason: "the folder is at " + dir + ", which isn't inside one of Arrmada's TV folders"})
		default:
			c.FilesFound++
		}
		if s.Monitored {
			c.Wanted++ // a show with some files usually still wants others
		}
	}
	return nil
}

// Result is what an import did.
type Result struct {
	App           string `json:"app"`
	Media         string `json:"media"`
	Name          string `json:"name"`
	Total         int    `json:"total"`
	Imported      int    `json:"imported"`       // titles added
	FilesAttached int    `json:"files_attached"` // files recorded (films, or episode files)
	Existing      int    `json:"existing"`       // already in Arrmada, left as they were
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	// Paced is how many titles were added monitored without (all) their files: the missing
	// sweeps take them a few per run.
	Paced int `json:"paced"`
	// Unmonitored is how many titles were added unmonitored because they were unmonitored
	// in Radarr or Sonarr (a show with its searching off): nothing is searched for them
	// until they're turned on, so the summary says so.
	Unmonitored int `json:"unmonitored"`
	// Notes say what happened to the titles worth a word: skipped and failed ones, and files
	// that couldn't be matched (the first maxNotes).
	Notes []Note `json:"notes"`
	// More is how many notes were left out past maxNotes.
	More int `json:"more,omitempty"`
	// Setup is what happened to the app's indexers and download clients (nil: not asked for).
	Setup *SetupResult `json:"setup,omitempty"`
}

// Note is one title's outcome, in words.
type Note struct {
	Title   string `json:"title"`
	Year    int    `json:"year,omitempty"`
	Outcome string `json:"outcome"` // skipped | failed | imported | attached
	Reason  string `json:"reason"`
}

func (r *Result) note(title string, year int, outcome, reason string) {
	if len(r.Notes) >= maxNotes {
		r.More++
		return
	}
	r.Notes = append(r.Notes, Note{Title: title, Year: year, Outcome: outcome, Reason: reason})
}

// Progress hears how far the import is: done of total titles, and the one being imported.
type Progress func(done, total int, title string)

// Run imports the snapshot with these options. A title that fails is noted and the rest
// carry on; only a cancelled context (or Arrmada's library being unreadable) stops it, and
// then the result says how far it got.
func (imp *Importer) Run(ctx context.Context, c *Client, snap *Snapshot, opts Options, progress Progress) (Result, error) {
	res := Result{App: snap.App, Media: snap.Media(), Name: snap.Status.Name(snap.App), Notes: []Note{}}
	if progress == nil {
		progress = func(int, int, string) {}
	}
	// Indexers and download clients first: they are a handful of rows, and a person who
	// stops the (long) title import still has them.
	if opts.ImportSetup {
		plan, err := imp.PlanSetup(ctx, snap, snap.Setup)
		if err != nil {
			return res, err
		}
		sr, err := imp.ApplySetup(ctx, plan)
		res.Setup = &sr
		if err != nil {
			return res, err
		}
	}
	if opts.SkipTitles {
		return res, nil
	}
	if snap.App == Sonarr {
		return res, imp.runSeries(ctx, c, snap, opts, &res, progress)
	}
	return res, imp.runMovies(ctx, snap, opts, &res, progress)
}

// guarded runs one title's import, turning a panic into that title's failure.
func (imp *Importer) guarded(title string, fn func() error) error {
	return safego.Call(imp.log(), "arr import: "+title, fn)
}

// availability is Radarr's minimum availability as Arrmada's.
func availability(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "announced", "tba":
		return "announced"
	case "incinemas":
		return "inCinemas"
	default: // released, preDB, deleted, unknown
		return "released"
	}
}

func (o Options) profileFor(id int) string {
	if p := strings.TrimSpace(o.Profiles[id]); p != "" {
		return p
	}
	return o.DefaultProfile
}

func (o Options) folderFor(root string) *int64 {
	if id, ok := o.Folders[root]; ok {
		return &id
	}
	if id, ok := o.Folders[slashes(root)]; ok {
		return &id
	}
	return nil
}

func (imp *Importer) runMovies(ctx context.Context, snap *Snapshot, opts Options, res *Result, progress Progress) error {
	idx, err := imp.movieIndex(ctx)
	if err != nil {
		return err
	}
	source := snap.Status.Name(snap.App)
	total := len(snap.Movies)
	for i, m := range snap.Movies {
		progress(i, total, m.Title)
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("the import stopped after %d of %d films: %w", i, total, err)
		}
		res.Total++
		err := imp.guarded(m.Title, func() error { return imp.oneMovie(ctx, m, opts, source, &idx, res) })
		if err != nil {
			res.Failed++
			res.note(m.Title, m.Year, "failed", err.Error())
			imp.log().Warn("arr import: a film failed", "title", m.Title, "tmdb", m.TMDBID, "err", err)
		}
	}
	progress(total, total, "")
	return nil
}

// oneMovie brings one Radarr film over. An error is the film failing; skips are noted here.
func (imp *Importer) oneMovie(ctx context.Context, m Movie, opts Options, source string, idx *movieIndex, res *Result) error {
	if m.TMDBID <= 0 {
		res.Skipped++
		res.note(m.Title, m.Year, "skipped", "Radarr has no TMDB id for it")
		return nil
	}
	local, problem := imp.movieFile(ctx, m, opts, *idx)
	if have, ok := idx.byTMDB[m.TMDBID]; ok {
		// Already here: left as it is, except that a film still without a file gets the one
		// Radarr has.
		if have.HasFile || local == "" || problem != "" {
			res.Existing++
			return nil
		}
		if err := imp.Movies.AttachImported(ctx, have.ID, local, source); err != nil {
			return fmt.Errorf("couldn't attach its file: %w", err)
		}
		have.HasFile, have.MovieFilePath = true, local
		idx.byTMDB[m.TMDBID] = have
		idx.owned[filepath.Clean(local)] = true
		res.Existing++
		res.FilesAttached++
		res.note(m.Title, m.Year, "attached", "already in Arrmada without a file — attached "+filepath.Base(local))
		return nil
	}
	file := local
	if problem != "" {
		if !opts.ImportMissing {
			res.Skipped++
			res.note(m.Title, m.Year, "skipped", problem)
			return nil
		}
		file = ""
		res.note(m.Title, m.Year, "imported", "added without its file ("+problem+")")
	}
	o := movies.AddOptions{
		QualityProfile:  opts.profileFor(m.ProfileID),
		Monitored:       m.Monitored,
		MinAvailability: availability(m.MinimumAvailability),
	}
	if file == "" {
		o.FolderID = opts.folderFor(m.Root())
		// Wanted: marked to be searched in its turn before it's monitored.
		o.SearchPaced = true
	}
	created, err := imp.Movies.AddImported(ctx, m.TMDBID, o, file, source)
	if errors.Is(err, movies.ErrExists) {
		res.Existing++ // added meanwhile (a request, another tab)
		return nil
	}
	if err != nil {
		if created.ID > 0 {
			// Added but not finished: AddImported left it unmonitored, and it's here now.
			idx.byTMDB[m.TMDBID] = created
		}
		return err
	}
	idx.byTMDB[m.TMDBID] = created
	res.Imported++
	if !m.Monitored {
		res.Unmonitored++
	}
	if file != "" {
		idx.owned[filepath.Clean(file)] = true
		res.FilesAttached++
	} else if created.Monitored {
		res.Paced++
	}
	return nil
}

func (imp *Importer) runSeries(ctx context.Context, c *Client, snap *Snapshot, opts Options, res *Result, progress Progress) error {
	idx, err := imp.seriesIndex(ctx)
	if err != nil {
		return err
	}
	imp.lookupTVDB(ctx, snap.Series, &idx)
	source := snap.Status.Name(snap.App)
	total := len(snap.Series)
	for i, s := range snap.Series {
		progress(i, total, s.Title)
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("the import stopped after %d of %d shows: %w", i, total, err)
		}
		res.Total++
		err := imp.guarded(s.Title, func() error { return imp.oneSeries(ctx, c, s, opts, source, &idx, res) })
		if err != nil {
			res.Failed++
			res.note(s.Title, s.Year, "failed", err.Error())
			imp.log().Warn("arr import: a show failed", "title", s.Title, "tvdb", s.TVDBID, "err", err)
		}
	}
	progress(total, total, "")
	return nil
}

// episodeFile is one of the show's files: Sonarr's record and where it is inside Arrmada.
type episodeFile struct {
	File
	local string
}

// showFiles reads a show's episode files and sorts them into the ones Arrmada can take and
// the problems with the rest.
func (imp *Importer) showFiles(ctx context.Context, c *Client, s Series, opts Options, idx seriesIndex) (ok []episodeFile, problems []string, err error) {
	if s.Statistics.EpisodeFileCount == 0 {
		return nil, nil, nil
	}
	files, err := c.EpisodeFiles(ctx, s.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't read its episode files from Sonarr: %w", err)
	}
	for _, f := range files {
		arr := EpisodeFilePath(f, s)
		if arr == "" {
			problems = append(problems, "Sonarr lists an episode file without saying where it is")
			continue
		}
		local := filepath.FromSlash(Remap(arr, opts.PathMaps))
		fi, serr := imp.stat(local)
		_, inside := imp.Series.LibraryFolderOf(ctx, local)
		switch {
		case serr != nil || fi.IsDir():
			problems = append(problems, "not found at "+local)
		case !inside:
			problems = append(problems, local+" isn't inside one of Arrmada's TV folders")
		case idx.owned[filepath.Clean(local)]:
			// Already recorded (an earlier run, or a library scan): nothing to do for it.
		default:
			ok = append(ok, episodeFile{File: f, local: local})
		}
	}
	return ok, problems, nil
}

// sameNumbering says, season by season, whether Arrmada numbers the show's episodes the way
// Sonarr does (TVDB's numbering): always when Arrmada's listing is TVDB's own, else when the
// season has as many episodes in both. Only there are Sonarr's per-episode choices and its
// own file-to-episode mapping used; elsewhere monitoring follows the season choice and files
// go by their names. A season only one side has counts as different.
func sameNumbering(show series.Series, eps []Episode) map[int]bool {
	arr := map[int]int{}
	for _, e := range eps {
		arr[e.SeasonNumber]++
	}
	tvdb := strings.EqualFold(show.NumberingSource, "tvdb")
	out := map[int]bool{}
	for _, sn := range show.Seasons {
		out[sn.SeasonNumber] = tvdb || len(sn.Episodes) == arr[sn.SeasonNumber]
	}
	for n := range arr {
		if _, ok := out[n]; !ok {
			out[n] = false
		}
	}
	return out
}

// differing is the regular seasons sameNumbering found numbered differently, in order.
func differing(same map[int]bool) []int {
	var out []int
	for n, ok := range same {
		if !ok && n > 0 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// attachEpisodes records each file on the episodes it holds that have no file yet: the
// episodes its name resolves to (multi-episode files, anime absolute numbers), else — in a
// season numbered the same way (same) — the episodes Sonarr filed it under. An episode that
// has a file by now keeps it. It returns how many files it recorded and the names of the
// ones it couldn't place.
func (imp *Importer) attachEpisodes(ctx context.Context, show series.Series, files []episodeFile, arrEps []Episode, same map[int]bool, idx *seriesIndex) (attached int, unplaced []string, err error) {
	type slot struct{ season, episode int }
	have := map[slot]series.Episode{}
	for _, sn := range show.Seasons {
		for _, e := range sn.Episodes {
			have[slot{e.SeasonNumber, e.EpisodeNumber}] = e
		}
	}
	for _, f := range files {
		refs := imp.Series.ResolveEpisodes(ctx, show.ID, parser.Parse(filepath.Base(f.local)))
		var slots []slot
		for _, r := range refs {
			if _, ok := have[slot{r.Season, r.Episode}]; ok {
				slots = append(slots, slot{r.Season, r.Episode})
			}
		}
		if len(slots) == 0 {
			for _, e := range arrEps {
				if e.EpisodeFileID > 0 && e.EpisodeFileID == f.ID && same[e.SeasonNumber] {
					if _, ok := have[slot{e.SeasonNumber, e.EpisodeNumber}]; ok {
						slots = append(slots, slot{e.SeasonNumber, e.EpisodeNumber})
					}
				}
			}
		}
		placed := false
		for _, sl := range slots {
			if have[sl].HasFile {
				continue
			}
			ok, err := imp.Series.AdoptEpisodeFile(ctx, show.ID, sl.season, sl.episode, f.local, f.Size)
			if err != nil {
				return attached, unplaced, err
			}
			e := have[sl]
			e.HasFile = true // recorded now, or it had one by the time we got there
			if ok {
				e.FilePath = f.local
				placed = true
			}
			have[sl] = e
		}
		if placed {
			attached++
			idx.owned[filepath.Clean(f.local)] = true
		} else if len(slots) == 0 {
			unplaced = append(unplaced, filepath.Base(f.local))
		}
	}
	return attached, unplaced, nil
}

// monitorNew is whether seasons added later are monitored: Sonarr 4 says; on Sonarr 3 they
// follow the show's latest season.
func monitorNew(s Series) bool {
	switch strings.ToLower(strings.TrimSpace(s.MonitorNewItems)) {
	case "all":
		return true
	case "none":
		return false
	}
	last, mon := -1, false
	for _, sn := range s.Seasons {
		if sn.SeasonNumber > last {
			last, mon = sn.SeasonNumber, sn.Monitored
		}
	}
	return mon
}

// oneSeries brings one Sonarr show over. An error is the show failing.
//
// A new show is added paused (its monitored gate off), so no sweep searches it while it's
// being set up: its episodes' monitoring is copied, its files recorded and its paced-search
// mark set, and only then — when every step went through — is it monitored as Sonarr had
// it. Everything after the show exists runs to the end even when ctx is cancelled (the
// import stopped or timed out), so a show is left complete, or paused, never wanting
// episodes the person already has.
func (imp *Importer) oneSeries(ctx context.Context, c *Client, s Series, opts Options, source string, idx *seriesIndex, res *Result) error {
	existing, tmdb, why := imp.showMatch(s, *idx)
	if existing == 0 && tmdb <= 0 {
		res.Skipped++
		res.note(s.Title, s.Year, "skipped", why)
		return nil
	}
	if existing == 0 {
		if id, ok := idx.byTMDB[tmdb]; ok {
			existing = id // added earlier in this run (two Sonarr shows, one TMDB show)
		}
	}
	files, problems, err := imp.showFiles(ctx, c, s, opts, *idx)
	if err != nil {
		return err
	}
	if existing > 0 {
		// Already here: left as it is, except that episodes still without a file get the
		// ones Sonarr has.
		res.Existing++
		if len(files) == 0 {
			return nil
		}
		eps, err := c.Episodes(ctx, s.ID)
		if err != nil {
			return fmt.Errorf("couldn't read its episodes from Sonarr: %w", err)
		}
		show, err := imp.Series.Get(ctx, existing)
		if err != nil {
			return err
		}
		n, _, err := imp.attachEpisodes(ctx, show, files, eps, sameNumbering(show, eps), idx)
		if n > 0 {
			res.FilesAttached += n
			imp.Series.AddEvent(ctx, existing, "imported", fmt.Sprintf("Brought over from %s: %d episode files", source, n))
			res.note(s.Title, s.Year, "attached", fmt.Sprintf("already in Arrmada — attached %d episode files it didn't have", n))
		}
		if err != nil {
			return fmt.Errorf("couldn't attach all of its episode files: %w", err)
		}
		return nil
	}
	if len(problems) > 0 && !opts.ImportMissing {
		res.Skipped++
		res.note(s.Title, s.Year, "skipped", fmt.Sprintf("%d of its episode files can't be used (%s)", len(problems), problems[0]))
		return nil
	}
	eps, err := c.Episodes(ctx, s.ID)
	if err != nil {
		return fmt.Errorf("couldn't read its episodes from Sonarr: %w", err)
	}
	seasons := map[int]bool{}
	for _, sn := range s.Seasons {
		if sn.SeasonNumber > 0 && sn.Monitored {
			seasons[sn.SeasonNumber] = true
		}
	}
	mns := monitorNew(s)
	o := series.AddOptions{Monitored: false, Seasons: seasons, MonitorNewSeasons: &mns}
	if len(files) > 0 {
		// The show belongs to the TV folder its files are in.
		if id, ok := imp.Series.LibraryFolderOf(ctx, files[0].local); ok {
			o.FolderID = &id
		}
	} else {
		o.FolderID = opts.folderFor(s.Root())
	}
	added, err := imp.Series.AddWith(ctx, tmdb, opts.profileFor(s.ProfileID), o)
	if errors.Is(err, series.ErrExists) {
		res.Existing++
		return nil
	}
	if err != nil {
		return err
	}
	idx.byTMDB[tmdb] = added.ID
	if s.TVDBID > 0 {
		idx.byTVDB[s.TVDBID] = added.ID
	}
	res.Imported++
	wctx := context.WithoutCancel(ctx)
	// unsafe names the first step that failed; the show then stays paused.
	unsafe := ""
	if strings.EqualFold(s.SeriesType, series.SeriesTypeAnime) && added.SeriesType != series.SeriesTypeAnime {
		if err := imp.Series.SetType(wctx, added.ID, series.SeriesTypeAnime); err != nil {
			res.note(s.Title, s.Year, "imported", "couldn't switch it to anime numbering: "+err.Error())
		}
	}
	show, err := imp.Series.Get(wctx, added.ID)
	if err != nil {
		unsafe = "couldn't read it back: " + err.Error()
	}
	var same map[int]bool
	if unsafe == "" {
		same = sameNumbering(show, eps)
		if diff := differing(same); len(diff) > 0 {
			res.note(s.Title, s.Year, "imported", fmt.Sprintf("Arrmada numbers season %s differently from Sonarr — there, which episodes are monitored follows the season choice, and files are matched by their names", joinInts(diff)))
		}
		if err := imp.copyEpisodeMonitoring(wctx, show, eps, same); err != nil {
			unsafe = "couldn't copy which episodes are monitored: " + err.Error()
		}
	}
	if unsafe == "" {
		n, unplaced, err := imp.attachEpisodes(wctx, show, files, eps, same, idx)
		res.FilesAttached += n
		if n > 0 {
			imp.Series.AddEvent(wctx, added.ID, "imported", fmt.Sprintf("Brought over from %s: %d episode files", source, n))
		}
		if len(unplaced) > 0 {
			res.note(s.Title, s.Year, "imported", fmt.Sprintf("%d episode files didn't match an episode here (%s)", len(unplaced), unplaced[0]))
		}
		if err != nil {
			unsafe = "couldn't attach all of its episode files: " + err.Error()
		}
	}
	if len(problems) > 0 {
		res.note(s.Title, s.Year, "imported", fmt.Sprintf("added without %d episode files (%s)", len(problems), problems[0]))
	}
	if !s.Monitored {
		res.Unmonitored++ // added with searching off, as it was in Sonarr
		return nil
	}
	if unsafe == "" {
		if err := imp.Series.SetSearchPaced(wctx, added.ID, true); err != nil {
			unsafe = "couldn't mark it to be searched in its turn: " + err.Error()
		}
	}
	if unsafe == "" {
		if err := imp.Series.SetMonitored(wctx, added.ID, true); err != nil {
			unsafe = "couldn't monitor it: " + err.Error()
		}
	}
	if unsafe != "" {
		res.note(s.Title, s.Year, "imported", unsafe+" — left with searching off, so nothing is downloaded for it; check it, then turn searching on from its page")
		return nil
	}
	res.Paced++
	return nil
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// copyEpisodeMonitoring makes each episode's monitored flag Sonarr's, where Arrmada has the
// same episode in a season numbered the same way (same): the season choice set most of them
// already; this carries the exceptions (one episode skipped in a monitored season). Specials
// are left to the season choice, which never monitors them.
func (imp *Importer) copyEpisodeMonitoring(ctx context.Context, show series.Series, eps []Episode, same map[int]bool) error {
	type slot struct{ season, episode int }
	want := map[slot]bool{}
	for _, e := range eps {
		if e.SeasonNumber > 0 && same[e.SeasonNumber] {
			want[slot{e.SeasonNumber, e.EpisodeNumber}] = e.Monitored
		}
	}
	var on, off []int64
	for _, sn := range show.Seasons {
		for _, e := range sn.Episodes {
			w, ok := want[slot{e.SeasonNumber, e.EpisodeNumber}]
			if !ok || w == e.Monitored {
				continue
			}
			if w {
				on = append(on, e.ID)
			} else {
				off = append(off, e.ID)
			}
		}
	}
	sort.Slice(on, func(i, j int) bool { return on[i] < on[j] })
	sort.Slice(off, func(i, j int) bool { return off[i] < off[j] })
	if _, err := imp.Series.SetEpisodesMonitored(ctx, show.ID, on, true); err != nil {
		return err
	}
	_, err := imp.Series.SetEpisodesMonitored(ctx, show.ID, off, false)
	return err
}
