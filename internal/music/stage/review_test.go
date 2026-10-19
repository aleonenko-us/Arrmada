package stage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// Findings of the independent review of MUS-33/34 (2026-10-20), kept as tests.

// A rename whose target goes through a symlinked folder that points back at the source's
// folder reaches the same file: it's refused, and the file keeps its name.
func TestRenameThroughSymlinkedFolderKeepsTheFile(t *testing.T) {
	w := newWorld(t)
	src := w.write(filepath.Join(w.root, "Beatles", "Abbey Road", "01.flac"), "AUDIO")
	if err := os.Symlink(filepath.Join(w.root, "Beatles"), filepath.Join(w.root, "The Beatles")); err != nil {
		t.Skip(err)
	}
	final := filepath.Join(w.root, "The Beatles", "Abbey Road", "01.flac")
	j, err := Start(w.root, Manifest{Kind: KindRename, Items: []Item{{Source: src, Final: final}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Place(0); !errors.Is(err, ErrSameFile) {
		t.Errorf("Place: %v, want ErrSameFile", err)
	}
	if b, err := os.ReadFile(src); err != nil || string(b) != "AUDIO" {
		t.Fatalf("DATA LOSS: the file is gone (%v)", err)
	}
	if !SameEntry(src, final) {
		t.Error("SameEntry doesn't see the two paths as one name")
	}
	_ = j.Settle(0)
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

// Two real names for one file (a hard link in another folder) are two entries: moving one
// onto the other drops the old name, and the file stays at the new one.
func TestMoveOntoASecondNameDropsTheOldOne(t *testing.T) {
	w := newWorld(t)
	a := w.write(filepath.Join(w.root, "A", "01.flac"), "AUDIO")
	b := filepath.Join(w.root, "B", "01.flac")
	_ = os.MkdirAll(filepath.Dir(b), 0o755)
	if err := os.Link(a, b); err != nil {
		t.Skip(err)
	}
	if SameEntry(a, b) {
		t.Error("two hard links read as one entry")
	}
	if err := moveNoClobber(a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Error("the old name is still there")
	}
	if got, _ := os.ReadFile(b); string(got) != "AUDIO" {
		t.Error("the file is gone")
	}
	// The same name in one folder, given twice, is one entry: never removed.
	if err := moveNoClobber(b, b); err != nil {
		t.Errorf("a move onto itself: %v", err)
	}
	if _, err := os.Stat(b); err != nil {
		t.Error("a move onto itself removed the file")
	}
}

// A crash after the bin took the old file but before the manifest said so: the manifest
// still says Verified. Recovery puts the staged copy in place; the track keeps a file.
func TestRecoverCrashBetweenBinAndManifest(t *testing.T) {
	w := newWorld(t)
	_, final, _ := crashAfter(t, w, "tag", true)
	if _, err := library.RemoveToBin(w.deps.Bin, final); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(final); err != nil || !bytes.HasSuffix(b, []byte("NEW")) {
		t.Errorf("after recovery the track's file is %q (%v)", b, err)
	}
	if len(w.stagingJobs()) != 0 {
		t.Error("the job wasn't settled")
	}
}

// A crash after the final move but before the manifest said Placed: it's settled as
// placed and recorded, on the first start.
func TestRecoverCrashAfterMoveBeforePlaced(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "bin", true)
	if err := renameInto(j.Items()[0].Temp, final); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if len(w.stagingJobs()) != 0 {
		t.Error("the job is never settled")
	}
	if len(w.recorded) != 1 {
		t.Errorf("recorded %v", w.recorded)
	}
	if b, _ := os.ReadFile(final); !bytes.HasSuffix(b, []byte("NEW")) {
		t.Errorf("final = %q", b)
	}
}

// A crash at every point of the replace sequence: whatever was saved, recovery leaves the
// track with exactly one file (the new one once the old is binned, else the old one) and
// nothing behind in the album folder.
func TestRecoverEveryReplaceStep(t *testing.T) {
	for _, tc := range []struct {
		state State
		want  string
	}{
		{Arriving, "OLD"}, {Binning, "OLD"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			w := newWorld(t)
			_, final, j := crashAfter(t, w, "tag", true)
			tmp := tempName(final, "new")
			if err := linkOrCopyNew(j.Items()[0].Staged, tmp); err != nil {
				t.Fatal(err)
			}
			if err := j.set(0, func(it *Item) { it.State, it.Temp = tc.state, tmp }); err != nil {
				t.Fatal(err)
			}
			if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(final); !bytes.HasSuffix(b, []byte(tc.want)) {
				t.Errorf("final = %q, want %s", b, tc.want)
			}
			if entries, _ := os.ReadDir(filepath.Dir(final)); len(entries) != 1 {
				t.Errorf("album folder holds %d entries", len(entries))
			}
		})
	}
	// Binning with the bin having taken the old file: forward.
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "tag", true)
	tmp := tempName(final, "new")
	_ = linkOrCopyNew(j.Items()[0].Staged, tmp)
	_ = j.set(0, func(it *Item) { it.State, it.Temp = Binning, tmp })
	if _, err := library.RemoveToBin(w.deps.Bin, final); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(final); !bytes.HasSuffix(b, []byte("NEW")) {
		t.Errorf("binning forward: final = %q", b)
	}
}

// A final move that fails after the bin took the old file brings the old file back at
// once (Settle), whatever the failure.
func TestSettleRestoresTheOldFile(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "bin", true)
	// The new file's temporary name and staged copy are lost: the move can't happen.
	_ = os.Remove(j.Items()[0].Temp)
	_ = os.Remove(j.Items()[0].Staged)
	if err := j.Settle(0); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(final); string(b) != "OLD" {
		t.Errorf("final = %q, want the old file back", b)
	}
	if j.Items()[0].State != Discarded {
		t.Errorf("state %s", j.Items()[0].State)
	}
}

