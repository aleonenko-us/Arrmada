package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// Third review of the #56 fix: what a rescan and the duplicate check read must be exactly
// the show's own files — never another show's inside its folder, never a featurette, never
// a link — and a show whose folder can't be told has a way out. Temp dirs only.

// rcEpisodes adds episode rows (without files) the harness's metadata doesn't have.
func rcEpisodes(t *testing.T, h *lifecycleHarness, refs ...[2]int) {
	t.Helper()
	for _, r := range refs {
		mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, h.seriesID, r[0])
		mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-01', 1)`, h.seriesID, r[0], r[1])
	}
}

// rcDupPaths is every path the duplicate check lists, kept copies included.
func rcDupPaths(t *testing.T, h *lifecycleHarness) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	dupes, err := h.c.SeriesDuplicates(h.ctx, h.seriesID)
	if err != nil {
		t.Logf("duplicates: %v", err)
	}
	for _, d := range dupes {
		out[d.Keeping.Path] = true
		for _, e := range d.Extras {
			out[e.Path] = true
		}
	}
	return out
}

// A folder above the show's S01 holding a link — to another show's folder, or to another
// show's season — is never taken for the show's: the link could be anything.
func TestRevCSymlinkInTheShowFolderRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		link string // relative to tv
	}{
		{"linked show folder", "Star Trek/Star Trek TNG"},
		{"linked season folder", "Star Trek/S03"},
		{"hidden linked folder", "Star Trek/.TNG"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, tv := rbSetup(t, []rbFile{{rel: "Star Trek/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}}})
			other := filepath.Join(t.TempDir(), "elsewhere", "Season 1")
			sparseFile(t, filepath.Join(other, "Show - S01E02.mkv"), 60<<20)
			if err := os.Symlink(other, filepath.Join(tv, filepath.FromSlash(c.link))); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("placement = %v, want refused", err)
			}
			if p := h.c.series.FolderPath(h.ctx, h.seriesID); p == filepath.Join(tv, "Star Trek") {
				t.Errorf("FolderPath = %s", p)
			}
		})
	}
}

// A featurette in a Plex extras folder named like an episode is never taken for one: a
// rescan leaves the episode on its own file, the duplicate check doesn't list it, and a
// delete with files leaves it alone.
func TestRevCExtrasNamedLikeAnEpisode(t *testing.T) {
	for _, extras := range []string{"Extras", "Featurettes", "Behind The Scenes", "Season 1/Deleted Scenes", "trailers", "Other"} {
		t.Run(extras, func(t *testing.T) {
			featRel := "Show/" + extras + "/Show - S01E01 - Behind the Scenes.mkv"
			h, tv := rbSetup(t, []rbFile{
				{rel: "Show/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
				{rel: "Show/Season 2/Show - S02E01.mkv", tracked: [2]int{2, 1}},
				{rel: featRel},
			})
			featurette := filepath.Join(tv, filepath.FromSlash(featRel))
			if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
				t.Fatalf("rescan: %v", err)
			}
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Show", "Season 1", "Show - S01E01.mkv") {
				t.Errorf("after rescan S01E01 = %s", cur.Path)
			}
			if rcDupPaths(t, h)[featurette] {
				t.Error("the featurette is listed by the duplicate check")
			}
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, featRel)
		})
	}
}

// Release, season-pack and disc folders inside the show's folder are still read by a
// rescan: narrowing what it reads never loses the show's own files.
func TestRevCRescanStillFindsReleaseAndDiscFolders(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Show/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Show/Season 1/Disc 1/Show - S01E02.mkv"},
		{rel: "Show/Season 1/Show.S01E03.1080p.WEB-DL-GRP/Show.S01E03.1080p.WEB-DL-GRP.mkv"},
		{rel: "Show/Show.S02.1080p.WEB-DL-GRP/Show.S02E01.1080p.WEB-DL-GRP.mkv"},
		{rel: "Show/Show - S01E04.mkv"},
		{rel: "Show/S01/Show - S01E05.mkv"},
	})
	rcEpisodes(t, h, [2]int{1, 3}, [2]int{1, 4}, [2]int{1, 5}, [2]int{2, 1})
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	for _, want := range []struct {
		s, e int
		rel  string
	}{
		{1, 2, "Show/Season 1/Disc 1/Show - S01E02.mkv"},
		{1, 3, "Show/Season 1/Show.S01E03.1080p.WEB-DL-GRP/Show.S01E03.1080p.WEB-DL-GRP.mkv"},
		{2, 1, "Show/Show.S02.1080p.WEB-DL-GRP/Show.S02E01.1080p.WEB-DL-GRP.mkv"},
		{1, 5, "Show/S01/Show - S01E05.mkv"},
	} {
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, want.s, want.e); cur.Path != filepath.Join(tv, filepath.FromSlash(want.rel)) {
			t.Errorf("after rescan S%02dE%02d = %q, want %s", want.s, want.e, cur.Path, want.rel)
		}
	}
	// A show kept in season folders doesn't keep episodes loose in its folder: a video
	// there is something else, never claimed nor offered as a duplicate.
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 4); cur.Path != "" {
		t.Errorf("rescan claimed the loose %s", cur.Path)
	}
}

