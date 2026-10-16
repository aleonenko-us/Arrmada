package libroots

import (
	"context"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
)

// fakeSettings is an in-memory settings store that, like settings.Get, hands back a
// stored "" as-is rather than the default.
type fakeSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (f *fakeSettings) get(_ context.Context, key, def string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.m[key]; ok {
		return v
	}
	return def
}

func (f *fakeSettings) set(key, v string) {
	f.mu.Lock()
	f.m[key] = v
	f.mu.Unlock()
}

var envCfg = config.Config{
	MoviesDir: "/env/movies", TVDir: "/env/tv", EbooksDir: "/env/ebooks",
	AudiobooksDir: "/env/audiobooks", MusicDir: "/env/music", DownloadsDir: "/env/downloads",
}

// A saved folder wins; a blank or whitespace-only save means "the install default".
func TestRootsFallbackOnEmptySaved(t *testing.T) {
	s := &fakeSettings{m: map[string]string{KeyMovies: "/storage/movies", KeyTV: "", KeyDownloads: "   "}}
	r := New(s.get, envCfg)
	ctx := context.Background()
	if got := r.Movies(ctx); got != "/storage/movies" {
		t.Errorf("movies = %q, want the saved folder", got)
	}
	if got := r.TV(ctx); got != "/env/tv" {
		t.Errorf("tv saved as \"\" = %q, want the environment's", got)
	}
	if got := r.Downloads(ctx); got != "/env/downloads" {
		t.Errorf("downloads saved as spaces = %q, want the environment's", got)
	}
	if got := r.Music(ctx); got != "/env/music" {
		t.Errorf("music unset = %q, want the environment's", got)
	}
	// Live: a change is seen on the next read, no rebuild.
	s.set(KeyTV, " /storage/tv ")
	if got := r.TV(ctx); got != "/storage/tv" {
		t.Errorf("tv after a save = %q, want /storage/tv (trimmed)", got)
	}
	c := r.Config(ctx)
	if c.MoviesDir != "/storage/movies" || c.TVDir != "/storage/tv" || c.DownloadsDir != "/env/downloads" {
		t.Errorf("Config = %+v", c)
	}
	all := r.All(ctx)
	if len(all) != 6 || all[5].Name != "downloads" || all[0] != (Root{Name: "movies", Path: "/storage/movies"}) {
		t.Errorf("All = %+v", all)
	}
	if libs := r.Libraries(ctx); len(libs) != 5 {
		t.Errorf("Libraries = %+v, want five without downloads", libs)
	}
}

// A root that resolves to nothing is left out of the lists rather than listed as "".
func TestRootsSkipEmpty(t *testing.T) {
	r := New(nil, config.Config{MoviesDir: "/m"})
	all := r.All(context.Background())
	if len(all) != 1 || all[0].Path != "/m" {
		t.Errorf("All = %+v, want only movies", all)
	}
	var nilRoots *Roots
	if got := nilRoots.Movies(context.Background()); got != "" {
		t.Errorf("nil resolver = %q, want empty", got)
	}
}

// Reads race with saves without a lock in the resolver: settings does its own locking.
func TestRootsConcurrentReads(t *testing.T) {
	s := &fakeSettings{m: map[string]string{}}
	r := New(s.get, envCfg)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i%2 == 0 {
					s.set(KeyTV, "/storage/tv")
				}
				_ = Func(r.TV)()
				_ = r.All(context.Background())
			}
		}(i)
	}
	wg.Wait()
}

// Extra Movies and TV folders join the library lists after the five defaults, each under
// its library's name with its own label, and the per-library lists put the default first.
// Without a source of extras nothing changes.
func TestRootsExtras(t *testing.T) {
	r := New(nil, envCfg)
	ctx := context.Background()
	if got := r.MovieFolders(ctx); len(got) != 1 || got[0] != "/env/movies" {
		t.Errorf("MovieFolders without extras = %v", got)
	}
	r.WithExtras(func(context.Context) []Extra {
		return []Extra{
			{Media: MediaMovie, ID: 1, Path: "/storage/kids", Label: "Kids"},
			{Media: MediaSeries, ID: 2, Path: "/storage/anime", Label: "Anime"},
			{Media: MediaMovie, ID: 3, Path: "  "}, // resolves to nothing: left out
		}
	})
	libs := r.Libraries(ctx)
	if len(libs) != 7 || libs[5] != (Root{Name: "movies", Path: "/storage/kids", Label: "Kids"}) || libs[6].Name != "tv" {
		t.Errorf("Libraries = %+v", libs)
	}
	if all := r.All(ctx); all[len(all)-1].Name != "downloads" {
		t.Errorf("All = %+v, want downloads last", all)
	}
	if got := r.MovieFolders(ctx); len(got) != 2 || got[0] != "/env/movies" || got[1] != "/storage/kids" {
		t.Errorf("MovieFolders = %v", got)
	}
	if got := r.TVFolders(ctx); len(got) != 2 || got[1] != "/storage/anime" {
		t.Errorf("TVFolders = %v", got)
	}
	// The default folders themselves are untouched by extras.
	if r.Movies(ctx) != "/env/movies" || r.Config(ctx).MoviesDir != "/env/movies" {
		t.Error("an extra folder changed the default")
	}
}
