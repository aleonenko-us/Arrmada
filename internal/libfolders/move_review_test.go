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

// The staging copy is the one thing a move deletes outright, so the delete only ever
// touches a dot-named staging folder directly inside the destination library folder.
func TestDiscardStagingIsNarrow(t *testing.T) {
	root := t.TempDir()
	ok := stagingPath(root, "Heat (1995)")
	if !strings.HasPrefix(filepath.Base(ok), ".") {
		t.Errorf("staging folder %q isn't hidden", ok)
	}
	writeFile(t, filepath.Join(ok, "Heat.mkv"), "copy")
	for _, bad := range []string{
		root,                               // the library folder itself
		filepath.Join(root, "Heat (1995)"), // a real title folder
		filepath.Join(root, "sub", filepath.Base(ok)), // a staging name, but not directly in the root
		filepath.Join(root, stagingPrefix),            // the bare prefix
		filepath.Join(t.TempDir(), filepath.Base(ok)), // another folder's
	} {
		if err := discardStaging(root, bad); !errors.Is(err, ErrNotStaging) {
			t.Errorf("discardStaging(%q) = %v, want a refusal", bad, err)
		}
	}
	if err := discardStaging(root, ok); err != nil {
		t.Fatal(err)
	}
	gone(t, ok)
	if got := stagingPath(root, filepath.Join("A", "Heat (1995)")); filepath.Dir(got) != root {
		t.Errorf("a nested title's staging folder %q isn't directly in the library folder", got)
	}
}

func exdevFor(src string) func(string, string) error {
	return func(oldpath, newpath string) error {
		if oldpath == src {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
		return os.Rename(oldpath, newpath)
	}
}

// A copy that stops part-way (cancelled, timed out, failed) leaves no copy anywhere — not in
// the destination, not in its recycle bin — and the originals as they were.
func TestCancelledCopyIsDeletedNotBinned(t *testing.T) {
	f := newFixture(t)
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	bin := filepath.Join(f.base, "bin")
	m, _ := f.mover(t, bin)
	m.Rename = exdevFor(src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := progressFunc(func(_ float64, msg string) {
		if strings.HasPrefix(msg, "Copying") {
			cancel()
		}
	})
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("move = %v, want it cancelled", err)
	}
	gone(t, stagingPath(kids.Path, "Heat (1995)"))
	gone(t, filepath.Join(kids.Path, "Heat (1995)"))
	gone(t, filepath.Join(kids.Path, ".arrmada-recycle"))
	gone(t, bin)
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
}

// A drive without room for the copy (plus a margin) is refused before anything is copied.
func TestCopyRefusedWithoutRoom(t *testing.T) {
	f := newFixture(t)
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	m.Rename = exdevFor(src)
	m.free = func(string) (uint64, bool) { return 1 << 20, true } // 1 MB
	if _, err := m.Move(context.Background(), MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("move = %v, want no room", err)
	}
	gone(t, stagingPath(kids.Path, "Heat (1995)"))
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
}

// A staging copy a crash left behind is hidden from scans and removed by the next move into
// that folder, instead of blocking it.
func TestLeftoverStagingIsClearedByTheNextMove(t *testing.T) {
	f := newFixture(t)
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	left := stagingPath(kids.Path, "Heat (1995)")
	writeFile(t, filepath.Join(left, "Heat (1995).mkv"), "half a copy")
	// Something that only looks similar is never touched.
	keep := writeFile(t, filepath.Join(kids.Path, "arrmada-moving-notes.txt"), "mine")
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	m.Rename = exdevFor(src)
	if _, err := m.Move(context.Background(), MediaMovie, 1, kids.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(kids.Path, "Heat (1995)", "Heat (1995).mkv")); got != "the film" {
		t.Errorf("moved file holds %q", got)
	}
	gone(t, left)
	readFile(t, keep)
}

// Two of a title's folders that would land on the same place in the destination are
// refused before anything moves.
func TestPlanRefusesTwoFoldersOntoOne(t *testing.T) {
	f := newFixture(t)
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	other := f.add(t, MediaMovie, "family", RuleNone)
	src := filmFixture(t, f)
	twin := writeFile(t, filepath.Join(other.Path, "Heat (1995)", "Heat (1995) - DC.mkv"), "dc")
	if _, err := f.st.DB().Exec(`UPDATE movie_versions SET file_path = ? WHERE movie_id = 1`, twin); err != nil {
		t.Fatal(err)
	}
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	if _, err := m.Move(context.Background(), MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBadPath) || !strings.Contains(err.Error(), "both become") {
		t.Fatalf("move = %v, want the collision refused", err)
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
	readFile(t, twin)
	gone(t, filepath.Join(kids.Path, "Heat (1995)"))
}

// A folder can't be removed while a move puts files into it, and a move can't start into
// one that's gone; the database rewrite checks again inside its transaction.
func TestRemoveAndMoveDontCross(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	done, err := f.svc.beginMove(ctx, MediaMovie, kids.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Remove(ctx, kids.ID); !errors.Is(err, ErrMoving) {
		t.Errorf("remove during a move = %v", err)
	}
	done()
	done() // twice is harmless
	if err := f.svc.Remove(ctx, kids.ID); err != nil {
		t.Fatalf("remove after the move = %v", err)
	}
	if _, err := f.svc.beginMove(ctx, MediaMovie, kids.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a move into a removed folder = %v", err)
	}
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	if _, err := m.repoint(ctx, MediaMovie, kids.ID, "/a", "/b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("repoint into a removed folder = %v", err)
	}
}

// Retiring the originals takes only what was copied: a file that turned up after (or
// changed size) stays where it is.
func TestRetireTakesOnlyWhatWasCopied(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.movies, "Heat (1995)")
	a := writeFile(t, filepath.Join(dir, "Heat.mkv"), "film")
	b := writeFile(t, filepath.Join(dir, "Heat.de.srt"), "late")
	c := writeFile(t, filepath.Join(dir, "Heat.en.srt"), "grown since")
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	left, late := m.retireCopied(dir, map[string]int64{"Heat.mkv": 4, "Heat.en.srt": 2})
	if left != 0 || late != 2 {
		t.Errorf("left %d, late %d", left, late)
	}
	gone(t, a)
	readFile(t, b)
	readFile(t, c)
}