// A flat show (its files right in its folder) is still read there.
func TestRevCRescanReadsAFlatShow(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Show/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Show/Show - S01E02.mkv"},
	})
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != filepath.Join(tv, "Show", "Show - S01E02.mkv") {
		t.Errorf("after rescan S01E02 = %q", cur.Path)
	}
}

// (ops7-follow-*) Untitled files ("S01E01.mkv") in a Season 1 folder beside the show's own
// S01 could be anyone's: a rescan never claims them — not when the show's S01 is on a share
// that's away, nor when one of its files is gone — and the duplicate check never lists them.
func TestRevCUntitledFilesBesideTheShowsSeasonFolder(t *testing.T) {
	paths := func(h *lifecycleHarness) string {
		var out []string
		for ep := 1; ep <= 2; ep++ {
			out = append(out, h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, ep).Path)
		}
		return strings.Join(out, ";")
	}
	t.Run("share away", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Kids/S01/S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Kids/S01/S01E02.mkv", tracked: [2]int{1, 2}, missing: true},
			{rel: "Kids/Season 1/S01E01.mkv"},
			{rel: "Kids/Season 1/S01E02.mkv"},
		})
		before := paths(h)
		_ = h.c.RescanSeries(h.ctx, h.seriesID)
		if after := paths(h); after != before {
			t.Errorf("records changed: %s -> %s", before, after)
		}
		rbExists(t, tv, "Kids/Season 1/S01E01.mkv", "Kids/Season 1/S01E02.mkv")
	})
	t.Run("one file gone, two candidates", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Kids/S01/S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Kids/S01/S01E02.mkv", tracked: [2]int{1, 2}},
			{rel: "Kids/Season 1/S01E01.mkv"},
			{rel: "Kids/Season 01/S01E01.mkv"},
		})
		dups := rcDupPaths(t, h)
		for _, rel := range []string{"Kids/Season 1/S01E01.mkv", "Kids/Season 01/S01E01.mkv"} {
			if dups[filepath.Join(tv, filepath.FromSlash(rel))] {
				t.Errorf("the duplicate check lists %s", rel)
			}
		}
		_ = h.c.RescanSeries(h.ctx, h.seriesID)
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); strings.Contains(cur.Path, "Season") {
			t.Errorf("rescan claimed %s", cur.Path)
		}
		rbExists(t, tv, "Kids/Season 1/S01E01.mkv", "Kids/Season 01/S01E01.mkv")
	})
}

// (ops6-st-loose-video) A video loose in a show kept in season folders, named like one of
// its episodes, is never offered as a duplicate to delete.
func TestRevCLooseVideoInASeasonFolderedShow(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Star Trek/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Star Trek/S01/Show - S01E02.mkv", tracked: [2]int{1, 2}},
		{rel: "Star Trek/Show - S01E01 - Pilot (Unaired).mkv"},
	})
	loose := filepath.Join(tv, "Star Trek", "Show - S01E01 - Pilot (Unaired).mkv")
	if rcDupPaths(t, h)[loose] {
		t.Error("the duplicate check lists the loose video")
	}
	if err := h.c.DeleteSeriesDuplicate(h.ctx, h.seriesID, loose); err == nil {
		t.Error("deleting the loose video as a duplicate was allowed")
	}
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Star Trek", "S01", "Show - S01E01.mkv") {
		t.Errorf("after rescan S01E01 = %q", cur.Path)
	}
	rbExists(t, tv, "Star Trek/Show - S01E01 - Pilot (Unaired).mkv")
}