// Retagging a library file that is a hard link to a seeding download: the download keeps
// its bytes and mtime (the bin doesn't stamp a shared inode).
func TestRetagHardlinkedLibraryFileLeavesTheSeedAlone(t *testing.T) {
	w := newWorld(t)
	dl := w.write(filepath.Join(w.dl, "rel", "01.flac"), "SEEDING-AUDIO")
	lib := filepath.Join(w.root, "A", "Al", "01.flac")
	_ = os.MkdirAll(filepath.Dir(lib), 0o755)
	if err := os.Link(dl, lib); err != nil {
		t.Skip(err)
	}
	before := snap(t, dl)
	j, err := Start(w.root, Manifest{Kind: KindRetag, Items: []Item{{Source: lib, Final: lib, Replace: lib}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Stage(0); err != nil {
		t.Fatal(err)
	}
	if err := j.Tag(context.Background(), 0, houseTags, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Place(0); err != nil {
		t.Fatal(err)
	}
	_ = j.Recorded(0)
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	after := snap(t, dl)
	if !bytes.Equal(after.content, before.content) || !after.mtime.Equal(before.mtime) {
		t.Error("the seeding file changed")
	}
	if got, _ := (&fakeTagger{}).Read(lib); got.Get("TITLE") != "One" {
		t.Errorf("library file not retagged: %v", got)
	}
}

// With the bin switched off, an old name a new file retires is kept, never deleted.
func TestRetireKeepsTheFileWithTheBinOff(t *testing.T) {
	w := newWorld(t)
	w.deps.Bin = library.SingleBin("")
	old := w.write(filepath.Join(w.root, "A", "01 - One.mp3"), "MP3")
	src := w.write(filepath.Join(w.dl, "01.flac"), "FLAC")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "A", "01 - One.flac"), Retire: old}}}, w.deps)
	_ = j.Stage(0)
	_ = j.Tag(context.Background(), 0, houseTags, nil)
	if err := j.Place(0); !errors.Is(err, ErrReplaceRefused) {
		t.Fatalf("Place with the bin off: %v", err)
	}
	j.retire(Item{Retire: old, Final: filepath.Join(w.root, "A", "01 - One.flac")})
	if _, err := os.Stat(old); err != nil {
		t.Error("the bin is off, and the old file was deleted")
	}
}

// Temporary names fit the 255-byte limit however long the name they stand beside.
func TestTempNameFits(t *testing.T) {
	long := filepath.Join("/x", string(bytes.Repeat([]byte("é"), 125))+".flac")
	for _, kind := range []string{"new", "part"} {
		if n := filepath.Base(tempName(long, kind)); len(n) > maxName || !isTempName(n) {
			t.Errorf("%s: %d bytes %q", kind, len(n), n)
		}
	}
	if got := filepath.Base(tempName("/x/01.flac", "new")); got != ".01.flac.arrmada-new" {
		t.Errorf("short name: %s", got)
	}
}
