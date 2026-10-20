package libfolders

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// progressFunc adapts a func to Progress.
type progressFunc func(pct float64, msg string)

func (f progressFunc) Set(pct float64, msg string) { f(pct, msg) }

// A file that turns up in the original while it's being copied (an import that slipped in)
// stops the move before anything is retired: the copy is taken out, the originals stay.
func TestMoveRefusesWhenTheOriginalChangesMidCopy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	m.Rename = func(oldpath, newpath string) error {
		if oldpath == src {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
		return os.Rename(oldpath, newpath)
	}
	var once sync.Once
	p := progressFunc(func(_ float64, msg string) {
		if strings.HasPrefix(msg, "Copying") {
			once.Do(func() { writeFile(t, filepath.Join(src, "Heat (1995).de.srt"), "late subtitles") })
		}
	})
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, p); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("move = %v, want it refused", err)
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
	readFile(t, filepath.Join(src, "Heat (1995).de.srt"))
	gone(t, filepath.Join(kids.Path, "Heat (1995)"))
	gone(t, stagingPath(kids.Path, "Heat (1995)"))
	if got := queryString(t, f, `SELECT movie_file_path FROM movies WHERE id = 1`); got != filepath.Join(src, "Heat (1995).mkv") {
		t.Errorf("movie path = %q", got)
	}
}

// Searches of the title are held for the whole move and let go after; a title being
// searched right now doesn't move.
func TestMoveHoldsSearches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	filmFixture(t, f)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	held, released := 0, 0
	m.Hold = func(media string, id int64) (func(), bool) {
		if media != MediaMovie || id != 1 {
			t.Errorf("held %s %d", media, id)
		}
		held++
		return func() { released++ }, true
	}
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); err != nil {
		t.Fatal(err)
	}
	if held != 1 || released != 1 {
		t.Errorf("held %d, released %d", held, released)
	}
	m.Hold = func(string, int64) (func(), bool) { return func() {}, false }
	if _, err := m.Move(ctx, MediaMovie, 1, DefaultID, nil); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "search") {
		t.Errorf("move while searching = %v", err)
	}
}
