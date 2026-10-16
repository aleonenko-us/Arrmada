package automation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// A Radarr import adds films that already have their files. Neither a sweep that runs while
// one is being added nor the import stopping half-way may leave such a film wanted: the
// sweep would download a film the person has.

type heatMeta struct{}

func (heatMeta) Available() bool { return true }
func (heatMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (heatMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	return &metadata.MovieDetails{MovieResult: metadata.MovieResult{TMDBID: id, Title: "Heat", Year: 1995, ReleaseDate: "1995-12-15"}, Status: "Released"}, nil
}
func (heatMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, metadata.ErrNotFound
}

// importHarness is the stall harness with a real movies service over a library folder that
// holds Heat's file, and a release of Heat on offer.
func importHarness(t *testing.T) (*stallHarness, *movies.Service, string) {
	t.Helper()
	h := newStallHarness(t)
	root := t.TempDir()
	svc := movies.NewService(h.c.db, heatMeta{}, nil, root, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.c.movies = svc
	file := filepath.Join(root, "Heat (1995)", "Heat.1995.1080p.BluRay.x264-OLD.mkv")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ix.offer("Heat.1995.1080p.BluRay.x264-GRP")
	return h, svc, file
}

// The movie sweep runs while AddImported reads the film's file (after the film exists,
// before its file is recorded): it must not search it.
func TestSweepDuringImportedFilmAdd(t *testing.T) {
	h, svc, file := importHarness(t)
	swept := false
	svc.SetFileAccess(func(string) (mediainfo.Info, error) {
		if !swept {
			swept = true
			h.c.SearchMissing(h.ctx) // the 5-minute sweep fires while ffprobe runs
		}
		return mediainfo.Info{}, nil
	}, nil)
	m, err := svc.AddImported(h.ctx, 949, movies.AddOptions{Monitored: true, MinAvailability: "released"}, file, "Radarr")
	if err != nil {
		t.Fatal(err)
	}
	if !swept {
		t.Fatal("setup: the file was never read, so the sweep never ran mid-add")
	}
	if calls := h.qbit.callLog(); len(calls) > 0 || h.ix.searchCount() > 0 {
		t.Fatalf("a sweep during the import searched (%d) and grabbed (%v) a film imported with its file", h.ix.searchCount(), calls)
	}
	if !m.Monitored || !m.HasFile {
		t.Errorf("film = monitored %v, has file %v; want both once the add finished", m.Monitored, m.HasFile)
	}
}

// The import is cancelled (or times out) while AddImported reads the file: the film is still
// finished — its file recorded — and the next sweep leaves it alone.
func TestCancelDuringImportedFilmAdd(t *testing.T) {
	h, svc, file := importHarness(t)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	svc.SetFileAccess(func(string) (mediainfo.Info, error) { cancel(); return mediainfo.Info{}, nil }, nil)
	m, err := svc.AddImported(ctx, 949, movies.AddOptions{Monitored: true, MinAvailability: "released"}, file, "Radarr")
	if err != nil {
		t.Fatal(err)
	}
	var mon, hasFile int
	if err := h.c.db.QueryRow(`SELECT monitored, has_file FROM movies WHERE id = ?`, m.ID).Scan(&mon, &hasFile); err != nil {
		t.Fatal(err)
	}
	if mon == 1 && hasFile == 0 {
		t.Fatal("the cancelled import left the film monitored without its file")
	}
	h.c.SearchMissing(h.ctx)
	if calls := h.qbit.callLog(); len(calls) > 0 {
		t.Fatalf("the sweep after a cancelled import grabbed %v", calls)
	}
}

// A wanted film (no file) is marked to be searched in its turn before it's monitored, so no
// sweep can catch it monitored and unmarked.
func TestImportedWantedFilmIsPacedBeforeMonitored(t *testing.T) {
	h, svc, _ := importHarness(t)
	m, err := svc.AddImported(h.ctx, 949, movies.AddOptions{Monitored: true, MinAvailability: "released", SearchPaced: true}, "", "Radarr")
	if err != nil {
		t.Fatal(err)
	}
	marked, err := svc.PacedSearchIDs(h.ctx)
	if err != nil || !marked[m.ID] || !m.Monitored {
		t.Fatalf("wanted film: monitored %v, marked %v (%v)", m.Monitored, marked[m.ID], err)
	}
}
