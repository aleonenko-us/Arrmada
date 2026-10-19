package arrimport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// watchSeries is the real series service that, on each step of setting a show up, notes
// whether the show was monitored at that moment — and can fail or cancel a step.
type watchSeries struct {
	*series.Service
	mu       sync.Mutex
	seen     []bool // the show's monitored gate at each step
	failOn   string // an episode file name whose attach fails
	onAttach func()
}

func (w *watchSeries) note(ctx context.Context, id int64) {
	sr, err := w.Service.GetSummary(ctx, id)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, err == nil && sr.Monitored)
}

func (w *watchSeries) AdoptEpisodeFile(ctx context.Context, id int64, season, episode int, path string, size int64) (bool, error) {
	w.note(ctx, id)
	if w.onAttach != nil {
		w.onAttach()
	}
	if w.failOn != "" && strings.Contains(path, w.failOn) {
		return false, errors.New("disk said no")
	}
	return w.Service.AdoptEpisodeFile(ctx, id, season, episode, path, size)
}

func (w *watchSeries) SetEpisodesMonitored(ctx context.Context, id int64, ids []int64, on bool) (int, error) {
	w.note(ctx, id)
	return w.Service.SetEpisodesMonitored(ctx, id, ids, on)
}

func (w *watchSeries) SetSearchPaced(ctx context.Context, id int64, on bool) error {
	w.note(ctx, id)
	return w.Service.SetSearchPaced(ctx, id, on)
}

func (w *watchSeries) monitoredDuringSetup() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.seen {
		if m {
			return true
		}
	}
	return false
}

