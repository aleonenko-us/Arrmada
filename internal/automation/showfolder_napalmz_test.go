package automation

import (
	"context"
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

// The #56 reporter's library, end to end: every show as /storage/TV Shows/<Show>/S12, the
// season folder template S{season00}, show folders without a year. Beside it, another show
// with the same title and a year — tracked, its files named exactly like this show's.
// Temp dirs only.

// napMeta is TMDB for the two Pechino Express shows: 1 (2012, the reporter's) and 2 (2019).
type napMeta struct{}

func (napMeta) Available() bool { return true }
func (napMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (napMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	year := 2012
	if id == 2 {
		year = 2019
	}
	var seasons []metadata.SeasonDetails
	for s := 11; s <= 13; s++ {
		var eps []metadata.EpisodeDetails
		for e := 1; e <= 6; e++ {
			eps = append(eps, metadata.EpisodeDetails{EpisodeNumber: e, AirDate: "2020-01-01"})
		}
		seasons = append(seasons, metadata.SeasonDetails{SeasonNumber: s, Episodes: eps})
	}
	return &metadata.SeriesDetails{
		SeriesResult:    metadata.SeriesResult{TMDBID: id, Title: "Pechino Express", Year: year},
		NumberingSource: "tmdb",
		Seasons:         seasons,
	}, nil
}

type napFixture struct {
	h        *lifecycleHarness
	tv, tv2  string
	id, sib  int64
	folders  *libfolders.Service
	tv2ID    int64
	sibFiles []string
}

func napSetup(t *testing.T) *napFixture {
	t.Helper()
	h := newLifecycleHarness(t)
	base := t.TempDir()
	tv := filepath.Join(base, "storage", "TV Shows")
	tv2 := filepath.Join(base, "storage", "TV Shows 2")
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
	svc := series.NewService(h.c.db, napMeta{}, tv, h.c.log)
	svc.SetFolders(folders)
	imp := library.NewImporter(tv, h.c.log)
	imp.SetSeriesNaming(renameSeriesNaming{library.SeriesNaming{
		Folder: "{title}", SeasonFolder: "S{season00}", EpisodeFile: "{title} - {episode}",
	}})
	svc.SetSeasonFolderMatcher(imp.IsSeasonFolderName)
	h.c.series, h.c.imp = svc, imp

	f := &napFixture{h: h, tv: tv, tv2: tv2, folders: folders, tv2ID: second.ID}
	add := func(tmdb int) int64 {
		s, err := svc.Add(h.ctx, tmdb, "", true)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	f.id, f.sib = add(1), add(2)
	for _, ep := range [][2]int{{11, 1}, {11, 2}, {12, 1}, {12, 2}} {
		p := filepath.Join(tv, "Pechino Express", "S"+pad2(ep[0]), "Pechino Express - S"+pad2(ep[0])+"E"+pad2(ep[1])+".mkv")
		setEpisodeFile(t, h.c, f.id, ep[0], ep[1], libFile(t, p), "Pechino.Express.S12.1080p.WEB-DL-GRP")
	}
	for _, ep := range [][2]int{{12, 1}, {12, 3}} {
		p := filepath.Join(tv, "Pechino Express (2019)", "S"+pad2(ep[0]), "Pechino Express - S"+pad2(ep[0])+"E"+pad2(ep[1])+".mkv")
		setEpisodeFile(t, h.c, f.sib, ep[0], ep[1], libFile(t, p), "Pechino.Express.2019.S12.1080p.WEB-DL-GRP")
		f.sibFiles = append(f.sibFiles, p)
	}
	h.seriesID = f.id
	return f
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// sibUntouched fails when the other Pechino Express's files or records changed.
func (f *napFixture) sibUntouched(t *testing.T, when string) {
	t.Helper()
	for _, p := range f.sibFiles {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: the 2019 show's %s is gone: %v", when, p, err)
		}
	}
	if cur := f.h.c.series.CurrentEpisodeFile(f.h.ctx, f.sib, 12, 1); cur.Path != f.sibFiles[0] {
		t.Errorf("%s: the 2019 show's S12E01 = %q", when, cur.Path)
	}
	if cur := f.h.c.series.CurrentEpisodeFile(f.h.ctx, f.sib, 12, 3); cur.Path != f.sibFiles[1] {
		t.Errorf("%s: the 2019 show's S12E03 = %q", when, cur.Path)
	}
}

func (f *napFixture) importEp(t *testing.T, season, ep int) string {
	t.Helper()
	name := "Pechino.Express.S" + pad2(season) + "E" + pad2(ep) + ".1080p.WEB-DL-GRP"
	dl := f.h.download(t, name, name+".mkv")
	s, err := f.h.c.series.Get(f.h.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.h.c.seriesPlacement(f.h.ctx, s); err != nil {
		t.Fatalf("placement refused: %v", err)
	}
	if _, _, _, failed := f.h.c.importSeriesInto(f.h.ctx, s, dl, forceAll); failed != 0 {
		t.Fatalf("import of %s failed", name)
	}
	return f.h.c.series.CurrentEpisodeFile(f.h.ctx, f.id, season, ep).Path
}

func TestNapalmzLayoutEndToEnd(t *testing.T) {
	f := napSetup(t)
	h, ctx := f.h, f.h.ctx
	own := filepath.Join(f.tv, "Pechino Express")

	if info := h.c.series.ShowFolderOf(ctx, f.id); info.Unknown || info.Path != own {
		t.Fatalf("ShowFolderOf = %+v, want %s", info, own)
	}
	if info := h.c.series.ShowFolderOf(ctx, f.sib); info.Unknown || info.Path != filepath.Join(f.tv, "Pechino Express (2019)") {
		t.Fatalf("the 2019 show's folder = %+v", info)
	}

	// Import: an episode of an existing season goes into its S12, a new season into S13.
	if got := f.importEp(t, 12, 3); got != filepath.Join(own, "S12", "Pechino Express - S12E03.mkv") {
		t.Errorf("S12E03 landed at %q", got)
	}
	if got := f.importEp(t, 13, 1); got != filepath.Join(own, "S13", "Pechino Express - S13E01.mkv") {
		t.Errorf("S13E01 landed at %q, want a new S13 inside the show's folder", got)
	}
	for _, bad := range []string{filepath.Join(f.tv, "S12"), filepath.Join(f.tv, "S13"), filepath.Join(own, "Season 12"), filepath.Join(own, "Season 13")} {
		noDir(t, bad, "a parallel or misplaced season folder")
	}
	f.sibUntouched(t, "after the imports")

	// Rescan: everything stays attached where it is.
	if err := h.c.RescanSeries(ctx, f.id); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	for _, ep := range [][2]int{{11, 1}, {11, 2}, {12, 1}, {12, 2}, {12, 3}, {13, 1}} {
		if cur := h.c.series.CurrentEpisodeFile(ctx, f.id, ep[0], ep[1]); !strings.HasPrefix(cur.Path, own+string(filepath.Separator)) {
			t.Errorf("after rescan S%02dE%02d = %q", ep[0], ep[1], cur.Path)
		}
	}
	f.sibUntouched(t, "after the rescan")

	// Rename preview: the files already follow the scheme; nothing of the 2019 show's.
	items, err := h.c.SeriesRenamePreview(ctx, f.id)
	if err != nil {
		t.Fatalf("rename preview: %v", err)
	}
	for _, it := range items {
		if !strings.HasPrefix(it.From, own) || !strings.HasPrefix(it.To, own) {
			t.Errorf("rename preview moves %s -> %s", it.From, it.To)
		}
	}

	// Duplicates: an extra copy in S12 is listed; the 2019 show's same-named files never.
	extra := libFile(t, filepath.Join(own, "S12", "Pechino.Express.S12E01.720p.HDTV-GRP.mkv"))
	dups, err := h.c.SeriesDuplicates(ctx, f.id)
	if err != nil {
		t.Fatalf("duplicates: %v", err)
	}
	listed, drop := false, ""
	for _, d := range dups {
		for _, c := range append([]DuplicateCopy{d.Keeping}, d.Extras...) {
			if c.Path == extra {
				listed = true
			}
			if !strings.HasPrefix(c.Path, own+string(filepath.Separator)) {
				t.Errorf("duplicates lists %s", c.Path)
			}
		}
		if d.Season == 12 && d.Episode == 1 && len(d.Extras) == 1 {
			drop = d.Extras[0].Path
		}
	}
	if !listed || drop == "" {
		t.Fatalf("the two S12E01 copies aren't listed as duplicates: %+v", dups)
	}
	if err := h.c.DeleteSeriesDuplicate(ctx, f.id, drop); err != nil {
		t.Errorf("deleting the extra copy: %v", err)
	}
	if err := h.c.RescanSeries(ctx, f.id); err != nil {
		t.Fatalf("rescan after the duplicate went: %v", err)
	}
	f.sibUntouched(t, "after the duplicates")

	// Move to folder: the whole show folder moves to the second TV folder.
	m := &libfolders.Mover{
		DB: h.c.db, Folders: f.folders, Bin: library.SingleBin(filepath.Join(t.TempDir(), "bin")),
		ShowFolders: h.c.series.ShowFoldersOf, Unclear: h.c.series.MoveBlocked, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if _, err := m.Move(ctx, libfolders.MediaSeries, f.id, f.tv2ID, nil); err != nil {
		t.Fatalf("move: %v", err)
	}
	moved := filepath.Join(f.tv2, "Pechino Express")
	for _, ep := range [][2]int{{11, 1}, {12, 3}, {13, 1}} {
		cur := h.c.series.CurrentEpisodeFile(ctx, f.id, ep[0], ep[1])
		if !strings.HasPrefix(cur.Path, moved+string(filepath.Separator)) {
			t.Errorf("after the move S%02dE%02d = %q", ep[0], ep[1], cur.Path)
		} else if _, err := os.Stat(cur.Path); err != nil {
			t.Errorf("after the move: %v", err)
		}
	}
	noDir(t, own, "the old folder after the move")
	f.sibUntouched(t, "after the move")
	if got := f.importEp(t, 13, 2); got != filepath.Join(moved, "S13", "Pechino Express - S13E02.mkv") {
		t.Errorf("after the move S13E02 landed at %q", got)
	}

	// Delete with files: only the show's own; the 2019 show stays whole.
	if _, err := h.c.series.Delete(ctx, f.id, true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	f.sibUntouched(t, "after the delete")
	// Its season folders go once emptied; the show's folder above "S12" stays, as in v0.3.2.
	for _, sn := range []string{"S11", "S12", "S13"} {
		noDir(t, filepath.Join(moved, sn), "an emptied season folder")
	}
	if entries, err := os.ReadDir(moved); err == nil && len(entries) != 0 {
		t.Errorf("the show's folder still holds %d entries", len(entries))
	}
}
