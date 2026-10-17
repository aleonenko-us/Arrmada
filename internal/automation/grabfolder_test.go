package automation

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/store"
)

// memSettings is an in-memory settings store for the folder resolver.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) get(_ context.Context, key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		return v
	}
	return def
}

func (s *memSettings) set(_ context.Context, key, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = v
	return nil
}

// folderRig is a coordinator over two Movies folders (Movies, Kids) and two TV folders
// (TV, Anime) in temp dirs, with a fake qBittorrent that records each add's save path.
type folderRig struct {
	c                       *Coordinator
	qb                      *savePathQbit
	roots                   *libroots.Roots
	folders                 *libfolders.Service
	base                    string
	movies, kids, tv, anime string
	dl, dlKids, dlTV        string
	kidsID, animeID         int64
}

func newFolderRig(t *testing.T) *folderRig {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &folderRig{base: t.TempDir()}
	for _, d := range []struct {
		p    *string
		name string
	}{
		{&r.movies, "media/movies"}, {&r.kids, "media/kids"}, {&r.tv, "media/tv"}, {&r.anime, "media/anime"},
		{&r.dl, "downloads"}, {&r.dlKids, "disk2/downloads"}, {&r.dlTV, "disk3/downloads"},
	} {
		*d.p = filepath.Join(r.base, filepath.FromSlash(d.name))
		if err := os.MkdirAll(*d.p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	set := &memSettings{m: map[string]string{}}
	r.roots = libroots.New(set.get, config.Config{MoviesDir: r.movies, TVDir: r.tv, DownloadsDir: r.dl})
	r.folders = libfolders.New(st.DB(), r.roots, filepath.Join(r.base, "data"))
	r.roots.WithExtras(r.folders.Extras)
	r.folders.SetSettingWriter(set.set)
	kids, err := r.folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaMovie, Path: r.kids, Label: "Kids"})
	if err != nil {
		t.Fatal(err)
	}
	anime, err := r.folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: r.anime, Label: "Anime"})
	if err != nil {
		t.Fatal(err)
	}
	r.kidsID, r.animeID = kids.ID, anime.ID
	// Kids movies download beside the Kids folder; the default TV folder has one of its
	// own; Movies and Anime have none.
	if _, err := r.folders.SetDownloads(ctx, libfolders.MediaMovie, kids.ID, r.dlKids); err != nil {
		t.Fatal(err)
	}
	if _, err := r.folders.SetDownloads(ctx, libfolders.MediaSeries, libfolders.DefaultID, r.dlTV); err != nil {
		t.Fatal(err)
	}

	mv := movies.NewService(st.DB(), nil, nil, r.movies, "", nil, log)
	mv.SetFolders(r.folders)
	sv := series.NewService(st.DB(), nil, r.tv, log)
	sv.SetFolders(r.folders)

	r.qb = &savePathQbit{}
	srv := httptest.NewServer(r.qb.handler())
	t.Cleanup(srv.Close)
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r.c = &Coordinator{db: st.DB(), log: log, bus: eventbus.New(log), downloads: dl, movies: mv, series: sv, indexers: indexer.NewService(st.DB(), log, nil),
		quality: quality.NewService(st.DB()), downloadsDir: "/startup/downloads"}
	r.c.SetDownloadsDirFunc(libroots.Func(r.roots.Downloads))
	r.c.SetFolderDownloadsFunc(r.roots.OwnDownloads)
	// Moana is a Kids film, Heat a default one; The Bear a default show, Frieren an Anime one.
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`INSERT INTO movies (id, tmdb_id, title, year, monitored, library_folder_id) VALUES (1, 101, 'Moana', 2016, 1, ?)`, kids.ID},
		{`INSERT INTO movies (id, tmdb_id, title, year, monitored, library_folder_id) VALUES (2, 102, 'Heat', 1995, 1, ?)`, libfolders.DefaultID},
		{`INSERT INTO series (id, tmdb_id, title, year, monitored, library_folder_id) VALUES (1, 201, 'The Bear', 2022, 1, ?)`, libfolders.DefaultID},
		{`INSERT INTO series (id, tmdb_id, title, year, monitored, library_folder_id) VALUES (2, 202, 'Frieren', 2023, 1, ?)`, anime.ID},
	} {
		if _, err := st.DB().ExecContext(ctx, q.sql, q.arg); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *folderRig) paths() []string {
	r.qb.mu.Lock()
	defer r.qb.mu.Unlock()
	return append([]string(nil), r.qb.paths...)
}

const tinyTorrent = "d8:announce0:e"

