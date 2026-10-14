package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasFile reads one episode's has_file flag back from the database.
func (f *renameFixture) hasFile(t *testing.T, season, episode int) bool {
	t.Helper()
	s, err := f.c.series.Get(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if e.SeasonNumber == season && e.EpisodeNumber == episode {
				return e.HasFile
			}
		}
	}
	t.Fatalf("no S%02dE%02d", season, episode)
	return false
}

// placeAll gives each episode a file at its canonical path.
func (f *renameFixture) placeAll(t *testing.T, episodes ...[2]int) {
	t.Helper()
	for _, ep := range episodes {
		f.place(t, ep[0], ep[1], f.canonical(ep[0], ep[1]), "episode")
	}
}

func wantRefused(t *testing.T, err error, phrase string) {
	t.Helper()
	var refused *RescanRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a refused rescan", err)
	}
	if !strings.Contains(refused.Reason, phrase) || !strings.Contains(refused.Reason, "nothing was changed") {
		t.Errorf("reason = %q, want it to mention %q and that nothing was changed", refused.Reason, phrase)
	}
}

// An unmounted TV share makes every stored path fail to stat. The rescan used to clear
// every episode — and the searcher then downloaded the whole show again.
func TestRescanSeriesUnmountedRootChangesNothing(t *testing.T) {
	eps := [][2]int{{1, 1}, {1, 2}, {1, 3}}
	for name, unmount := range map[string]func(t *testing.T, root string){
		"missing": func(t *testing.T, root string) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
		},
		// The mount point is still there but empty, apart from a bin something wrote to it.
		"empty": func(t *testing.T, root string) {
			entries, _ := os.ReadDir(root)
			for _, e := range entries {
				if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(root, ".arrmada-recycle"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRenameFixture(t, eps...)
			f.placeAll(t, eps...)
			unmount(t, f.root)

			wantRefused(t, f.c.RescanSeries(f.ctx, f.id), "The TV folder isn't reachable")
			for _, ep := range eps {
				if !f.hasFile(t, ep[0], ep[1]) {
					t.Errorf("S%02dE%02d was marked missing while the TV folder was unmounted", ep[0], ep[1])
				}
			}
		})
	}
}

// With the share mounted, a file that's really gone is cleared — and only that one.
func TestRescanSeriesClearsOnlyTheMissingFile(t *testing.T) {
	eps := [][2]int{{1, 1}, {1, 2}, {1, 3}}
	f := newRenameFixture(t, eps...)
	f.placeAll(t, eps...)
	if err := os.Remove(f.canonical(1, 2)); err != nil {
		t.Fatal(err)
	}

	if err := f.c.RescanSeries(f.ctx, f.id); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if f.hasFile(t, 1, 2) {
		t.Error("S01E02's file is gone, so the episode should read as missing")
	}
	for _, ep := range [][2]int{{1, 1}, {1, 3}} {
		if !f.hasFile(t, ep[0], ep[1]) {
			t.Errorf("S%02dE%02d's file is still there but it was marked missing", ep[0], ep[1])
		}
	}
}

// The TV folder is fine but this show's folder is gone — one disk of a split share being
// down looks exactly like this. Nothing is cleared.
func TestRescanSeriesMissingShowFolderChangesNothing(t *testing.T) {
	eps := [][2]int{{1, 1}, {1, 2}}
	f := newRenameFixture(t, eps...)
	f.placeAll(t, eps...)
	if err := os.MkdirAll(filepath.Join(f.root, "Another Show (2019)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.root, f.folder)); err != nil {
		t.Fatal(err)
	}

	wantRefused(t, f.c.RescanSeries(f.ctx, f.id), "The folder for Show isn't reachable")
	for _, ep := range eps {
		if !f.hasFile(t, ep[0], ep[1]) {
			t.Errorf("S%02dE%02d was marked missing while its show folder was gone", ep[0], ep[1])
		}
	}
}

// Every recorded file vanishing at once, with the show folder still holding something
// else (artwork, an empty season folder), is refused too.
func TestRescanSeriesWholeShowVanishingChangesNothing(t *testing.T) {
	eps := [][2]int{{1, 1}, {1, 2}}
	f := newRenameFixture(t, eps...)
	f.placeAll(t, eps...)
	for _, ep := range eps {
		if err := os.Remove(f.canonical(ep[0], ep[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.root, f.folder, "poster.jpg"), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}

	wantRefused(t, f.c.RescanSeries(f.ctx, f.id), "None of the 2 files for Show")
	for _, ep := range eps {
		if !f.hasFile(t, ep[0], ep[1]) {
			t.Errorf("S%02dE%02d was marked missing when the whole show vanished at once", ep[0], ep[1])
		}
	}
}

// A show with nothing on disk yet has nothing to clear, so an empty TV folder (a fresh
// install) isn't an error.
func TestRescanSeriesNothingRecordedIsFine(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1})
	if err := f.c.RescanSeries(f.ctx, f.id); err != nil {
		t.Fatalf("rescan of a show with no files: %v", err)
	}
}