func sonarrImport(t *testing.T, w *world) (*Client, *Snapshot) {
	t.Helper()
	w.file(t, bbE1)
	w.file(t, bbE2)
	w.file(t, frE1)
	c := liveClient(t, Sonarr)
	snap, err := Load(w.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return c, snap
}

// A new show stays paused (its monitored gate off) while its episodes' monitoring is copied,
// its files recorded and its paced mark set — no sweep can find it wanting episodes the
// person has — and is monitored once all of that went through.
func TestImportedShowPausedUntilComplete(t *testing.T) {
	w := newWorld(t)
	watch := &watchSeries{Service: w.series}
	w.imp.Series = watch
	c, snap := sonarrImport(t, w)
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(watch.seen) == 0 {
		t.Fatal("setup: no step was watched")
	}
	if watch.monitoredDuringSetup() {
		t.Fatalf("a show was monitored while it was being set up: %v", watch.seen)
	}
	bb := showByTMDB(t, w, 1396)
	marked, _ := w.series.PacedSearchIDs(w.ctx)
	if !bb.Monitored || !marked[bb.ID] || res.Paced != 2 {
		t.Errorf("finished show: monitored %v, marked %v, paced %d", bb.Monitored, marked[bb.ID], res.Paced)
	}
}

// An episode file that can't be recorded leaves the show paused, and the summary says so:
// otherwise the sweep would download the episodes whose files are on disk.
func TestShowAttachFailureLeavesItUnmonitored(t *testing.T) {
	w := newWorld(t)
	w.imp.Series = &watchSeries{Service: w.series, failOn: "S01E02"}
	c, snap := sonarrImport(t, w)
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bb := showByTMDB(t, w, 1396)
	if bb.Monitored {
		t.Fatal("a show whose files couldn't all be recorded was left monitored")
	}
	found := false
	for _, n := range res.Notes {
		if n.Title == "Breaking Bad" && strings.Contains(n.Reason, "left with searching off") && strings.Contains(n.Reason, "disk said no") {
			found = true
		}
	}
	if !found || res.Paced != 1 {
		t.Errorf("notes = %+v, paced = %d; want Breaking Bad named as left with searching off, only Frieren paced", res.Notes, res.Paced)
	}
}

// The import stopping (a cancel, the time limit) part-way through a show still finishes that
// show: every file recorded, then monitored. The next show isn't started.
func TestCancelDuringShowAttach(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(w.ctx)
	defer cancel()
	w.imp.Series = &watchSeries{Service: w.series, onAttach: cancel}
	c, snap := sonarrImport(t, w)
	_, err := w.imp.Run(ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err == nil || !strings.Contains(err.Error(), "stopped after 1 of 2") {
		t.Fatalf("cancelled run: %v", err)
	}
	bb := showByTMDB(t, w, 1396)
	e1, _ := episode(bb, 1, 1)
	e2, _ := episode(bb, 1, 2)
	if !bb.Monitored || !e1.HasFile || !e2.HasFile {
		t.Errorf("show cut off mid-import: monitored %v, S01E01 file %v, S01E02 file %v", bb.Monitored, e1.HasFile, e2.HasFile)
	}
}

// Where Arrmada numbers a season differently from Sonarr (its listing isn't TVDB's and the
// episode counts differ), Sonarr's per-episode choices aren't copied onto the wrong
// episodes: the season choice stands, files go by their names, and the summary says why.
func TestImportShowNumberedDifferently(t *testing.T) {
	w := newWorld(t)
	w.meta.source, w.meta.short = "tvmaze", map[int]bool{1: true}
	c, snap := sonarrImport(t, w)
	res, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bb := showByTMDB(t, w, 1396)
	if e3, _ := episode(bb, 1, 3); !e3.Monitored {
		t.Errorf("S01E03 = %+v: Sonarr's per-episode choice was copied onto a season numbered differently", e3)
	}
	if e1, _ := episode(bb, 1, 1); !e1.HasFile {
		t.Errorf("S01E01 = %+v: files should still go by their names", e1)
	}
	found := false
	for _, n := range res.Notes {
		if n.Title == "Breaking Bad" && strings.Contains(n.Reason, "numbers season 1 differently") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %+v, want the numbering difference named", res.Notes)
	}
	// Frieren's seasons have the same counts, so its per-episode choices are copied.
	if fr := showByTMDB(t, w, 209867); fr.SeriesType != series.SeriesTypeAnime {
		t.Errorf("frieren = %+v", fr)
	}
}

// Specials are never monitored by the per-episode copy, and an episode that has a file is
// never given another one.
func TestEpisodeCopySkipsSpecialsAndKeepsFiles(t *testing.T) {
	w := newWorld(t)
	c, snap := sonarrImport(t, w)
	if _, err := w.imp.Run(w.ctx, c, snap, Options{PathMaps: w.remap()}, nil); err != nil {
		t.Fatal(err)
	}
	bb := showByTMDB(t, w, 1396)
	if err := w.imp.copyEpisodeMonitoring(w.ctx, bb, []Episode{{SeasonNumber: 0, EpisodeNumber: 1, Monitored: true}}, map[int]bool{0: true}); err != nil {
		t.Fatal(err)
	}
	bb = showByTMDB(t, w, 1396)
	if sp, ok := episode(bb, 0, 1); !ok || sp.Monitored {
		t.Errorf("special = %+v (found %v), want it left unmonitored", sp, ok)
	}
	e1, _ := episode(bb, 1, 1)
	ok, err := w.series.AdoptEpisodeFile(w.ctx, bb.ID, 1, 1, "/elsewhere/other.mkv", 1)
	if err != nil || ok {
		t.Fatalf("adopting onto an episode with a file = %v, %v; want refused", ok, err)
	}
	if again, _ := episode(showByTMDB(t, w, 1396), 1, 1); again.FilePath != e1.FilePath {
		t.Errorf("S01E01's file was replaced: %q → %q", e1.FilePath, again.FilePath)
	}
}

// Radarr saying a film has a file without saying where is a file that can't be found: never
// a film to download.
func TestRadarrFileWithoutPath(t *testing.T) {
	w := newWorld(t)
	snap := &Snapshot{App: Radarr, Status: Status{AppName: "Radarr"}, Movies: []Movie{{
		ID: 1, Title: "Heat", Year: 1995, TMDBID: 949, Monitored: true, MinimumAvailability: "released",
		Path: "/media/movies/Heat (1995)", RootFolderPath: "/media/movies", HasFile: true,
	}}}
	pv, err := w.imp.Preview(w.ctx, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Counts.NotFound != 1 || pv.Counts.Wanted != 0 || len(pv.NotFound) != 1 || pv.NotFound[0].Reason != noFilePath {
		t.Fatalf("preview = %+v %+v", pv.Counts, pv.NotFound)
	}
	res, err := w.imp.Run(w.ctx, nil, snap, Options{PathMaps: w.remap()}, nil)
	if err != nil || res.Skipped != 1 || res.Imported != 0 {
		t.Fatalf("run = %+v, %v; want it skipped", res, err)
	}
	res, err = w.imp.Run(w.ctx, nil, snap, Options{PathMaps: w.remap(), ImportMissing: true}, nil)
	if err != nil || res.Imported != 1 || res.Paced != 1 {
		t.Fatalf("import-missing run = %+v, %v", res, err)
	}
}
