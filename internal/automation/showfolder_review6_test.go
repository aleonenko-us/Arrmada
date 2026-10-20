package automation

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The review of a09b67f2 (A, B, M) and the bench's Dark Matter case, as regression tests:
// every one is closed by refusing. Temp dirs only.

func r6Meta(title string, year, seasons, eps int) *importMeta {
	var ss []metadata.SeasonDetails
	for s := 1; s <= seasons; s++ {
		var e []metadata.EpisodeDetails
		for i := 1; i <= eps; i++ {
			e = append(e, metadata.EpisodeDetails{EpisodeNumber: i, AirDate: "2006-01-01"})
		}
		ss = append(ss, metadata.SeasonDetails{SeasonNumber: s, Episodes: e})
	}
	return &importMeta{d: metadata.SeriesDetails{
		SeriesResult:    metadata.SeriesResult{TMDBID: 2316, Title: title, Year: year},
		NumberingSource: "tmdb", Seasons: ss,
	}}
}

type r6Naming struct{ n library.SeriesNaming }

func (s r6Naming) SeriesNaming() library.SeriesNaming { return s.n }

func r6Gone(t *testing.T, paths []string, when string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: another show's %s is gone", when, p)
		}
	}
}

// A: the US "The Office" (2005), added with no files, beside the untracked UK show in a
// year-less "The Office". Rescan claims none of the UK files, an import goes to a new
// folder of the US show's own, and delete-with-files leaves every UK file.
func TestRevFYearlessNamesakeNeverClaimed(t *testing.T) {
	for i, name := range []string{"The Office - S01E0%d - Ep.mkv", "The Office (2001) - S01E0%d - Ep.mkv"} {
		t.Run([]string{"no year", "UK year"}[i], func(t *testing.T) {
			h := newStallHarness(t)
			tv := t.TempDir()
			h.c.series = series.NewService(h.c.db, r6Meta("The Office", 2005, 3, 6), tv, h.c.log)
			h.c.imp = library.NewImporter(tv, h.c.log)
			h.c.series.SetSeasonFolderMatcher(h.c.imp.IsSeasonFolderName)
			s, err := h.c.series.Add(h.ctx, 2316, "", true)
			if err != nil {
				t.Fatal(err)
			}
			var uk []string
			for e := 1; e <= 2; e++ {
				uk = append(uk, libFile(t, filepath.Join(tv, "The Office", "Season 1", strings.Replace(name, "%d", string(rune('0'+e)), 1))))
			}
			if err := h.c.RescanSeries(h.ctx, s.ID); err != nil {
				t.Logf("rescan: %v", err)
			}
			for e := 1; e <= 2; e++ {
				if cur := h.c.series.CurrentEpisodeFile(h.ctx, s.ID, 1, e); cur.Path != "" {
					t.Errorf("rescan claimed the UK %q", cur.Path)
				}
			}
			const rel = "The.Office.US.S01E01.1080p.BluRay.x264-GRP"
			dl := filepath.Join(t.TempDir(), rel)
			libFile(t, filepath.Join(dl, "The.Office.S01E01.1080p.BluRay.x264-GRP.mkv"))
			s, _ = h.c.series.Get(h.ctx, s.ID)
			h.c.importSeriesInto(h.ctx, s, dl, forceAll)
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, s.ID, 1, 1); strings.HasPrefix(cur.Path, filepath.Join(tv, "The Office")+string(filepath.Separator)) {
				t.Errorf("the US S01E01 went into the UK show's folder: %s", cur.Path)
			}
			r6Gone(t, uk, "after the import")
			if _, err := h.c.series.Delete(h.ctx, s.ID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			r6Gone(t, uk, "after delete with files")
		})
	}
}

