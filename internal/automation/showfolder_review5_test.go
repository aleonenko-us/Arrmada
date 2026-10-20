package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/series"
)

// The hotfix reviews' findings, checked against main's resolver: the way out never
// re-points to another show's file, never acts while a share is away and never writes
// before a refusal; a "first record gone" doesn't widen a show's folder; a season folder
// right in the TV folder is never taken; a big show is judged in linear time. Temp dirs only.

// r5Records is every episode record of every show.
func r5Records(t *testing.T, h *lifecycleHarness) string {
	t.Helper()
	rows, err := h.c.db.Query(`SELECT series_id, season_number, episode_number, has_file, file_path FROM episodes ORDER BY series_id, season_number, episode_number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid int64
		var s, e, hf int
		var p string
		if err := rows.Scan(&sid, &s, &e, &hf, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d:S%02dE%02d:%d:%s", sid, s, e, hf, p))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// r5Unchanged runs a rescan and fails if any record of any show changed.
func r5Unchanged(t *testing.T, h *lifecycleHarness) {
	t.Helper()
	before := r5Records(t, h)
	_ = h.c.RescanSeries(h.ctx, h.seriesID)
	if after := r5Records(t, h); after != before {
		t.Errorf("the rescan changed records:\nbefore\n%s\nafter\n%s", before, after)
	}
}

// r5NeverRepointed runs a rescan and fails if any record now points at a different file:
// a record may only stay as it was, or be cleared because its own file is gone.
func r5NeverRepointed(t *testing.T, h *lifecycleHarness) {
	t.Helper()
	before := strings.Split(r5Records(t, h), "\n")
	_ = h.c.RescanSeries(h.ctx, h.seriesID)
	after := strings.Split(r5Records(t, h), "\n")
	if len(after) != len(before) {
		t.Fatalf("records went from %d to %d", len(before), len(after))
	}
	for i := range before {
		if after[i] == before[i] {
			continue
		}
		cleared := strings.HasSuffix(after[i], ":0:")
		if old := before[i][strings.LastIndex(before[i], ":")+1:]; !cleared || old == "" {
			t.Errorf("record re-pointed: %s -> %s", before[i], after[i])
			continue
		}
		if _, err := os.Stat(before[i][strings.LastIndex(before[i], ":")+1:]); err == nil {
			t.Errorf("a record whose file is there was cleared: %s", before[i])
		}
	}
}

func TestRevEFollowNeverRepointsWrongly(t *testing.T) {
	t.Run("a show of another year beside", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Show (2019)/Season 1/Show - S01E01.mkv"},
		})
		mustExec(t, h.c, `UPDATE series SET year = 2005 WHERE id = ?`, h.seriesID)
		r5Unchanged(t, h)
		rbExists(t, tv, "Show (2019)/Season 1/Show - S01E01.mkv")
	})
	t.Run("share away", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Show/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Show/S01/Show - S01E02.mkv", tracked: [2]int{1, 2}, missing: true},
			{rel: "Elsewhere/S01/Show - S01E01.mkv"},
			{rel: "Other Show/Season 1/Other Show - S01E01.mkv"},
		})
		r5Unchanged(t, h)
		rbExists(t, tv, "Elsewhere/S01/Show - S01E01.mkv")
	})
	t.Run("into a category: still refused, nothing written", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Kids/S01/Show - S01E01.mkv"},
			{rel: "Kids/Bluey/Season 1/Bluey - S01E01.mkv"},
		})
		r5Unchanged(t, h)
		if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
			t.Logf("delete: %v", err)
		}
		rbExists(t, tv, "Kids/S01/Show - S01E01.mkv", "Kids/Bluey/Season 1/Bluey - S01E01.mkv")
	})
	t.Run("generic names in an untracked show's season folder beside", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Shared/S01/S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Shared/S01/S01E02.mkv", tracked: [2]int{1, 2}},
			{rel: "Shared/Season 1/S01E01.mkv"},
			{rel: "Shared/Other/Season 1/Other - S01E01.mkv"},
		})
		r5NeverRepointed(t, h)
		if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
			t.Logf("delete: %v", err)
		}
		rbExists(t, tv, "Shared/Season 1/S01E01.mkv", "Shared/Other/Season 1/Other - S01E01.mkv")
	})
}

// G3, G4: the show's S02 file is gone (its folder kept or not), Season 1 is there, and an
// untracked show sits in the same folder. The folder holding another show is never the
// show's: rescan, duplicates and delete touch nothing of the other show's.
func TestRevEFirstRecordGoneNeverWidens(t *testing.T) {
	for _, keepS02 := range []bool{false, true} {
		t.Run(fmt.Sprintf("S02 folder kept=%v", keepS02), func(t *testing.T) {
			files := []rbFile{
				{rel: "Star Trek/S02/Show - S02E01.mkv", tracked: [2]int{2, 1}, missing: true},
				{rel: "Star Trek/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
				{rel: "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"},
			}
			if keepS02 {
				files = append(files, rbFile{rel: "Star Trek/S02/folder.jpg"})
			}
			h, tv := rbSetup(t, files)
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("placement = %v, want refused (Star Trek holds another show)", err)
			}
			r5Unchanged(t, h)
			if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("duplicates = %v, want refused", err)
			}
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv")
		})
	}
}

// D1: a season folder right in the TV folder is never a show's: placement, rescan and
// duplicates refuse, and nothing is imported there.
func TestRevESeasonFolderInTheTVFolder(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{{rel: "Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}}})
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
		t.Fatalf("placement = %v, want refused", err)
	}
	r5Unchanged(t, h)
	if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("duplicates = %v, want refused", err)
	}
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	if placed, _, _, _ := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 0 {
		t.Errorf("imported %v", placed)
	}
	if entries, _ := os.ReadDir(filepath.Join(tv, "Season 1")); len(entries) != 1 {
		t.Errorf("Season 1 holds %d entries, want only the old file", len(entries))
	}
}

// A new episode joins the season folder the show already has, whatever it's called, and
// never makes a parallel "Season 1" beside it; while that folder is away the import is
// refused rather than put in a new one. A season the show has no folder for gets the
// scheme's.
func TestRevESeasonFolderJoinedWhateverItsName(t *testing.T) {
	for _, name := range []string{"Stagione 1", "Staffel 01", "Series 1", "Saison 1", "Season 01 (2014)", "S01"} {
		t.Run(name, func(t *testing.T) {
			h, tv := rbSetup(t, []rbFile{
				{rel: "Show/" + name + "/Show - S01E01.mkv", tracked: [2]int{1, 1}},
			})
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			const rel = "Show.S01E02.1080p.WEB-DL-GRP"
			dl := h.download(t, rel, rel+".mkv")
			if _, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); failed != 0 {
				t.Fatalf("import failed")
			}
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(cur.Path) != filepath.Join(tv, "Show", name) {
				t.Errorf("S01E02 at %q, want in %s", cur.Path, name)
			}
			noDir(t, filepath.Join(tv, "Show", "Season 1"), "a parallel season folder")
		})
	}
	t.Run("beside its own S01, never a Season 1 of someone else's files", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Star Trek/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
			{rel: "Star Trek/Season 1/Other - S01E01.mkv"},
		})
		s, _ := h.c.series.Get(h.ctx, h.seriesID)
		const rel = "Show.S01E02.1080p.WEB-DL-GRP"
		dl := h.download(t, rel, rel+".mkv")
		if _, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); failed != 0 {
			t.Fatalf("import failed")
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(cur.Path) != filepath.Join(tv, "Star Trek", "S01") {
			t.Errorf("S01E02 at %q, want in S01", cur.Path)
		}
	})
	t.Run("season folder away", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Show/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Show/S02/Show - S02E01.mkv", tracked: [2]int{2, 1}},
		})
		s, _ := h.c.series.Get(h.ctx, h.seriesID)
		const rel = "Show.S01E02.1080p.WEB-DL-GRP"
		dl := h.download(t, rel, rel+".mkv")
		if placed, _, _, _ := h.c.importSeriesInto(h.ctx, s, dl, forceAll); len(placed) != 0 {
			t.Errorf("imported %v while S01 is away", placed)
		}
		noDir(t, filepath.Join(tv, "Show", "Season 1"), "a parallel season folder")
		noDir(t, filepath.Join(tv, "Show", "S01"), "the away season folder, made anew")
	})
}

// A show with no files yet is rescanned from a folder named for it — "Show (2005)", or
// "Show" in a library named without years — only when that folder is provably its own.
func TestRevENewShowFolderBothSpellings(t *testing.T) {
	run := func(t *testing.T, files []rbFile, setup func(h *lifecycleHarness)) (*lifecycleHarness, string) {
		t.Helper()
		h, tv := rbSetup(t, files)
		mustExec(t, h.c, `UPDATE series SET year = 2005 WHERE id = ?`, h.seriesID)
		mustExec(t, h.c, `UPDATE episodes SET has_file = 0, file_path = '' WHERE series_id = ?`, h.seriesID)
		if setup != nil {
			setup(h)
		}
		if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
			t.Fatalf("rescan: %v", err)
		}
		return h, tv
	}
	t.Run("Title (Year)", func(t *testing.T) {
		h, tv := run(t, []rbFile{{rel: "Show (2005)/Season 1/Show - S01E01.mkv"}}, nil)
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Show (2005)", "Season 1", "Show - S01E01.mkv") {
			t.Errorf("S01E01 = %q", cur.Path)
		}
	})
	t.Run("Title, no year", func(t *testing.T) {
		h, tv := run(t, []rbFile{{rel: "Show/Season 1/Show - S01E01.mkv"}}, nil)
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Show", "Season 1", "Show - S01E01.mkv") {
			t.Errorf("S01E01 = %q", cur.Path)
		}
	})
	t.Run("Title, no year, another show of that title in the library", func(t *testing.T) {
		h, _ := run(t, []rbFile{{rel: "Show/Season 1/Show - S01E01.mkv"}}, func(h *lifecycleHarness) {
			mustExec(t, h.c, `INSERT INTO series (tmdb_id, title, year, monitored) VALUES (99, 'Show', 2019, 1)`)
		})
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != "" {
			t.Errorf("claimed %q from a folder that may be the 2019 show's", cur.Path)
		}
	})
	t.Run("Title, holding another show", func(t *testing.T) {
		h, _ := run(t, []rbFile{
			{rel: "Show/Season 1/Show - S01E01.mkv"},
			{rel: "Show/Show Spin-off/Season 1/Show Spin-off - S01E01.mkv"},
		}, nil)
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != "" {
			t.Errorf("claimed %q from a folder holding another show", cur.Path)
		}
	})
}

// A show of 2,000 episodes in 20 season folders is judged, and its files filtered, in
// linear time: every folder is checked once per lookup.
func TestRevEBigShowIsJudgedInLinearTime(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	for s := 1; s <= 20; s++ {
		mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, h.seriesID, s)
		dir := filepath.Join(tv, "Show", fmt.Sprintf("S%02d", s))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for e := 1; e <= 100; e++ {
			p := filepath.Join(dir, fmt.Sprintf("Show - S%02dE%03d.mkv", s, e))
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-01', 1)`, h.seriesID, s, e)
			mustExec(t, h.c, `UPDATE episodes SET has_file = 1, file_path = ? WHERE series_id = ? AND season_number = ? AND episode_number = ?`, p, h.seriesID, s, e)
		}
	}
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	start := time.Now()
	info := h.c.series.ShowFolderOf(h.ctx, h.seriesID)
	keep := h.c.series.EpisodeFileFilter(h.ctx, s, filepath.Join(tv, "Show"))
	n := 0
	for s := 1; s <= 20; s++ {
		for e := 1; e <= 100; e++ {
			if keep(filepath.Join(tv, "Show", fmt.Sprintf("S%02d", s), fmt.Sprintf("Show - S%02dE%03d.mkv", s, e))) {
				n++
			}
		}
	}
	took := time.Since(start)
	if info.Unknown || info.Path != filepath.Join(tv, "Show") || n != 2000 {
		t.Fatalf("ShowFolderOf = %+v, kept %d of 2000", info, n)
	}
	if took > 3*time.Second {
		t.Errorf("judging 2,000 episodes took %s", took)
	}
}
