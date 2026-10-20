package automation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Regression tests from the adversarial review of the #56 fix: shows kept in a category or
// franchise folder beside another show Arrmada doesn't track. Whatever the show's folder is
// read as, a rescan must never claim the other show's files, a delete must never remove
// them, and neither a rename nor an import may put files straight into the category.

func rvFile(t *testing.T, path string, n int64) string {
	t.Helper()
	sparseFile(t, path, n)
	return path
}

func rvSet(t *testing.T, h *lifecycleHarness, season, ep int, path string) {
	t.Helper()
	mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, h.seriesID, season)
	mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-01', 1)`, h.seriesID, season, ep)
	mustExec(t, h.c, `UPDATE episodes SET has_file = 1, file_path = ?, size_bytes = ?, source_release = 'x' WHERE series_id = ? AND season_number = ? AND episode_number = ?`, path, 60<<20, h.seriesID, season, ep)
}

func rvTree(root string) string {
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			r, _ := filepath.Rel(root, p)
			out = append(out, r)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n    ")
}

type rvLayout struct {
	name     string
	category string            // the folder (relative to tv) other shows share with this one
	tracked  map[[2]int]string // relative to tv
	other    []string          // untracked files of another show, relative to tv
}

var rvLayouts = []rvLayout{
	{"category+resolution-tagged show folder", "Anime", map[[2]int]string{{1, 1}: "Anime/Show [1080p]/Season 1/Show - S01E01.mkv"},
		[]string{"Anime/Other Show/Season 1/Other Show - S01E01 - 2160p.mkv", "Anime/Other Show/Season 1/Other Show - S01E02 - 2160p.mkv"}},
	{"category+release-named show folder (flat)", "Anime", map[[2]int]string{{1, 1}: "Anime/Show.S01.1080p.WEB-DL-GRP/Show.S01E01.1080p.WEB-DL-GRP.mkv"},
		[]string{"Anime/Other Show/Other Show - S01E01.mkv", "Anime/Other Show/Other Show - S01E02.mkv"}},
	{"category+'Show Season 2' sibling folders", "Anime", map[[2]int]string{{1, 1}: "Anime/Show/Show - S01E01.mkv", {1, 2}: "Anime/Show Season 2/Show - S01E02.mkv"},
		[]string{"Anime/Other Show/Other Show - S01E01.mkv", "Anime/Other Show/Other Show - S01E02.mkv"}},
	{"category+two odd-named sibling folders", "Kids", map[[2]int]string{{1, 1}: "Kids/Bluey Part A/Bluey - S01E01.mkv", {1, 2}: "Kids/Bluey Part B/Bluey - S01E02.mkv"},
		[]string{"Kids/Other Show/Other Show - S01E01.mkv", "Kids/Other Show/Other Show - S01E02.mkv"}},
	{"franchise folder named after the show, odd subfolder", "Show", map[[2]int]string{{1, 1}: "Show/Classic Era/Show - S01E01.mkv"},
		[]string{"Show/Show Next Generation/Show Next Generation - S01E01.mkv", "Show/Show Next Generation/Show Next Generation - S01E02.mkv"}},
}

func rvSetup(t *testing.T, l rvLayout) (*lifecycleHarness, string) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	for k, rel := range l.tracked {
		rvSet(t, h, k[0], k[1], rvFile(t, filepath.Join(tv, rel), 60<<20))
	}
	for _, rel := range l.other {
		rvFile(t, filepath.Join(tv, rel), 90<<20)
	}
	return h, tv
}

