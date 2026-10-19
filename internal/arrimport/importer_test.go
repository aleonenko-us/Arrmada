package arrimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The import runs against a real Radarr 6.4's and Sonarr 4.0's recorded answers
// (testdata/live: four films, two shows, synthetic files under /media) and Arrmada's real
// movies and series services over a real database, with TMDB replaced by a stand-in.

// liveArr serves testdata/live as the app would, checking the API key.
func liveArr(t *testing.T, app string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		name := app + "_" + strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/api/v3/"), "/", "_")
		switch {
		case r.URL.Path == "/api/v3/episode" || r.URL.Path == "/api/v3/episodefile":
			name += "_" + r.URL.Query().Get("seriesId")
		case r.URL.Path == "/api/v3/moviefile":
			name = app + "_moviefile"
		}
		body, err := os.ReadFile(filepath.Join("testdata", "live", name+".json"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func liveClient(t *testing.T, app string) *Client {
	t.Helper()
	c, err := New(app, liveArr(t, app).URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// stand-in TMDB: films by id, and shows whose listing is Sonarr's own (so the numbering
// matches, as it does with a TVDB key).
type fakeMovieMeta struct{}

func (fakeMovieMeta) Available() bool { return true }
func (fakeMovieMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (fakeMovieMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, metadata.ErrNotFound
}
func (fakeMovieMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	titles := map[int]string{949: "Heat", 8195: "Ronin", 14160: "Up", 1091: "The Thing"}
	if titles[id] == "" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.MovieDetails{MovieResult: metadata.MovieResult{TMDBID: id, Title: titles[id], Year: 2000}, Status: "Released", ReleaseDate: "2000-01-01"}, nil
}

// fakeSeriesMeta's listing is TVDB's (Sonarr's own) unless source says otherwise; short
// drops the last episode of the seasons it names, the way another source numbers a show.
type fakeSeriesMeta struct {
	t      *testing.T
	source string
	short  map[int]bool
}

func (fakeSeriesMeta) Available() bool { return true }
func (fakeSeriesMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (f fakeSeriesMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	shows := map[int]struct {
		file, title string
		tvdb        int
	}{1396: {"sonarr_episode_1.json", "Breaking Bad", 81189}, 209867: {"sonarr_episode_2.json", "Frieren", 424536}}
	show, ok := shows[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	body, err := os.ReadFile(filepath.Join("testdata", "live", show.file))
	if err != nil {
		f.t.Fatal(err)
	}
	var eps []Episode
	if err := json.Unmarshal(body, &eps); err != nil {
		f.t.Fatal(err)
	}
	bySeason := map[int][]metadata.EpisodeDetails{}
	for _, e := range eps {
		bySeason[e.SeasonNumber] = append(bySeason[e.SeasonNumber], metadata.EpisodeDetails{EpisodeNumber: e.EpisodeNumber, AirDate: "2010-01-01"})
	}
	src := f.source
	if src == "" {
		src = "tvdb"
	}
	d := &metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: id, Title: show.title, Year: 2008}, TVDBID: show.tvdb, NumberingSource: src}
	for sn, list := range bySeason {
		if f.short[sn] && len(list) > 1 {
			list = list[:len(list)-1]
		}
		d.Seasons = append(d.Seasons, metadata.SeasonDetails{SeasonNumber: sn, Episodes: list})
	}
	sort.Slice(d.Seasons, func(i, j int) bool { return d.Seasons[i].SeasonNumber < d.Seasons[j].SeasonNumber })
	return d, nil
}

// world is Arrmada with two Movies folders (movies, kids-movies) and two TV folders (tv,
// anime) under base, the folders Radarr and Sonarr call /media/….
type world struct {
	base    string
	movies  *movies.Service
	series  *series.Service
	folders *libfolders.Service
	kids    int64 // the Kids movies folder's id
	anime   int64 // the Anime TV folder's id
	imp     *Importer
	ctx     context.Context
	meta    *fakeSeriesMeta // the stand-in TMDB for shows, to change its numbering
}

func newWorld(t *testing.T) *world {
	t.Helper()
	base := t.TempDir()
	for _, d := range []string{"movies", "kids-movies", "tv", "anime", "data", "downloads"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	roots := libroots.New(nil, config.Config{MoviesDir: filepath.Join(base, "movies"), TVDir: filepath.Join(base, "tv"),
		DownloadsDir: filepath.Join(base, "downloads"), DataDir: filepath.Join(base, "data")})
	fs := libfolders.New(st.DB(), roots, filepath.Join(base, "data"))
	roots.WithExtras(fs.Extras)
	kids, err := fs.Add(ctx, libfolders.AddInput{Media: libfolders.MediaMovie, Path: filepath.Join(base, "kids-movies"), Label: "Kids"})
	if err != nil {
		t.Fatal(err)
	}
	anime, err := fs.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: filepath.Join(base, "anime"), Label: "Anime"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mv := movies.NewService(st.DB(), fakeMovieMeta{}, nil, filepath.Join(base, "movies"), "", nil, log)
	mv.SetFolders(fs)
	meta := &fakeSeriesMeta{t: t}
	sv := series.NewService(st.DB(), meta, filepath.Join(base, "tv"), log)
	sv.SetFolders(fs)
	return &world{base: base, movies: mv, series: sv, folders: fs, kids: kids.ID, anime: anime.ID, ctx: ctx, meta: meta,
		imp: &Importer{Movies: mv, Series: sv, Log: log}}
}

// file puts a synthetic video where Radarr/Sonarr said it is (/media/… → base/…).
func (w *world) file(t *testing.T, arrPath string) string {
	t.Helper()
	p := filepath.Join(w.base, filepath.FromSlash(strings.TrimPrefix(arrPath, "/media/")))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (w *world) remap() []PathMap { return []PathMap{{From: "/media", To: filepath.ToSlash(w.base)}} }

const (
	heatFile = "/media/movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv"
	upFile   = "/media/kids-movies/Up (2009)/Up (2009) Bluray-2160p.mkv"
	bbE1     = "/media/tv/Breaking Bad/Season 1/Breaking Bad - S01E01 - Pilot WEBDL-1080p.mkv"
	bbE2     = "/media/tv/Breaking Bad/Season 1/Breaking Bad - S01E02 - Cats in the Bag WEBDL-1080p.mkv"
	frE1     = "/media/anime/Frieren/Season 1/Frieren - S01E01 - The Journey End WEBDL-1080p.mkv"
)

// The recorded answers decode into what the import reads.
func TestLiveShapes(t *testing.T) {
	ctx := context.Background()
	snap, err := Load(ctx, liveClient(t, Radarr))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status.AppName != "Radarr" || snap.Status.Name(Radarr) != "Radarr" || len(snap.Movies) != 4 || len(snap.Roots) != 2 {
		t.Fatalf("radarr snapshot = %+v", snap)
	}
	res := map[string]int{}
	for _, p := range snap.Profiles {
		res[p.Name] = p.MaxResolution
	}
	if res["HD-1080p"] != 1080 || res["Ultra-HD"] != 2160 || res["HD - 720p/1080p"] != 1080 || res["SD"] != 576 {
		t.Errorf("profile resolutions = %v", res)
	}
	heat := snap.Movies[0]
	if heat.TMDBID != 949 || heat.FilePath() != heatFile || heat.Root() != "/media/movies" || !heat.Monitored || heat.ProfileID != 4 {
		t.Errorf("heat = %+v (file %q)", heat, heat.FilePath())
	}
	if snap.Roots[0].Path != "/media/kids-movies" || snap.Roots[0].Titles != 1 || snap.Roots[1].Titles != 3 {
		t.Errorf("roots = %+v", snap.Roots)
	}

	c := liveClient(t, Sonarr)
	ss, err := Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss.Series) != 2 || ss.Media() != "series" {
		t.Fatalf("sonarr snapshot = %+v", ss)
	}
	bb, fr := ss.Series[0], ss.Series[1]
	if bb.TVDBID != 81189 || bb.TMDBID != 1396 || bb.SeriesType != "standard" || bb.MonitorNewItems != "none" || bb.Statistics.EpisodeFileCount != 2 {
		t.Errorf("breaking bad = %+v", bb)
	}
	if fr.SeriesType != "anime" || fr.MonitorNewItems != "all" || fr.Root() != "/media/anime" {
		t.Errorf("frieren = %+v", fr)
	}
	files, err := c.EpisodeFiles(ctx, bb.ID)
	if err != nil || len(files) != 2 || EpisodeFilePath(files[0], bb) != bbE1 {
		t.Errorf("episode files = %+v, %v", files, err)
	}
	eps, err := c.Episodes(ctx, bb.ID)
	if err != nil || len(eps) == 0 {
		t.Fatalf("episodes = %v", err)
	}
	for _, e := range eps {
		if e.SeasonNumber == 1 && e.EpisodeNumber == 3 && e.Monitored {
			t.Error("S01E03 should be unmonitored in the recording")
		}
	}
	// The wrong app at the address says so.
	wrong, _ := New(Sonarr, liveArr(t, Radarr).URL, "test-key")
	if _, err := wrong.Status(ctx); err == nil || !strings.Contains(err.Error(), "pick Radarr") {
		t.Errorf("radarr answering as sonarr: %v", err)
	}
}

func movieByTMDB(t *testing.T, w *world, tmdb int) movies.Movie {
	t.Helper()
	m, err := w.movies.GetByTMDB(w.ctx, tmdb)
	if err != nil {
		t.Fatalf("film %d: %v", tmdb, err)
	}
	return m
}

// Radarr's films come over with their profile, monitoring, availability, folder and files —
// and a second run adds nothing.
func TestImportMoviesIdempotent(t *testing.T) {
	w := newWorld(t)
	heat, up := w.file(t, heatFile), w.file(t, upFile)
	c := liveClient(t, Radarr)
	snap, err := Load(w.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Profiles:       map[int]string{4: "custom:7", 5: "custom:8"},
		DefaultProfile: "custom:1",
		Folders:        map[string]int64{"/media/movies": 0, "/media/kids-movies": w.kids},
		PathMaps:       w.remap(),
	}
	pv, err := w.imp.Preview(w.ctx, snap, opts, []Target{{Key: "custom:1", Name: "Any", Default: true}, {Key: "custom:7", Name: "HD-1080p", MaxResolution: 1080}})
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Titles: 4, New: 4, WithFiles: 2, FilesFound: 2, Wanted: 1}
	if pv.Counts != want {
		t.Fatalf("preview counts = %+v, want %+v", pv.Counts, want)
	}
	suggested := map[string]string{}
	for _, p := range pv.Profiles {
		suggested[p.Name] = p.Suggested
	}
	if suggested["HD-1080p"] != "custom:7" || suggested["Ultra-HD"] != "custom:1" {
		t.Errorf("suggested profiles = %v", suggested)
	}

	res, err := w.imp.Run(w.ctx, c, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 4 || res.FilesAttached != 2 || res.Paced != 1 || res.Unmonitored != 1 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}
	h := movieByTMDB(t, w, 949)
	if !h.HasFile || h.MovieFilePath != heat || h.QualityProfile != "custom:7" || !h.Monitored || h.LibraryFolderID != 0 {
		t.Errorf("heat = %+v", h)
	}
	u := movieByTMDB(t, w, 14160)
	if !u.HasFile || u.MovieFilePath != up || u.QualityProfile != "custom:8" || u.LibraryFolderID != w.kids {
		t.Errorf("up = %+v, want its file in the Kids folder (%d)", u, w.kids)
	}
	thing := movieByTMDB(t, w, 1091)
	if thing.Monitored || thing.HasFile || thing.MinAvailability != "announced" || thing.QualityProfile != "custom:1" {
		t.Errorf("the thing = %+v (profile 3 has no mapping: the default)", thing)
	}
	ronin := movieByTMDB(t, w, 8195)
	paced, err := w.movies.PacedSearchIDs(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paced) != 1 || !paced[ronin.ID] {
		t.Errorf("paced = %v, want only Ronin (%d), the one wanted film", paced, ronin.ID)
	}

	again, err := w.imp.Run(w.ctx, c, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Imported != 0 || again.FilesAttached != 0 || again.Existing != 4 || again.Paced != 0 {
		t.Fatalf("second run = %+v, want everything already here", again)
	}
	all, _ := w.movies.List(w.ctx)
	if len(all) != 4 {
		t.Fatalf("%d films after two runs, want 4", len(all))
	}
}

// Without the path mapping, Radarr's /media paths aren't where Arrmada sees the files:
// those films are skipped (never imported to be downloaded again), unless the person asks
// for them anyway — then they're added without the file and wait their turn to be searched.
func TestImportMoviesPathMapping(t *testing.T) {
	w := newWorld(t)
	w.file(t, heatFile)
	w.file(t, upFile)
	c := liveClient(t, Radarr)
	snap, _ := Load(w.ctx, c)

	pv, err := w.imp.Preview(w.ctx, snap, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Counts.NotFound != 2 || pv.Counts.FilesFound != 0 || len(pv.NotFound) != 2 || !strings.Contains(pv.NotFound[0].Reason, "not found at /media/") {
		t.Fatalf("unmapped preview = %+v %+v", pv.Counts, pv.NotFound)
	}
	res, err := w.imp.Run(w.ctx, c, snap, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Skipped != 2 || res.FilesAttached != 0 {
		t.Fatalf("unmapped run = %+v", res)
	}
	if _, err := w.movies.GetByTMDB(w.ctx, 949); !errors.Is(err, movies.ErrNotFound) {
		t.Errorf("Heat was added without its file: %v", err)
	}

	// Now with the mapping: the two skipped films come over with their files.
	res, err = w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.FilesAttached != 2 || res.Existing != 2 {
		t.Fatalf("mapped run = %+v", res)
	}

	// A file mapped to somewhere outside the Movies folders is never attached.
	w2 := newWorld(t)
	elsewhere := t.TempDir()
	p := filepath.Join(elsewhere, "movies", "Heat (1995)", "Heat (1995) Bluray-1080p.mkv")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("x"), 0o644)
	res, err = w2.imp.Run(w2.ctx, c, snap, Options{PathMaps: []PathMap{{From: "/media", To: filepath.ToSlash(elsewhere)}}, ImportMissing: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := movieByTMDB(t, w2, 949)
	if h.HasFile {
		t.Errorf("a file outside the library was attached: %s", h.MovieFilePath)
	}
	if res.FilesAttached != 0 || res.Imported != 4 || res.Paced != 3 {
		t.Errorf("import-missing run = %+v, want 4 added without files, 3 of them wanted", res)
	}
}

// A film already in Arrmada without a file gets Radarr's, and is otherwise left alone.
func TestImportAttachesToExistingFilm(t *testing.T) {
	w := newWorld(t)
	heat := w.file(t, heatFile)
	m, err := w.movies.AddWith(w.ctx, 949, movies.AddOptions{QualityProfile: "custom:2", Monitored: false})
	if err != nil {
		t.Fatal(err)
	}
	c := liveClient(t, Radarr)
	snap, _ := Load(w.ctx, c)
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap(), ImportMissing: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := movieByTMDB(t, w, 949)
	if got.ID != m.ID || !got.HasFile || got.MovieFilePath != heat || got.QualityProfile != "custom:2" || got.Monitored {
		t.Errorf("existing film = %+v", got)
	}
	if res.Existing != 1 || res.FilesAttached != 1 {
		t.Errorf("result = %+v", res)
	}
}

// failingMovies is the real service with one film whose add fails and one whose add panics.
type failingMovies struct{ *movies.Service }

func (f failingMovies) AddImported(ctx context.Context, tmdbID int, o movies.AddOptions, file, source string) (movies.Movie, error) {
	switch tmdbID {
	case 8195:
		return movies.Movie{}, errors.New("TMDB is having a moment")
	case 1091:
		panic("boom")
	}
	return f.Service.AddImported(ctx, tmdbID, o, file, source)
}

// One title failing — an error or a panic — is that title's failure; the rest import.
func TestImportIsolatesTitleErrors(t *testing.T) {
	w := newWorld(t)
	w.file(t, heatFile)
	w.file(t, upFile)
	w.imp.Movies = failingMovies{w.movies}
	c := liveClient(t, Radarr)
	snap, _ := Load(w.ctx, c)
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Failed != 2 || res.FilesAttached != 2 {
		t.Fatalf("result = %+v", res)
	}
	var reasons []string
	for _, n := range res.Notes {
		if n.Outcome == "failed" {
			reasons = append(reasons, n.Title+": "+n.Reason)
		}
	}
	if len(reasons) != 2 || !strings.Contains(strings.Join(reasons, "|"), "TMDB is having a moment") {
		t.Errorf("failure notes = %v", reasons)
	}
	// A cancelled import stops between titles and says how far it got.
	ctx, cancel := context.WithCancel(w.ctx)
	defer cancel()
	stop := func(done, _ int, _ string) {
		if done == 2 {
			cancel()
		}
	}
	if _, err := w.imp.Run(ctx, c, snap, Options{}, stop); err == nil || !strings.Contains(err.Error(), "stopped after 2 of 4") {
		t.Errorf("cancelled run: %v", err)
	}
}

func showByTMDB(t *testing.T, w *world, tmdb int) series.Series {
	t.Helper()
	all, err := w.series.List(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.TMDBID == tmdb {
			full, err := w.series.Get(w.ctx, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			return full
		}
	}
	t.Fatalf("show %d isn't in the library", tmdb)
	return series.Series{}
}

func episode(s series.Series, season, ep int) (series.Episode, bool) {
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if e.SeasonNumber == season && e.EpisodeNumber == ep {
				return e, true
			}
		}
	}
	return series.Episode{}, false
}

// Sonarr's shows come over with their monitored seasons and episodes, anime type, folder
// and episode files — and a second run adds nothing.
func TestImportSeriesIdempotent(t *testing.T) {
	w := newWorld(t)
	e1, e2, fe1 := w.file(t, bbE1), w.file(t, bbE2), w.file(t, frE1)
	c := liveClient(t, Sonarr)
	snap, err := Load(w.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Profiles: map[int]string{4: "custom:9"}, PathMaps: w.remap()}
	pv, err := w.imp.Preview(w.ctx, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Counts != (Counts{Titles: 2, New: 2, WithFiles: 2, FilesFound: 2, Wanted: 2}) {
		t.Fatalf("preview = %+v", pv.Counts)
	}
	res, err := w.imp.Run(w.ctx, c, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.FilesAttached != 3 || res.Paced != 2 || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("result = %+v notes %+v", res, res.Notes)
	}

	bb := showByTMDB(t, w, 1396)
	if !bb.Monitored || bb.MonitorNewSeasons || bb.QualityProfile != "custom:9" || bb.SeriesType != series.SeriesTypeStandard || bb.LibraryFolderID != 0 {
		t.Errorf("breaking bad = %+v", bb)
	}
	for _, c := range []struct {
		s, e     int
		mon, has bool
		path     string
	}{{1, 1, true, true, e1}, {1, 2, true, true, e2}, {1, 3, false, false, ""}, {1, 4, true, false, ""}, {2, 1, false, false, ""}} {
		ep, ok := episode(bb, c.s, c.e)
		if !ok || ep.Monitored != c.mon || ep.HasFile != c.has || ep.FilePath != c.path {
			t.Errorf("S%02dE%02d = %+v, want monitored=%v file=%q", c.s, c.e, ep, c.mon, c.path)
		}
	}
	fr := showByTMDB(t, w, 209867)
	if fr.SeriesType != series.SeriesTypeAnime || !fr.MonitorNewSeasons || fr.LibraryFolderID != w.anime {
		t.Errorf("frieren = %+v, want anime in the Anime folder (%d)", fr, w.anime)
	}
	if ep, _ := episode(fr, 1, 1); !ep.HasFile || ep.FilePath != fe1 {
		t.Errorf("frieren S01E01 = %+v", ep)
	}
	if ep, _ := episode(fr, 2, 1); !ep.Monitored {
		t.Errorf("frieren S02E01 = %+v, want monitored", ep)
	}

	again, err := w.imp.Run(w.ctx, c, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Imported != 0 || again.FilesAttached != 0 || again.Existing != 2 {
		t.Fatalf("second run = %+v", again)
	}
	all, _ := w.series.List(w.ctx)
	if len(all) != 2 {
		t.Fatalf("%d shows after two runs", len(all))
	}
}

// A show Sonarr has no TMDB id for is found through its TVDB id; one TMDB doesn't know
// either is skipped with a reason.
func TestImportSeriesByTVDB(t *testing.T) {
	w := newWorld(t)
	srv := liveArr(t, Sonarr)
	// Sonarr 3: no tmdbId on the shows.
	body, _ := os.ReadFile(filepath.Join("testdata", "live", "sonarr_series.json"))
	var raw []map[string]any
	_ = json.Unmarshal(body, &raw)
	for _, s := range raw {
		delete(s, "tmdbId")
	}
	stripped, _ := json.Marshal(raw)
	v3 := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/series" {
			_, _ = rw.Write(stripped)
			return
		}
		r2 := r.Clone(r.Context())
		resp, err := http.DefaultClient.Do(mustReq(t, srv.URL+r.URL.RequestURI(), r2.Header))
		if err != nil {
			rw.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		rw.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(rw, resp.Body)
	}))
	defer v3.Close()
	c, _ := New(Sonarr, v3.URL, "test-key")
	w.imp.TMDBForTVDB = func(_ context.Context, tvdb int) (int, error) {
		if tvdb == 81189 {
			return 1396, nil
		}
		return 0, metadata.ErrNotFound
	}
	snap, err := Load(w.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.imp.Run(w.ctx, c, snap, Options{ImportMissing: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || res.Skipped != 1 || len(res.Notes) == 0 {
		t.Fatalf("result = %+v", res)
	}
	found := false
	for _, n := range res.Notes {
		if n.Outcome == "skipped" && strings.Contains(n.Reason, "TVDB id 424536") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %+v, want Frieren skipped naming its TVDB id", res.Notes)
	}
	showByTMDB(t, w, 1396)
}

func mustReq(t *testing.T, url string, h http.Header) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = h
	return r
}