// B: Pechino Express (2019)'s files that #56 put in <TV>/S12/Season 12 are gone; the 2012
// show (untracked) has files of the same names in a year-less "Pechino Express". Nothing
// is followed into it: no record re-pointed, its files survive import and delete.
func TestRevFFollowNeverIntoAYearlessNamesake(t *testing.T) {
	for _, own := range []int{1, 0} {
		t.Run(map[int]string{1: "one file in its own folder", 0: "none in its own folder"}[own], func(t *testing.T) {
			h := newStallHarness(t)
			tv := t.TempDir()
			h.c.series = series.NewService(h.c.db, r6Meta("Pechino Express", 2019, 12, 6), tv, h.c.log)
			h.c.imp = library.NewImporter(tv, h.c.log)
			h.c.imp.SetSeriesNaming(r6Naming{library.SeriesNaming{Folder: "{title} ({year})", SeasonFolder: "S{season00}", EpisodeFile: "{title} - {episode}"}})
			h.c.series.SetSeasonFolderMatcher(h.c.imp.IsSeasonFolderName)
			s, err := h.c.series.Add(h.ctx, 2316, "", true)
			if err != nil {
				t.Fatal(err)
			}
			for e := 1; e <= own; e++ {
				p := libFile(t, filepath.Join(tv, "Pechino Express (2019)", "S12", "Pechino Express - S12E0"+string(rune('0'+e))+".mkv"))
				setEpisodeFile(t, h.c, s.ID, 12, e, p, "x")
			}
			var other, gone []string
			for e := own + 1; e <= own+2; e++ {
				n := "Pechino Express - S12E0" + string(rune('0'+e)) + ".mkv"
				g := filepath.Join(tv, "S12", "Season 12", n)
				setEpisodeFile(t, h.c, s.ID, 12, e, g, "x")
				gone = append(gone, g)
				other = append(other, libFile(t, filepath.Join(tv, "Pechino Express", "S12", n)))
			}
			other = append(other, libFile(t, filepath.Join(tv, "Pechino Express", "S11", "Pechino Express - S11E01.mkv")))
			_ = h.c.RescanSeries(h.ctx, s.ID)
			for i, g := range gone {
				if cur := h.c.series.CurrentEpisodeFile(h.ctx, s.ID, 12, own+1+i); cur.Path != g && cur.Path != "" {
					t.Errorf("S12E%02d re-pointed to %q", own+1+i, cur.Path)
				}
			}
			for _, rel := range []string{"Pechino.Express.S12E02.1080p.WEB-DL-GRP", "Pechino.Express.S12E04.1080p.WEB-DL-GRP"} {
				dl := filepath.Join(t.TempDir(), rel)
				libFile(t, filepath.Join(dl, rel+".mkv"))
				s, _ = h.c.series.Get(h.ctx, s.ID)
				h.c.importSeriesInto(h.ctx, s, dl, forceAll)
			}
			r6Gone(t, other, "after the imports")
			if _, err := h.c.series.Delete(h.ctx, s.ID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			r6Gone(t, other, "after delete with files")
		})
	}
}

// The bench's ops9-follow-pair-untracked-yearless: Dark Matter (2015)'s #56 leftovers are
// gone; Dark Matter (2024) is tracked with no files; a year-less "Dark Matter/Season 01"
// holds the 2024 show's episodes. A rescan of the 2015 show never re-points into it.
func TestRevFDarkMatterPair(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "S01/Season 1/Show - S01E02.mkv", tracked: [2]int{1, 2}, missing: true},
		{rel: "Show/Season 01/Show - S01E01.mkv"},
		{rel: "Show/Season 01/Show - S01E02.mkv"},
	})
	mustExec(t, h.c, `UPDATE series SET year = 2015 WHERE id = ?`, h.seriesID)
	mustExec(t, h.c, `INSERT INTO series (tmdb_id, title, year, monitored) VALUES (99, 'Show', 2024, 1)`)
	r5NeverRepointed(t, h)
	for ep := 1; ep <= 2; ep++ {
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, ep); strings.Contains(cur.Path, "Season 01") {
			t.Errorf("S01E%02d followed into the year-less folder: %s", ep, cur.Path)
		}
	}
	if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
		t.Logf("delete: %v", err)
	}
	rbExists(t, tv, "Show/Season 01/Show - S01E01.mkv", "Show/Season 01/Show - S01E02.mkv")
}

// ops6-st-sib-season-named: another show's file in the show's own Season 1 may be read on
// the benefit of the doubt, but is never offered as a duplicate to delete.
func TestRevFDuplicatesOfferOnlyTheShowsNamedFiles(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Star Trek/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Star Trek/S01/Show - S01E02.mkv", tracked: [2]int{1, 2}},
		{rel: "Star Trek/Season 1/Other Show - S01E01.mkv"},
		{rel: "Star Trek/S01/Show.S01E02.720p.HDTV-GRP.mkv"},
	})
	dups := rcDupPaths(t, h)
	if dups[filepath.Join(tv, "Star Trek", "Season 1", "Other Show - S01E01.mkv")] {
		t.Error("the duplicate check offers another show's file")
	}
	if !dups[filepath.Join(tv, "Star Trek", "S01", "Show.S01E02.720p.HDTV-GRP.mkv")] {
		t.Error("the show's own extra copy isn't listed")
	}
	if err := h.c.DeleteSeriesDuplicate(h.ctx, h.seriesID, filepath.Join(tv, "Star Trek", "Season 1", "Other Show - S01E01.mkv")); err == nil {
		t.Error("deleting another show's file as a duplicate was allowed")
	}
	rbExists(t, tv, "Star Trek/Season 1/Other Show - S01E01.mkv")
}

