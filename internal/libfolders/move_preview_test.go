package libfolders

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A film's folder moves only when OwnFolder says it's the film's own; without the check
// (nil) every film is refused, and nothing moves. Shows never ask.
func TestMoveAsksOwnFolderForFilmsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	anime := f.add(t, MediaSeries, "anime", RuleAnime)
	src := filmFixture(t, f)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))

	var asked []string
	m.OwnFolder = func(_ context.Context, id int64, dir string) string {
		asked = append(asked, dir)
		return "another video is inside it (Ronin.mkv)"
	}
	_, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil)
	if !errors.Is(err, ErrBadPath) || !strings.Contains(err.Error(), "isn't provably its own (another video is inside it (Ronin.mkv))") {
		t.Fatalf("move = %v, want it refused with the reason", err)
	}
	if len(asked) != 1 || asked[0] != src {
		t.Errorf("asked about %v, want %s", asked, src)
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
	gone(t, filepath.Join(kids.Path, "Heat (1995)"))
	pv, err := m.Preview(ctx, MediaMovie, 1, kids.ID)
	if err != nil || !strings.HasPrefix(pv.Refused, "Heat stays where it is") || len(pv.Moves) != 0 {
		t.Errorf("preview = %+v, %v; want the refusal, plainly", pv, err)
	}

	m.OwnFolder = nil
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBadPath) || !strings.Contains(err.Error(), "couldn't check") {
		t.Errorf("no check: %v, want refused", err)
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))

	// A show moves as it always did: OwnFolder (nil here) isn't asked.
	show := filepath.Join(f.tv, "Frieren (2023)")
	e1 := writeFile(t, filepath.Join(show, "Season 01", "Frieren - S01E01.mkv"), "e1")
	if _, err := f.st.DB().Exec(`INSERT INTO series (id, tmdb_id, title) VALUES (1, 209867, 'Frieren')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().Exec(`INSERT INTO episodes (series_id, season_number, episode_number, has_file, file_path) VALUES (1, 1, 1, 1, ?)`, e1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Move(ctx, MediaSeries, 1, anime.ID, nil); err != nil {
		t.Fatalf("show move: %v", err)
	}
	readFile(t, filepath.Join(anime.Path, "Frieren (2023)", "Season 01", "Frieren - S01E01.mkv"))
}

// The preview lists every file of every folder that would move; a move bound to it
// (MoveShown) refuses a folder holding a file it didn't list, on one drive and onto
// another, and moves nothing.
func TestMoveKeepsToThePreview(t *testing.T) {
	for _, cross := range []bool{false, true} {
		name := "rename"
		if cross {
			name = "copy"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			kids := f.add(t, MediaMovie, "kids", RuleKids)
			src := filmFixture(t, f)
			m, _ := f.mover(t, filepath.Join(f.base, "bin"))
			if cross {
				m.Rename = func(oldpath, newpath string) error {
					if oldpath == src {
						return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
					}
					return os.Rename(oldpath, newpath)
				}
			}
			pv, err := m.Preview(ctx, MediaMovie, 1, kids.ID)
			if err != nil || pv.Refused != "" || len(pv.Moves) != 1 {
				t.Fatalf("preview = %+v, %v", pv, err)
			}
			want := []string{
				filepath.Join(src, "Heat (1995) - 4K.mkv"), filepath.Join(src, "Heat (1995).en.srt"),
				filepath.Join(src, "Heat (1995).mkv"), filepath.Join(src, "poster.jpg"),
			}
			if got := pv.Moves[0].Files; strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("preview files = %v, want %v", got, want)
			}
			if pv.Moves[0].To != filepath.Join(kids.Path, "Heat (1995)") || pv.Moves[0].Bytes == 0 {
				t.Errorf("preview = %+v", pv.Moves[0])
			}

			// Something turns up after the preview: the move refuses, nothing moves.
			late := writeFile(t, filepath.Join(src, "Ronin (1998).mkv"), "another film")
			if _, err := m.MoveShown(ctx, MediaMovie, 1, kids.ID, pv.Moves[0].Files, nil); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "Ronin (1998).mkv") {
				t.Fatalf("move = %v, want it refused", err)
			}
			readFile(t, late)
			readFile(t, filepath.Join(src, "Heat (1995).mkv"))
			gone(t, filepath.Join(kids.Path, "Heat (1995)"))
			gone(t, stagingPath(kids.Path, "Heat (1995)"))

			// Exactly what was shown: it moves.
			if err := os.Remove(late); err != nil {
				t.Fatal(err)
			}
			res, err := m.MoveShown(ctx, MediaMovie, 1, kids.ID, pv.Moves[0].Files, nil)
			if err != nil || len(res.Moved) != 1 {
				t.Fatalf("move = %+v, %v", res, err)
			}
			readFile(t, filepath.Join(kids.Path, "Heat (1995)", "poster.jpg"))
			gone(t, src)
		})
	}
}

// The originals' retirement never removes a library folder, even an empty one, nor
// follows a link.
func TestRetireKeepsLibraryFolders(t *testing.T) {
	f := newFixture(t)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	if m.prunable(f.movies) {
		t.Errorf("the Movies folder is prunable")
	}
	empty := filepath.Join(f.movies, "Gone (2001)")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if !m.prunable(empty) {
		t.Errorf("an emptied film folder isn't prunable")
	}
	link := filepath.Join(f.movies, "Alias")
	if err := os.Symlink(empty, link); err == nil && m.prunable(link) {
		t.Errorf("a link is prunable")
	}
	// retireCopied on a folder that is itself the library folder leaves it.
	writeFile(t, filepath.Join(f.movies, "x.nfo"), "x")
	left, late := m.retireCopied(f.movies, map[string]int64{"x.nfo": 1})
	if left != 0 {
		t.Errorf("left = %d", left)
	}
	_ = late
	if fi, err := os.Stat(f.movies); err != nil || !fi.IsDir() {
		t.Errorf("the Movies folder went: %v", err)
	}
}