// Another show inside the show's folder — hidden, under a folder named like an extras
// folder, or deeper than anything looks — is never read: a rescan claims none of its files,
// the duplicate check offers none of them for deletion, and a delete leaves them all.
// Its files are named like this show's, so only where they are tells them apart.
func TestRevCNestedShowIsNeverRead(t *testing.T) {
	for _, c := range []struct{ name, dir string }{
		{"hidden", "Show/.TNG/Season 1"},
		{"under Other", "Show/Other/TNG/Season 1"},
		{"under Bonus", "Show/Bonus/TNG/Season 1"},
		{"deep", "Show/a/b/c/d/e/f/g/h/i/Season 1"},
		{"nested show folder", "Show/Show The Next Generation/Season 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e1, e2 := c.dir+"/Show - S01E01.mkv", c.dir+"/Show - S01E02.mkv"
			h, tv := rbSetup(t, []rbFile{
				{rel: "Show/Season 01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
				{rel: e1},
				{rel: e2},
			})
			if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil && !errors.Is(err, series.ErrNoShowFolder) {
				t.Fatalf("rescan: %v", err)
			}
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != "" {
				t.Errorf("rescan claimed %s", cur.Path)
			}
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Show", "Season 01", "Show - S01E01.mkv") {
				t.Errorf("S01E01 = %s", cur.Path)
			}
			dups := rcDupPaths(t, h)
			for _, rel := range []string{e1, e2} {
				p := filepath.Join(tv, filepath.FromSlash(rel))
				if dups[p] {
					t.Errorf("the duplicate check lists %s", rel)
				}
				if err := h.c.DeleteSeriesDuplicate(h.ctx, h.seriesID, p); err == nil {
					t.Errorf("deleting %s as a duplicate was allowed", rel)
				}
			}
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, e1, e2)
		})
	}
}

// (y) Another show's file dropped into one of the show's season folders is never claimed
// by a rescan nor offered as a duplicate: its name says whose it is.
func TestRevCAnotherShowsFileInASeasonFolder(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Kids/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Kids/S01/Bluey - S01E01.mkv"},
		{rel: "Kids/S01/Bluey - S01E02.mkv"},
		{rel: "Kids/S01/Show.S01E03.1080p.WEB-DL-GRP.mkv"},
	})
	rcEpisodes(t, h, [2]int{1, 3})
	// Bluey is in the library: its name positively says whose the files are. (In a folder
	// that's provably the show's own, an episode whose name merely doesn't fit — romaji, a
	// short title — is the show's; see TestRevGProvenFolderReadsVariantNames.)
	mustExec(t, h.c, `INSERT INTO series (tmdb_id, title, year, monitored) VALUES (82728, 'Bluey', 2018, 1)`)
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != "" {
		t.Errorf("rescan claimed %s", cur.Path)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 3); cur.Path != filepath.Join(tv, "Kids", "S01", "Show.S01E03.1080p.WEB-DL-GRP.mkv") {
		t.Errorf("rescan didn't find the show's own S01E03: %q", cur.Path)
	}
	if dups := rcDupPaths(t, h); dups[filepath.Join(tv, "Kids", "S01", "Bluey - S01E01.mkv")] {
		t.Error("the duplicate check lists Bluey's file")
	}
	if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
		t.Logf("delete: %v", err)
	}
	rbExists(t, tv, "Kids/S01/Bluey - S01E01.mkv", "Kids/S01/Bluey - S01E02.mkv")
}

// (b) A rescan walks only the show's own folder: a folder of the same name elsewhere in
// the TV folder is never read.
func TestRevCRescanWalksOnlyTheShowsOwnFolder(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Kids/Show/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Show/Season 1/Show - S01E02.mkv"},
		{rel: "Show/Season 1/Show - S01E01.mkv"},
	})
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != "" {
		t.Errorf("rescan claimed %s", cur.Path)
	}
	if dups := rcDupPaths(t, h); dups[filepath.Join(tv, "Show", "Season 1", "Show - S01E01.mkv")] {
		t.Error("the duplicate check lists the other folder's file")
	}
}