// A grab for a title in a folder with its own downloads folder saves there; one in a
// folder without falls back to the Downloads folder — for hand-uploaded torrents and the
// free-space check alike, films and shows alike.
func TestGrabSavesToTheFoldersDownloads(t *testing.T) {
	ctx := context.Background()
	r := newFolderRig(t)

	if err := r.c.GrabMovieTorrent(ctx, 1, 0, []byte(tinyTorrent), "a.torrent", "Moana.2016.1080p"); err != nil {
		t.Fatal(err)
	}
	if err := r.c.GrabMovieTorrent(ctx, 2, 0, []byte(tinyTorrent), "b.torrent", "Heat.1995.1080p"); err != nil {
		t.Fatal(err)
	}
	if err := r.c.GrabSeriesTorrent(ctx, 1, []byte(tinyTorrent), "c.torrent", "The.Bear.S01.1080p"); err != nil {
		t.Fatal(err)
	}
	if err := r.c.GrabSeriesTorrent(ctx, 2, []byte(tinyTorrent), "d.torrent", "Frieren.S01.1080p"); err != nil {
		t.Fatal(err)
	}
	want := []string{r.dlKids, r.dl, r.dlTV, r.dl}
	got := r.paths()
	if len(got) != len(want) {
		t.Fatalf("save paths = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("grab %d saved to %q, want %q", i, got[i], want[i])
		}
	}

	// The space check before an automatic grab measures the same folder.
	if p := r.c.savePath(r.c.forMovieID(ctx, 1)); p != r.dlKids {
		t.Errorf("Kids film's free-space folder = %q, want %q", p, r.dlKids)
	}
	// A grab nothing marked (books, music) keeps the Downloads folder.
	if p := r.c.savePath(ctx); p != r.dl {
		t.Errorf("unmarked grab = %q, want the Downloads folder", p)
	}
	// Clearing the folder's downloads folder sends the next grab back to Downloads.
	if _, err := r.folders.SetDownloads(ctx, libfolders.MediaMovie, r.kidsID, ""); err != nil {
		t.Fatal(err)
	}
	if p := r.c.savePath(r.c.forMovieID(ctx, 1)); p != r.dl {
		t.Errorf("after clearing = %q, want the Downloads folder", p)
	}
}

// A film moved to another folder (its file now lives there) downloads its upgrades
// beside its new folder, whatever it was assigned to before; a show's episodes decide
// for a show the same way.
func TestGrabFollowsTheFolderTheFilesAreIn(t *testing.T) {
	ctx := context.Background()
	r := newFolderRig(t)
	moved := filepath.Join(r.kids, "Heat (1995)", "Heat (1995).mkv")
	if _, err := r.c.db.ExecContext(ctx, `UPDATE movies SET has_file = 1, movie_file_path = ? WHERE id = 2`, moved); err != nil {
		t.Fatal(err)
	}
	if p := r.c.savePath(r.c.forMovieID(ctx, 2)); p != r.dlKids {
		t.Errorf("moved film saves to %q, want the Kids folder's downloads %q", p, r.dlKids)
	}
	// Frieren's episodes sit in the default TV folder: it downloads beside that.
	ep := filepath.Join(r.tv, "Frieren (2023)", "Season 01", "Frieren - S01E01.mkv")
	if _, err := r.c.db.ExecContext(ctx, `INSERT INTO episodes (series_id, season_number, episode_number, title, has_file, file_path) VALUES (2, 1, 1, 'Ep', 1, ?)`, ep); err != nil {
		t.Fatal(err)
	}
	if p := r.c.savePath(r.c.forSeriesID(ctx, 2)); p != r.dlTV {
		t.Errorf("show with files in the TV folder saves to %q, want %q", p, r.dlTV)
	}
}

// The free-space check before a grab measures where that release would actually land: a
// torrent for a Kids film the Kids downloads folder's disk, a usenet job for the same film
// the Downloads folder's.
func TestDiskCheckFollowsTheReleasesNetwork(t *testing.T) {
	ctx := context.Background()
	r := newFolderRig(t)
	if _, err := r.c.indexers.Create(ctx, indexer.Indexer{Name: "Usenet", Kind: indexer.KindNewznab, URL: "http://127.0.0.1:1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.indexers.Create(ctx, indexer.Indexer{Name: "Tracker", Kind: indexer.KindTorznab, URL: "http://127.0.0.1:1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// The Kids disk is full; the Downloads folder's has room.
	free := map[string]float64{r.dlKids: 1, r.dl: 500}
	r.c.freeGBFn = func(p string) (float64, bool) { v, ok := free[p]; return v, ok }
	kids := r.c.forMovieID(ctx, 1)
	if r.c.diskOKFor(kids, "Tracker", 10) {
		t.Error("a torrent for the Kids film passed the check on its full disk")
	}
	if !r.c.diskOKFor(kids, "Usenet", 10) {
		t.Error("a usenet job for the Kids film was judged by the Kids disk, where it never saves")
	}
	if got := r.c.grabSavePath(kids, "Usenet"); got != r.dl {
		t.Errorf("usenet grab path = %q, want %q", got, r.dl)
	}
}

// Usenet: SABnzbd has no folder per job, so an NZB always goes to the Downloads folder,
// even for a title whose folder has its own downloads folder (torrents only).
func TestGrabUsenetIgnoresFolderDownloads(t *testing.T) {
	ctx := context.Background()
	r := newFolderRig(t)
	marked := r.c.forMovieID(ctx, 1)
	if got := r.c.savePath(marked); got != r.dlKids {
		t.Fatalf("rig: a torrent for the Kids film saves to %q, want %q", got, r.dlKids)
	}
	// addNZB and addNZBLink send this: the Downloads folder whatever the grab is for.
	if got := r.c.usenetSavePath(marked); got != r.dl {
		t.Errorf("usenet save path = %q, want the Downloads folder %q", got, r.dl)
	}
}
