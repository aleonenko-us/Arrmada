package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// Second review of the #56 fix, layouts j, k, p, o and r: what every consumer of a show's
// folder does — import, rescan, duplicates, rename, delete — is what v0.3.1 did, or a
// refusal.

type rbFile struct {
	rel     string
	tracked [2]int // {0,0}: on disk, not tracked
	missing bool   // tracked but not on disk
}

func rbSetup(t *testing.T, files []rbFile) (*lifecycleHarness, string) {
	t.Helper()
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	for _, f := range files {
		p := filepath.Join(tv, filepath.FromSlash(f.rel))
		if !f.missing {
			sparseFile(t, p, 60<<20)
		}
		if f.tracked != [2]int{} {
			rvSet(t, h, f.tracked[0], f.tracked[1], p)
		}
	}
	return h, tv
}

func rbExists(t *testing.T, tv string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(tv, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is gone: %v\n    tree: %s", rel, err, rvTree(tv))
		}
	}
}

// j, k: a season-named folder (S01) inside a category or franchise folder that also holds
// a show Arrmada doesn't track. v0.3.1 read the show's folder as <TV>/S01 (and its rescan
// found nothing); the folder above S01 holds another show, so nothing may treat it as this
// show's — every action is refused, and the other show is never touched.
func TestRevBSeasonFolderInsideACategory(t *testing.T) {
	for _, c := range []struct {
		name      string
		tracked   string
		untracked []string
	}{
		{"j-category", "Kids/S01/Show - S01E01.mkv",
			[]string{"Kids/Bluey/Season 1/Bluey - S01E01 - 2160p.mkv", "Kids/Bluey/Season 1/Bluey - S01E02 - 2160p.mkv"}},
		{"k-franchise", "Star Trek/S01/Show - S01E01.mkv",
			[]string{"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E01 - 2160p.mkv",
				"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := []rbFile{{rel: c.tracked, tracked: [2]int{1, 1}}}
			for _, u := range c.untracked {
				files = append(files, rbFile{rel: u})
			}
			own := filepath.FromSlash(c.tracked)
			category := strings.SplitN(c.tracked, "/", 2)[0]

			h, tv := rbSetup(t, files)
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("placement = %v, want refused", err)
			}
			if p := h.c.series.FolderPath(h.ctx, h.seriesID); p != "" {
				t.Errorf("FolderPath = %q, want none", p)
			}
			// Rescan (refresh-all runs it unattended): refused, nothing re-pointed.
			var refused *RescanRefusedError
			if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.As(err, &refused) {
				t.Errorf("rescan = %v, want refused", err)
			}
			for _, ep := range []int{1, 2} {
				cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, ep)
				if ep == 1 && cur.Path != filepath.Join(tv, own) || ep == 2 && cur.Path != "" {
					t.Errorf("after rescan S01E%02d = %q", ep, cur.Path)
				}
			}
			if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("duplicates = %v, want refused", err)
			}
			if _, err := h.c.SeriesRenamePreview(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("rename preview = %v, want refused", err)
			}
			if _, err := h.c.SeriesRename(h.ctx, h.seriesID, nil); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("rename = %v, want refused", err)
			}
			// Import: nothing placed, nothing made in the category.
			const name = "Show.S01E03.1080p.WEB-DL-GRP"
			mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, 1, 3, '2020-01-15', 1)`, h.seriesID)
			dl := h.download(t, name, name+".mkv")
			s, _ = h.c.series.Get(h.ctx, h.seriesID)
			if placed, _, _, _ := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 0 {
				t.Errorf("import placed %v", placed)
			}
			noDir(t, filepath.Join(tv, category, "Season 1"), "an import made a season folder in the category")
			// Delete with files: only the show's own file goes; the other show stays.
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, c.untracked...)
		})
	}
}

// p: the show's only recorded file was a #56 leftover the owner has since moved back to the
// show's real folder. It isn't a new show: everything that needs the folder is refused,
// rather than making a new <TV>/Show beside the real one.
func TestRevBAllLeftoversGone(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "Real Folder/S01/Show - S01E01.mkv"},
	})
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
		t.Fatalf("placement = %v, want refused", err)
	}
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	if placed, _, _, _ := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 0 {
		t.Errorf("import placed %v", placed)
	}
	noDir(t, filepath.Join(tv, "Show"), "a new folder was made for a show that has one")
	rbExists(t, tv, "Real Folder/S01/Show - S01E01.mkv")
}

// o: an existing "Season.01" folder is joined, not doubled by a "Season 1" beside it (the
// one place the import's season folder differs from v0.3.1's).
func TestRevBJoinsSeasonDot01(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{{rel: "Show/Season.01/Show - S01E01.mkv", tracked: [2]int{1, 1}}})
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 1 || failed != 0 {
		t.Fatalf("placed %v failed %d", placed, failed)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(cur.Path) != filepath.Join(tv, "Show", "Season.01") {
		t.Errorf("S01E02 at %s, want in Show/Season.01", cur.Path)
	}
	noDir(t, filepath.Join(tv, "Show", "Season 1"), "a parallel Season 1 was made")
}

// r: a bonus folder named like a season ("Series 1 (Behind the scenes)") is never joined by
// an import: as v0.3.1, season 1 goes into "Season 1".
func TestRevBNeverJoinsATaggedSeasonFolder(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Show/Season 2/Show - S02E01.mkv", tracked: [2]int{2, 1}},
		{rel: "Show/Series 1 (Behind the scenes)/bonus.mkv"},
	})
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 1 || failed != 0 {
		t.Fatalf("placed %v failed %d", placed, failed)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(cur.Path) != filepath.Join(tv, "Show", "Season 1") {
		t.Errorf("S01E02 at %s, want in Show/Season 1", cur.Path)
	}
}
