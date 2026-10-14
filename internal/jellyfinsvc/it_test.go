package jellyfinsvc

// Live tests against a real Jellyfin, run by scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)
// with the rig's synthetic library. Every test name contains "IT" so the script selects
// them with -run IT. The rig mounts its media volume at /media (where Jellyfin sees it)
// and again at ARRMADA_JELLYFIN_ARR (/arr), which plays Arrmada's own spelling of the same
// folders, so path mappings are exercised for real.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

type itRig struct {
	url, key, media, arr string
}

func liveRig(t *testing.T) itRig {
	t.Helper()
	if os.Getenv("ARRMADA_JELLYFIN_IT") != "1" {
		t.Skip("live Jellyfin test: run scripts/jellyfin-it.sh (ARRMADA_JELLYFIN_IT=1)")
	}
	r := itRig{
		url: os.Getenv("ARRMADA_JELLYFIN_URL"), key: os.Getenv("ARRMADA_JELLYFIN_KEY"),
		media: os.Getenv("ARRMADA_JELLYFIN_MEDIA"), arr: os.Getenv("ARRMADA_JELLYFIN_ARR"),
	}
	if r.url == "" || r.key == "" || r.media == "" {
		t.Fatal("ARRMADA_JELLYFIN_URL, _KEY and _MEDIA must be set")
	}
	return r
}

