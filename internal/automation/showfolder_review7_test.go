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
	"github.com/tristenlammi/arrmada/internal/series"
)

// Move to folder never carries an episode the show hasn't recorded whose name doesn't say
// it's the show's — in any layout, at any size, sample or not — and says which file blocks
// it. The show's own recorded files move whatever they're called; a theme doesn't count.
// Temp dirs only.
func TestRevHMoveRefusesAnEpisodeNotTheShowsInside(t *testing.T) {
	for _, c := range []struct {
		name, title string
		recorded    []string // the show's own, recorded
		untracked   string   // "" = none
		refused     bool
		size        int64 // the untracked file's size; 0 = 60 MB
	}{
		{"ops12-other-in-S01", "Pechino Express", []string{"Pechino Express/S01/Pechino Express - S01E01.mkv"}, "Pechino Express/S01/Other Show - S01E03.mkv", true, 0},
		{"ops12-other-in-category-S01", "The Pitt", []string{"Anime/The Pitt/S01/The Pitt - S01E01.mkv"}, "Anime/The Pitt/S01/Other Show - S01E03.mkv", true, 0},
		{"ops12-other-in-season-n", "Show", []string{"Show (2010)/Season 1/Show - S01E01.mkv"}, "Show (2010)/Season 1/Other Show - S01E03.mkv", true, 0},
		{"romaji recorded files move", "Attack on Titan", []string{"Attack on Titan (2013)/Season 1/Shingeki no Kyojin - S01E01.mkv", "Attack on Titan (2013)/Season 1/Shingeki no Kyojin - S01E02.mkv"}, "", false, 0},
		{"short-name recorded files move", "Frieren Beyond Journeys End", []string{"Frieren (2023)/Season 1/Frieren - S01E01.mkv"}, "", false, 0},
		{"own unrecorded odd name refuses", "Attack on Titan", []string{"Attack on Titan (2013)/Season 1/Attack on Titan - S01E01.mkv"}, "Attack on Titan (2013)/Season 1/Shingeki no Kyojin - S01E05.mkv", true, 0},
		{"theme in backdrops", "Show", []string{"Show (2010)/Season 1/Show - S01E01.mkv"}, "Show (2010)/backdrops/theme.mkv", false, 0},
		{"small theme in backdrops", "Show", []string{"Show (2010)/Season 1/Show - S01E01.mkv"}, "Show (2010)/backdrops/theme.mkv", false, 4},
		{"small other-show episode in S01", "Show", []string{"Show/S01/Show - S01E01.mkv"}, "Show/S01/Peppa Pig - S01E02.mkv", true, 40 << 20},
		{"small other-show episode in Season 1", "Show", []string{"Show (2010)/Season 1/Show - S01E01.mkv"}, "Show (2010)/Season 1/Peppa Pig - S01E02.mkv", true, 40 << 20},
		{"other-show sample", "Show", []string{"Show/S01/Show - S01E01.mkv"}, "Show/S01/Other Show - S01E02 - sample.mkv", true, 0},
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
			svc := series.NewService(h.c.db, r6Meta(c.title, 2010, 1, 6), tv, h.c.log)
			svc.SetFolders(folders)
			imp := library.NewImporter(tv, h.c.log)
			svc.SetSeasonFolderMatcher(imp.IsSeasonFolderName)
			h.c.series, h.c.imp = svc, imp
			s, err := svc.Add(h.ctx, 2316, "", true)
			if err != nil {
				t.Fatal(err)
			}
			for i, rel := range c.recorded {
				setEpisodeFile(t, h.c, s.ID, 1, i+1, libFile(t, filepath.Join(tv, filepath.FromSlash(rel))), "x")
			}
			if c.untracked != "" {
				size := c.size
				if size == 0 {
					size = 60 << 20
				}
				sparseFile(t, filepath.Join(tv, filepath.FromSlash(c.untracked)), size)
			}
			m := &libfolders.Mover{DB: h.c.db, Folders: folders, Bin: library.SingleBin(filepath.Join(t.TempDir(), "bin")),
				ShowFolders: svc.ShowFoldersOf, Unclear: svc.MoveBlocked, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			_, err = m.Move(h.ctx, libfolders.MediaSeries, s.ID, second.ID, nil)
			if c.refused {
				if err == nil {
					t.Fatal("Move to folder carried the folder")
				}
				if !strings.Contains(err.Error(), filepath.Base(c.untracked)) {
					t.Errorf("the refusal doesn't name the file: %v", err)
				}
				rbExists(t, tv, c.untracked)
				return
			}
			if err != nil {
				t.Fatalf("move: %v", err)
			}
			for i := range c.recorded {
				if cur := svc.CurrentEpisodeFile(h.ctx, s.ID, 1, i+1); !strings.HasPrefix(cur.Path, tv2+string(filepath.Separator)) {
					t.Errorf("S01E%02d = %q after the move", i+1, cur.Path)
				}
			}
		})
	}
}