// The way out for a show whose folder can't be told: move its files by hand into a folder
// named with the show's year, then Refresh & rescan — the moved files are followed, the show
// reads normally again, and it imports into that folder. A year-less folder may as well be a
// namesake's: nothing is followed into it.
func TestRevCRescanFollowsFilesMovedOutOfAnUnclearFolder(t *testing.T) {
	t.Run("all recorded in a season-named folder", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
			{rel: "S01/Season 1/Show - S01E02.mkv", tracked: [2]int{1, 2}},
		})
		mustExec(t, h.c, `UPDATE series SET year = 2005 WHERE id = ?`, h.seriesID)
		s, _ := h.c.series.Get(h.ctx, h.seriesID)
		if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("placement = %v, want refused", err)
		}
		if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("rescan before the move = %v, want refused", err)
		} else if !strings.Contains(err.Error(), "Refresh & rescan") {
			t.Errorf("the refusal doesn't say the way out: %v", err)
		}
		// Into a year-less folder: never followed, the show stays refused, nothing changes.
		bare := filepath.Join(tv, "Show", "S01")
		if err := os.MkdirAll(bare, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(tv, "S01", "Season 1", "Show - S01E01.mkv"), filepath.Join(bare, "Show - S01E01.mkv")); err != nil {
			t.Fatal(err)
		}
		if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("rescan after a move into a year-less folder = %v, want refused", err)
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "S01", "Season 1", "Show - S01E01.mkv") {
			t.Fatalf("S01E01 = %q, want unchanged", cur.Path)
		}
		if err := os.Rename(filepath.Join(bare, "Show - S01E01.mkv"), filepath.Join(tv, "S01", "Season 1", "Show - S01E01.mkv")); err != nil {
			t.Fatal(err)
		}
		own := filepath.Join(tv, "Real Folder (2005)", "S01")
		for _, n := range []string{"Show - S01E01.mkv", "Show - S01E02.mkv"} {
			if err := os.MkdirAll(own, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(tv, "S01", "Season 1", n), filepath.Join(own, n)); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
			t.Fatalf("rescan after the move: %v", err)
		}
		for ep := 1; ep <= 2; ep++ {
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, ep); filepath.Dir(cur.Path) != own {
				t.Errorf("S01E%02d = %q, want in %s", ep, cur.Path, own)
			}
		}
		if p := h.c.series.FolderPath(h.ctx, h.seriesID); p != filepath.Join(tv, "Real Folder (2005)") {
			t.Errorf("FolderPath = %q", p)
		}
	})
	t.Run("never a generic name", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Kids/S01/01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Kids/Season 1/01.mkv"},
			{rel: "Kids/Bluey/Season 1/Bluey - S01E01.mkv"},
		})
		if err := h.c.RescanSeries(h.ctx, h.seriesID); err == nil {
			t.Log("rescan ran")
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "Kids", "S01", "01.mkv") && cur.Path != "" {
			t.Errorf("S01E01 = %q: followed a generic name", cur.Path)
		}
		if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
			t.Logf("delete: %v", err)
		}
		rbExists(t, tv, "Kids/Season 1/01.mkv", "Kids/Bluey/Season 1/Bluey - S01E01.mkv")
	})
	t.Run("never another tracked show's file", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "Shared/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "Shared/S01/Show - S01E02.mkv", tracked: [2]int{1, 2}},
		})
		res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (99, 'Other', 1)`)
		if err != nil {
			t.Fatal(err)
		}
		oid, _ := res.LastInsertId()
		other := filepath.Join(tv, "Shared", "Season 1", "Show - S01E01.mkv")
		sparseFile(t, other, 60<<20)
		mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, 1, 1)`, oid)
		mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored, has_file, file_path) VALUES (?, 1, 1, '2020-01-01', 1, 1, ?)`, oid, other)
		_ = h.c.RescanSeries(h.ctx, h.seriesID)
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path == other {
			t.Error("followed into another show's recorded file")
		}
		if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
			t.Logf("delete: %v", err)
		}
		rbExists(t, tv, "Shared/Season 1/Show - S01E01.mkv")
	})
	t.Run("never a file another show has, nor one of two", func(t *testing.T) {
		h, tv := rbSetup(t, []rbFile{
			{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
			{rel: "A/S01/Show - S01E01.mkv"},
			{rel: "B/S01/Show - S01E01.mkv"},
		})
		if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("rescan = %v, want refused", err)
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1); cur.Path != filepath.Join(tv, "S01", "Season 1", "Show - S01E01.mkv") {
			t.Errorf("S01E01 = %q, want unchanged", cur.Path)
		}
	})
}
