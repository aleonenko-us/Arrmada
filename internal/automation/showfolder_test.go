package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

// libFile writes a sparse 60 MB episode file at path.
func libFile(t *testing.T, path string) string {
	t.Helper()
	sparseFile(t, path, 60<<20)
	return path
}

// setEpisodeFile gives an episode row a file, adding the row when the listing lacks it.
func setEpisodeFile(t *testing.T, c *Coordinator, seriesID int64, season, ep int, path, release string) {
	t.Helper()
	mustExec(t, c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, seriesID, season)
	mustExec(t, c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored)
		VALUES (?, ?, ?, '2020-01-01', 1)`, seriesID, season, ep)
	mustExec(t, c, `UPDATE episodes SET has_file = 1, file_path = ?, size_bytes = ?, source_release = ?
		WHERE series_id = ? AND season_number = ? AND episode_number = ?`, path, 60<<20, release, seriesID, season, ep)
}

// noDir fails when dir exists.
func noDir(t *testing.T, dir, why string) {
	t.Helper()
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("%s exists — %s", dir, why)
	}
}

// The #56 report, end to end: Pechino Express lives in <TV>/Pechino Express with season
// folders named "S12" and "S13", and one episode the old bug already put in <TV>/S13/Season
// 12. A season 13 upgrade lands in the show's own "S13" folder — not <TV>/S13/Season 13,
// and not a parallel "Season 13" beside the existing "S13".
func TestSeasonUpgradeLandsInTheShowsOwnS13Folder(t *testing.T) {
	h := newStallHarness(t)
	tv := t.TempDir()
	meta := &importMeta{d: metadata.SeriesDetails{
		SeriesResult:    metadata.SeriesResult{TMDBID: 56, Title: "Pechino Express", Year: 2012},
		NumberingSource: "tmdb",
		Seasons: []metadata.SeasonDetails{
			{SeasonNumber: 12, Episodes: []metadata.EpisodeDetails{{EpisodeNumber: 1, AirDate: "2024-01-01"}, {EpisodeNumber: 2, AirDate: "2024-01-08"}}},
			{SeasonNumber: 13, Episodes: []metadata.EpisodeDetails{{EpisodeNumber: 1, AirDate: "2025-01-01"}, {EpisodeNumber: 2, AirDate: "2025-01-08"}}},
		},
	}}
	h.c.series = series.NewService(h.c.db, meta, tv, h.c.log)
	h.c.imp = library.NewImporter(tv, h.c.log)
	s, err := h.c.series.Add(h.ctx, 56, "", true)
	if err != nil {
		t.Fatal(err)
	}
	show := filepath.Join(tv, "Pechino Express")
	setEpisodeFile(t, h.c, s.ID, 12, 1, libFile(t, filepath.Join(show, "S12", "Pechino Express - S12E01 - 720p.mkv")), "Pechino.Express.S12E01.720p.WEB-DL-OLD")
	setEpisodeFile(t, h.c, s.ID, 13, 1, libFile(t, filepath.Join(show, "S13", "Pechino Express - S13E01 - 720p.mkv")), "Pechino.Express.S13E01.720p.WEB-DL-OLD")
	setEpisodeFile(t, h.c, s.ID, 13, 2, libFile(t, filepath.Join(show, "S13", "Pechino Express - S13E02 - 720p.mkv")), "Pechino.Express.S13E02.720p.WEB-DL-OLD")
	// Where the old bug put an upgrade.
	setEpisodeFile(t, h.c, s.ID, 12, 2, libFile(t, filepath.Join(tv, "S13", "Season 12", "Pechino Express - S12E02 - 1080p.mkv")), "Pechino.Express.S12E02.1080p.WEB-DL-NEW")

	const pack = "Pechino.Express.S13.1080p.WEB-DL.x264-GRP"
	dl := filepath.Join(t.TempDir(), pack)
	libFile(t, filepath.Join(dl, "Pechino.Express.S13E01.1080p.WEB-DL.x264-GRP.mkv"))
	libFile(t, filepath.Join(dl, "Pechino.Express.S13E02.1080p.WEB-DL.x264-GRP.mkv"))
	s, err = h.c.series.Get(h.ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll)
	if failed != 0 || len(placed) != 2 {
		t.Fatalf("placed %v, failed %d — want both season 13 episodes", placed, failed)
	}
	for _, ep := range []int{1, 2} {
		cur := h.c.series.CurrentEpisodeFile(h.ctx, s.ID, 13, ep)
		if filepath.Dir(cur.Path) != filepath.Join(show, "S13") {
			t.Errorf("S13E%02d landed at %s, want inside %s", ep, cur.Path, filepath.Join(show, "S13"))
		}
	}
	noDir(t, filepath.Join(tv, "S13", "Season 13"), "the upgrade followed the season folder out of the show")
	noDir(t, filepath.Join(show, "Season 13"), "a parallel season folder was made beside S13")
}

// A show whose only files are in a folder other shows use — the shared <TV>/S01 the bug
// made — isn't imported into it: the download goes to Review saying why, and nothing lands.
func TestImportIntoSharedFolderGoesToReview(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	shared := filepath.Join(tv, "S01")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(shared, "Season 1", "Show - S01E01.mkv")), "Show.S01E01.720p")
	res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (99, 'Other', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	setEpisodeFile(t, h.c, other, 4, 1, libFile(t, filepath.Join(shared, "Season 4", "Other - S04E01.mkv")), "Other.S04E01.720p")

	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	hash := h.completedTorrent(name, seriesCategory, dl)
	h.seriesGrab(t, name, hash)
	h.c.ImportSeriesDownloads(h.ctx)

	r := reviewFor(t, h.c, hash)
	if r.ReasonCode != ReasonImportFailed || !strings.Contains(r.Reason, "Can't tell which folder Show lives in") {
		t.Errorf("review = %q (%s), want an import_failed saying the folder can't be told", r.Reason, r.ReasonCode)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != "" {
		t.Errorf("S01E02 was placed at %s", cur.Path)
	}
	entries, _ := os.ReadDir(filepath.Join(shared, "Season 1"))
	if len(entries) != 1 {
		t.Errorf("the shared folder now holds %d files, want only the one that was there", len(entries))
	}
	// Importing it from Review is refused the same way, as are rescans, renames and the
	// duplicate check, which would walk the shared folder.
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if _, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); failed != 1 {
		t.Errorf("importSeriesInto failed=%d, want the one file refused", failed)
	}
	if err := h.c.ManualImportSeries(h.ctx, h.seriesID, filepath.Join(dl, name+".mkv")); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("manual import: %v", err)
	}
	var refused *RescanRefusedError
	if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.As(err, &refused) {
		t.Errorf("rescan: %v, want refused", err)
	}
	if _, err := h.c.SeriesRenamePreview(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("rename preview: %v", err)
	}
	if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("duplicates: %v", err)
	}
}

// A show kept inside a category folder (<TV>/Anime/Show) gets its next episode in its own
// season folder, not a "Season 1" made in the category folder.
func TestImportIntoAShowInsideACategoryFolder(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Anime", "Show")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "Season 1", "Show - S01E01.mkv")), "Show.S01E01")
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); failed != 0 || len(placed) != 1 {
		t.Fatalf("placed %v failed %d", placed, failed)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(cur.Path) != filepath.Join(own, "Season 1") {
		t.Errorf("S01E02 at %s, want in %s", cur.Path, filepath.Join(own, "Season 1"))
	}
	noDir(t, filepath.Join(tv, "Anime", "Season 1"), "the import went into the category folder")
}

// A flat show inside a category under another name (romaji) imports into its own folder
// when its files carry that name, and is held for Review when nothing ties the folder to
// the show — never into the category folder.
func TestImportFlatShowInsideACategory(t *testing.T) {
	t.Run("files named like the folder", func(t *testing.T) {
		h := newLifecycleHarness(t)
		tv := h.c.imp.TVRoot()
		own := filepath.Join(tv, "Anime", "Sousou no Show")
		setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "Sousou no Show - S01E01.mkv")), "Show.S01E01")
		const name = "Show.S01E02.1080p.WEB-DL-GRP"
		dl := h.download(t, name, name+".mkv")
		s, _ := h.c.series.Get(h.ctx, h.seriesID)
		if placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll); failed != 0 || len(placed) != 1 {
			t.Fatalf("placed %v failed %d", placed, failed)
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); filepath.Dir(filepath.Dir(cur.Path)) != own {
			t.Errorf("S01E02 at %s, want inside %s", cur.Path, own)
		}
		noDir(t, filepath.Join(tv, "Anime", "Season 1"), "the import went into the category folder")
	})
	t.Run("named like a download: refused", func(t *testing.T) {
		h := newLifecycleHarness(t)
		tv := h.c.imp.TVRoot()
		setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(tv, "Anime", "Show 1080p", "Show - S01E01.mkv")), "Show.S01E01")
		s, _ := h.c.series.Get(h.ctx, h.seriesID)
		if _, _, err := h.c.seriesPlacement(h.ctx, s); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("placement = %v, want refused", err)
		}
	})
}

// Moving a misplaced file back by hand and rescanning is enough: the rescan finds it in
// the show's folder, and the next import goes there.
func TestRescanAfterMovingMisplacedFileBackByHand(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Show")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "S01", "Show - S01E01.mkv")), "Show.S01E01")
	lost := libFile(t, filepath.Join(tv, "S01", "Season 1", "Show - S01E02.mkv"))
	setEpisodeFile(t, h.c, h.seriesID, 1, 2, lost, "Show.S01E02")
	back := filepath.Join(own, "S01", "Show - S01E02.mkv")
	if err := os.Rename(lost, back); err != nil {
		t.Fatal(err)
	}
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != back {
		t.Errorf("S01E02 = %q after the rescan, want %s", cur.Path, back)
	}
}

// Files the bug misplaced, even outnumbering the show's own, don't pull the next import
// out of the show's folder.
func TestImportFollowsTheShowsFolderPastMisplacedFiles(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Show (2020)")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "Stagione 1", "Show - S01E01.mkv")), "Show.S01E01.720p")
	for ep := 1; ep <= 3; ep++ {
		p := filepath.Join(tv, "S02", "Season 2", "Show - S02E0"+string(rune('0'+ep))+".mkv")
		setEpisodeFile(t, h.c, h.seriesID, 2, ep, libFile(t, p), "Show.S02.720p")
	}
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll)
	if failed != 0 || len(placed) != 1 {
		t.Fatalf("placed %v failed %d", placed, failed)
	}
	cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2)
	// The season folder the show's season 1 files are in, whatever it's called: never a
	// parallel "Season 1" beside it.
	if filepath.Dir(cur.Path) != filepath.Join(own, "Stagione 1") {
		t.Errorf("S01E02 at %s, want in %s", cur.Path, filepath.Join(own, "Stagione 1"))
	}
	noDir(t, filepath.Join(own, "Season 1"), "a parallel season folder")
	noDir(t, filepath.Join(tv, "S02", "Season 1"), "the import followed the misplaced files")
	noDir(t, filepath.Join(tv, "Stagione 1"), "the season folder was taken for the show")
}