// An untracked show's episode in the show's own season 2 folder. Where v0.3.3 read nothing
// (an "S02"-style show, a show in a category) or the name has no title, it survives a
// rescan, a rename, an import of S02E01 and a delete with files; in an ordinary
// "Title (Year)/Season 2" it's read as v0.3.3 read it.
func TestRevFOtherShowsEpisodeInTheShowsSeasonFolder(t *testing.T) {
	for _, c := range []struct {
		s1, s2, name string
		survives     bool
	}{
		{"Show (2010)/Season 1", "Show (2010)/Season 2", "Other Show - S02E01.mkv", false},
		{"Show/S01", "Show/S02", "Other Show - S02E01.mkv", true},
		{"Anime/Show/Season 1", "Anime/Show/Season 2", "Other Show - S02E01.mkv", true},
		{"Show (2010)/Season 1", "Show (2010)/Season 2", "S02E01.mkv", true},
	} {
		t.Run(strings.ReplaceAll(c.s2+"|"+c.name, "/", "|"), func(t *testing.T) {
			other := c.s2 + "/" + c.name
			h, tv := rbSetup(t, []rbFile{
				{rel: c.s1 + "/Show - S01E01.mkv", tracked: [2]int{1, 1}},
				{rel: other},
			})
			mustExec(t, h.c, `UPDATE series SET year = 2010 WHERE id = ?`, h.seriesID)
			rcEpisodes(t, h, [2]int{2, 1})
			otherPath := filepath.Join(tv, filepath.FromSlash(other))
			_ = h.c.RescanSeries(h.ctx, h.seriesID)
			attached := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 2, 1).Path == otherPath
			if c.survives && attached {
				t.Errorf("rescan attached %s", other)
			}
			if _, err := h.c.SeriesRename(h.ctx, h.seriesID, nil); err != nil {
				t.Logf("rename: %v", err)
			}
			const rel = "Show.S02E01.1080p.WEB-DL-GRP"
			dl := h.download(t, rel, rel+".mkv")
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			h.c.importSeriesInto(h.ctx, s, dl, forceAll)
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			if c.survives {
				rbExists(t, tv, other)
			}
		})
	}
}

// A media server's backdrop or theme folder (Jellyfin "backdrops/theme.mkv") never keeps
// an ordinary show from its folder; one holding an episode-named video does.
func TestRevFBackdropsFolder(t *testing.T) {
	for _, c := range []struct {
		rel     string
		refused bool
	}{
		{"Show (2010)/backdrops/theme.mkv", false},
		{"Show (2010)/theme-music/theme.mp4", false},
		{"Show (2010)/backdrops/Other Show - S01E01.mkv", true},
	} {
		t.Run(strings.ReplaceAll(c.rel, "/", "|"), func(t *testing.T) {
			h, _ := rbSetup(t, []rbFile{
				{rel: "Show (2010)/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
				{rel: c.rel},
			})
			s, _ := h.c.series.Get(h.ctx, h.seriesID)
			_, _, err := h.c.seriesPlacement(h.ctx, s)
			if refused := err != nil; refused != c.refused {
				t.Errorf("placement = %v, want refused=%v", err, c.refused)
			}
		})
	}
}

// M: a show in <TV>/Kids/S01 beside an untracked flat show folder, a loose video of
// another show, or a show in season folders: its folder can't be told, so Move to folder
// refuses, and nothing of the other show's moves.
func TestRevFMoveRefusesACategory(t *testing.T) {
	for _, c := range []struct {
		name      string
		untracked []string
	}{
		{"flat show folder", []string{"Kids/Bluey/Bluey - S01E01.mkv", "Kids/Bluey/Bluey - S01E02.mkv"}},
		{"loose video", []string{"Kids/Peppa Pig - S01E01.mkv"}},
		{"show in season folders", []string{"Kids/Bluey/Season 1/Bluey - S01E01.mkv"}},
		{"show inside Other", []string{"Kids/Other/Bluey/Season 1/Bluey - S01E01.mkv"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newStallHarness(t)
			base := t.TempDir()
			tv, tv2 := filepath.Join(base, "tv"), filepath.Join(base, "tv2")
			for _, d := range []string{tv, tv2} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			roots := libroots.New(nil, config.Config{TVDir: tv})
			folders := libfolders.New(h.c.db, roots, filepath.Join(base, "data"))
			roots.WithExtras(folders.Extras)
			second, err := folders.Add(h.ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: tv2})
			if err != nil {
				t.Fatal(err)
			}
			svc := series.NewService(h.c.db, r6Meta("Show", 2010, 1, 3), tv, h.c.log)
			svc.SetFolders(folders)
			imp := library.NewImporter(tv, h.c.log)
			svc.SetSeasonFolderMatcher(imp.IsSeasonFolderName)
			h.c.series, h.c.imp = svc, imp
			s, err := svc.Add(h.ctx, 2316, "", true)
			if err != nil {
				t.Fatal(err)
			}
			setEpisodeFile(t, h.c, s.ID, 1, 1, libFile(t, filepath.Join(tv, "Kids", "S01", "Show - S01E01.mkv")), "Show.S01E01.1080p.WEB-DL-GRP")
			var others []string
			for _, u := range c.untracked {
				others = append(others, libFile(t, filepath.Join(tv, filepath.FromSlash(u))))
			}
			s, _ = svc.Get(h.ctx, s.ID)
			if _, _, err := h.c.seriesPlacement(h.ctx, s); err == nil {
				t.Error("placement accepted Kids as the show's folder")
			}
			m := &libfolders.Mover{DB: h.c.db, Folders: folders, Bin: library.SingleBin(filepath.Join(t.TempDir(), "bin")),
				ShowFolders: svc.ShowFoldersOf, Unclear: svc.MoveBlocked, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if _, err := m.Move(h.ctx, libfolders.MediaSeries, s.ID, second.ID, nil); err == nil {
				t.Error("Move to folder carried the category")
			}
			r6Gone(t, others, "after the move")
		})
	}
}