// liveService is a connected service over in-memory settings, with Arrmada's folders at
// root (movies, TV).
func liveService(t *testing.T, r itRig, movies, tv string) (*Service, *memSettings) {
	t.Helper()
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	set := &memSettings{m: map[string]string{}}
	s := New(Options{
		Settings: set, Keys: apikeys.NewStore(set), Version: "0.1-it", Log: quiet(),
		Roots: func(context.Context) map[string]string {
			return map[string]string{plexscan.KindMovie: movies, plexscan.KindShow: tv}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.Save(ctx, r.url, "https://jellyfin.example.com", r.key); err != nil {
		t.Fatal(err)
	}
	if h := s.Health(ctx); h.LastOKAt.IsZero() || s.Config(ctx).ServerID == "" {
		t.Fatalf("save didn't read the identity: %+v %+v", h, s.Config(ctx))
	}
	return s, set
}

// The index finds every synthetic title by TMDB, TVDB and IMDb id, and the ungrouped
// multi-version folders (no ids at all) by their folder — as Jellyfin spells it, through a
// path mapping, and by a guessed library folder name.
func TestIT_SvcIndexLocates(t *testing.T) {
	r := liveRig(t)
	s, _ := liveService(t, r, r.media+"/Movies", r.media+"/TV")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	if err := s.idx.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	idx := s.idx.current()
	t.Logf("index: %d movies, %d shows in %s", idx.movies, idx.shows, time.Since(start).Round(time.Millisecond))

	find := func(media string, ids mediaserver.ExternalIDs) mediaserver.Item {
		t.Helper()
		it, ok := s.Locate(ctx, media, ids)
		if !ok {
			t.Errorf("%s %+v not found", media, ids)
		}
		return it
	}
	for tmdb, title := range map[int]string{603: "The Matrix", 27205: "Inception", 348: "Alien", 578: "Jaws", 577922: "Tenet", 49047: "Gravity"} {
		if it := find("movie", mediaserver.ExternalIDs{TMDB: tmdb}); it.ID != "" && it.IDs.TMDB != tmdb {
			t.Errorf("%s: %+v", title, it)
		}
	}
	for _, imdb := range []string{"tt0133093", "tt1375666", "tt6723592"} {
		find("movie", mediaserver.ExternalIDs{IMDB: imdb})
	}
	bb := find("series", mediaserver.ExternalIDs{TVDB: 81189})
	if bb.ID == "" || find("series", mediaserver.ExternalIDs{TMDB: 1396}).ID != bb.ID || find("series", mediaserver.ExternalIDs{IMDB: "tt0903747"}).ID != bb.ID {
		t.Errorf("Breaking Bad by TVDB/TMDB/IMDb disagree")
	}
	if _, ok := s.Locate(ctx, "movie", mediaserver.ExternalIDs{TMDB: 438631}); ok {
		t.Log("note: Dune now carries its TMDB id (Jellyfin groups its files?)")
	}

	// By folder: Dune, Heat, Arrival and Memento have no ids in Jellyfin.
	for _, folder := range []string{"Dune (2021) {tmdb-438631}", "Heat (1995) {tmdb-949}", "Arrival (2016)", "Memento (2000)"} {
		find("movie", mediaserver.ExternalIDs{Folder: r.media + "/Movies/" + folder})
	}
	find("series", mediaserver.ExternalIDs{Folder: r.media + "/TV/Breaking Bad (2008) {tvdb-81189}"})
	if r.arr != "" {
		// Arrmada's /arr/Movies is Jellyfin's /media/Movies: guessed from the folder name…
		s.roots = func(context.Context) map[string]string {
			return map[string]string{plexscan.KindMovie: r.arr + "/Movies", plexscan.KindShow: r.arr + "/TV"}
		}
		find("movie", mediaserver.ExternalIDs{Folder: r.arr + "/Movies/Dune (2021) {tmdb-438631}"})
		// …or mapped, when the names don't match.
		s.roots = func(context.Context) map[string]string { return map[string]string{plexscan.KindMovie: r.arr} }
		s.pathMaps = func(context.Context) []plexscan.PathMap { return []plexscan.PathMap{{From: r.arr, To: r.media}} }
		find("movie", mediaserver.ExternalIDs{Folder: r.arr + "/Movies/Heat (1995) {tmdb-949}"})
	}

	// Watch links: the public address, the item, the server.
	matrix := find("movie", mediaserver.ExternalIDs{TMDB: 603})
	want := jellyfin.WatchURL("https://jellyfin.example.com", s.Config(ctx).ServerID, matrix.ID)
	if got := s.WatchURL(ctx, "movie", mediaserver.ExternalIDs{TMDB: 603}); got != want || got == "" {
		t.Errorf("watch url = %q, want %q", got, want)
	}

	recent, err := s.RecentlyAddedTMDB(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, x := range recent {
		seen[fmt.Sprintf("%s:%d", x.Media, x.TMDB)] = true
	}
	if !seen["series:1396"] || !seen["movie:603"] {
		t.Errorf("recently added = %+v", recent)
	}
}

// A newly imported folder, reported through the scanner with a path mapping (Arrmada's
// /arr is Jellyfin's /media), becomes locatable once Jellyfin's wait is over; an Arrmada
// recycle bin and a hidden-folder marker in the same report never show up.
func TestIT_SvcScanNewFolder(t *testing.T) {
	r := liveRig(t)
	if r.arr == "" {
		t.Skip("ARRMADA_JELLYFIN_ARR not set: the rig's second mount isn't here")
	}
	s, set := liveService(t, r, r.arr+"/Movies", r.arr+"/TV")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sc := s.NewScanner(set, s.roots, quiet())
	if err := sc.Save(ctx, true, []plexscan.PathMap{{From: r.arr + "/Movies", To: r.media + "/Movies"}}); err != nil {
		t.Fatal(err)
	}
	rv, err := sc.ScanNow(ctx, plexscan.KindMovie, false)
	if err != nil || rv.How != plexscan.HowMapped || rv.PlexPath != r.media+"/Movies" {
		t.Fatalf("resolve = %+v %v", rv, err)
	}
	go sc.Run(ctx)
	go s.RunIndex(ctx)

	base := 910_000_000 + int(time.Now().Unix()%1_000_000)*10
	visible := writeMovie(t, r.arr+"/Movies", "Arrival Five", base)
	// A recycle bin made the way Arrmada makes it, with a deleted film in it…
	bins := &library.RootBins{Roots: func() []libroots.Root { return []libroots.Root{{Name: "movies", Path: r.arr + "/Movies"}} }}
	bin, err := bins.For(filepath.Join(r.arr, "Movies", "x", "x.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	binned := writeMovie(t, bin, "Binned Six", base+1)
	// …and a movie folder hidden by Arrmada's marker files alone (no dot in its name).
	held := filepath.Join(r.arr, "Movies", fmt.Sprintf("Held Seven (2016) {tmdb-%d}", base+2))
	if err := os.Mkdir(held, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := library.PrepareHiddenDir(held); err != nil {
		t.Fatal(err)
	}
	writeMovieIn(t, held, "Held Seven", base+2)

	start := time.Now()
	sc.Request(plexscan.KindMovie, visible)
	sc.Request(plexscan.KindMovie, binned)
	sc.Request(plexscan.KindMovie, held)
	for {
		if _, ok := s.Locate(ctx, "movie", mediaserver.ExternalIDs{TMDB: base}); ok {
			break
		}
		if time.Since(start) > 8*time.Minute {
			t.Fatalf("the new movie wasn't locatable %s after the report (last scan %+v)", time.Since(start).Round(time.Second), sc.LastScan())
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("new folder locatable %s after the request (scanner debounce + Jellyfin's wait + index settle)", time.Since(start).Round(time.Second))
	if st := sc.LastScan(); st.Error != "" || st.How != plexscan.HowMapped {
		t.Errorf("last scan %+v", st)
	}
	if _, ok := s.Locate(ctx, "movie", mediaserver.ExternalIDs{Folder: visible}); !ok {
		t.Error("the new movie isn't found by its Arrmada folder")
	}
	// The hidden ones: not in Jellyfin even after a scan of every library.
	c := s.Client(ctx)
	if err := c.RefreshLibrary(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	for i := 0; ; i++ {
		vfs, err := c.VirtualFolders(ctx)
		if err != nil {
			t.Fatal(err)
		}
		busy := false
		for _, vf := range vfs {
			busy = busy || vf.RefreshStatus == "Active"
		}
		if !busy {
			break
		}
		if i > 120 {
			t.Fatal("the full scan didn't finish")
		}
		time.Sleep(2 * time.Second)
	}
	items, err := c.AllItems(ctx, jellyfin.ItemsQuery{IncludeItemTypes: []string{jellyfin.TypeMovie}, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ProviderIDs.TMDB == base+1 || it.ProviderIDs.TMDB == base+2 {
			t.Errorf("a hidden folder's film is in Jellyfin: %s (%s)", it.Name, it.Path)
		}
	}
}

// writeMovie puts a single-file movie with an NFO in parent/<title> (2016) {tmdb-N} and
// returns the folder.
func writeMovie(t *testing.T, parent, title string, tmdb int) string {
	t.Helper()
	dir := filepath.Join(parent, fmt.Sprintf("%s (2016) {tmdb-%d}", title, tmdb))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMovieIn(t, dir, title, tmdb)
	return dir
}

// writeMovieIn writes the movie's file and NFO into dir. The clip is the rig's own
// synthetic Matrix file.
func writeMovieIn(t *testing.T, dir, title string, tmdb int) {
	t.Helper()
	r := liveRig(t)
	src, err := os.ReadFile(filepath.Join(r.media, "Movies", "The Matrix (1999) {tmdb-603}", "The Matrix (1999).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, title+" (2016).mkv"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	nfo := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<movie><title>%s</title><year>2016</year><uniqueid type="tmdb" default="true">%d</uniqueid></movie>
`, title, tmdb)
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
}