// rvIntoCategory reports a path put straight into the category folder (a "Season N" made in
// it, or a file loose in it), or into the other show's folder.
func rvIntoCategory(tv string, l rvLayout, p string) bool {
	rel, err := filepath.Rel(tv, p)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, o := range l.other {
		if strings.HasPrefix(rel, filepath.ToSlash(filepath.Dir(o))+"/") {
			return true
		}
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || parts[0] != l.category {
		return false
	}
	if len(parts) == 2 {
		return true // loose in the category
	}
	return strings.HasPrefix(strings.ToLower(parts[1]), "season") // a season folder made in the category
}

func TestRevLayouts(t *testing.T) {
	for _, l := range rvLayouts {
		t.Run(l.name, func(t *testing.T) {
			// Rescan, then delete with files: the other show's files are never claimed or
			// removed.
			{
				h, tv := rvSetup(t, l)
				_ = h.c.RescanSeries(h.ctx, h.seriesID) // refusing is fine
				for k := range l.tracked {
					cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, k[0], k[1])
					if cur.Path != filepath.Join(tv, l.tracked[k]) {
						t.Errorf("after rescan S%02dE%02d -> %q, want it left at %s", k[0], k[1], cur.Path, l.tracked[k])
					}
				}
				if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
					t.Logf("delete: %v", err)
				}
				for _, rel := range l.other {
					if _, err := os.Stat(filepath.Join(tv, rel)); err != nil {
						t.Errorf("the untracked show's file %s is gone after rescan + delete; tree:\n    %s", rel, rvTree(tv))
					}
				}
			}
			// Rename preview never targets the category or the other show.
			{
				h, tv := rvSetup(t, l)
				items, _ := h.c.SeriesRenamePreview(h.ctx, h.seriesID) // refusing is fine
				for _, it := range items {
					if rvIntoCategory(tv, l, it.To) {
						t.Errorf("rename would move %s to %s", it.From, it.To)
					}
				}
			}
			// Import of a new episode: placed outside the category and the other show, or
			// refused.
			{
				h, tv := rvSetup(t, l)
				const name = "Show.S01E03.1080p.WEB-DL-GRP"
				mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, 1, 3, '2020-01-15', 1)`, h.seriesID)
				dl := h.download(t, name, name+".mkv")
				s, _ := h.c.series.Get(h.ctx, h.seriesID)
				h.c.importSeriesInto(h.ctx, s, dl, forceAll)
				if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 3); cur.Path != "" && rvIntoCategory(tv, l, cur.Path) {
					t.Errorf("import put S01E03 at %s", cur.Path)
				}
			}
		})
	}
}

// A category folder that is a symlink (to another disk): deleting one show with its files
// never removes the link, which would take every show in it out of the library.
func TestRevDeleteSymlinkedCategory(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	real := filepath.Join(t.TempDir(), "disk2", "Anime")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(tv, "Anime")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	rvSet(t, h, 1, 1, rvFile(t, filepath.Join(tv, "Anime", "Show [1080p]", "Season 1", "Show - S01E01.mkv"), 60<<20))
	rvFile(t, filepath.Join(real, "Other Show", "Season 1", "Other Show - S01E05.mkv"), 60<<20)
	if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
		t.Logf("delete: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tv, "Anime")); err != nil {
		t.Errorf("the category symlink %s is gone after deleting one show inside it: %v", filepath.Join(tv, "Anime"), err)
	}
	if _, err := os.Stat(filepath.Join(tv, "Anime", "Other Show", "Season 1", "Other Show - S01E05.mkv")); err != nil {
		t.Errorf("Other Show is no longer reachable through the TV folder: %v", err)
	}
}

// A symlink anywhere in a deleted show's folders is never unlinked, even when the folder
// it points at is empty.
func TestRevDeleteKeepsAnEmptySymlinkedSeasonFolder(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	real := filepath.Join(t.TempDir(), "disk2", "Season 2")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	ep := rvFile(t, filepath.Join(tv, "Show", "Season 1", "Show - S01E01.mkv"), 60<<20)
	if err := os.Symlink(real, filepath.Join(tv, "Show", "Season 2")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	rvSet(t, h, 1, 1, ep)
	if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
		t.Logf("delete: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tv, "Show", "Season 2")); err != nil {
		t.Errorf("the symlinked season folder was removed: %v", err)
	}
}
