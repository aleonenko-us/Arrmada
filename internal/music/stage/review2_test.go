package stage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// The second review's crash cases (2026-10-20), as tests.

// Where the staging folder and the album folder can't share a hard link (mergerfs across
// disks, a separately mounted album folder), the new file reaches its temporary name as
// a copy. A crash after the final move then finds the staged copy a different inode from
// the file in place: the same bytes still count as landed, so the job settles on the
// first start and the file is recorded.
func TestRecoverLandedCopyWhenNoHardLink(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "bin", true)
	it := j.Items()[0]
	_ = os.Remove(it.Temp)
	if err := copyNew(it.Staged, it.Temp); err != nil {
		t.Fatal(err)
	}
	if err := renameInto(it.Temp, final); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("the job is kept though the new file is in place: %v", jobs)
	}
	if len(w.recorded) != 1 {
		t.Errorf("recorded %d files, want 1", len(w.recorded))
	}
	if b, _ := os.ReadFile(final); !bytes.HasSuffix(b, []byte("NEW")) {
		t.Errorf("final = %q", b)
	}
}

// crashInPlainMove runs Place's plain path (nothing to replace) up to a crash after the
// move: Placing saved, the copy at its final path, Placed never saved.
func crashInPlainMove(t *testing.T, w *world, j *Job, final string) {
	t.Helper()
	if err := j.set(0, func(it *Item) { it.State = Placing }); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveNoClobber(j.Items()[0].Staged, final); err != nil {
		t.Fatal(err)
	}
}

// A crash after a plain move, before Placed was saved: the file is recorded, not dropped
// as a copy that never landed.
func TestRecoverCrashAfterPlainMove(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "tag", false)
	crashInPlainMove(t, w, j, final)
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if len(w.recorded) != 1 {
		t.Errorf("recorded %d files, want 1", len(w.recorded))
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("job kept: %v", jobs)
	}
	if b, _ := os.ReadFile(final); !bytes.HasSuffix(b, []byte("NEW")) {
		t.Errorf("final = %q", b)
	}
}

// Placing that never moved (the crash came before the move): the copy is dropped and the
// job settles with nothing in the library.
func TestRecoverPlacingBeforeTheMove(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "tag", false)
	if err := j.set(0, func(it *Item) { it.State = Placing }); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if exists(final) || len(w.recorded) != 0 || len(w.stagingJobs()) != 0 {
		t.Errorf("final there=%v recorded=%d jobs=%v", exists(final), len(w.recorded), w.stagingJobs())
	}
}

// The same crash with an old name to retire (an .mp3 the .flac replaces): recovery
// records the new file and sends the old one to the bin.
func TestRecoverPlainMoveRetiresTheOldName(t *testing.T) {
	w := newWorld(t)
	src := w.write(filepath.Join(w.dl, "01.flac"), "NEW")
	old := w.write(filepath.Join(w.root, "A", "01.mp3"), "OLDMP3")
	final := filepath.Join(w.root, "A", "01.flac")
	j, err := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final, Retire: old, Ref: 1}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Stage(0); err != nil {
		t.Fatal(err)
	}
	if err := j.Tag(context.Background(), 0, houseTags, nil); err != nil {
		t.Fatal(err)
	}
	crashInPlainMove(t, w, j, final)
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if exists(old) {
		t.Error("the old .mp3 is still in the library beside the new file")
	}
	if entries, _ := os.ReadDir(w.bin); len(entries) == 0 {
		t.Error("the old .mp3 isn't in the bin")
	}
	if len(w.recorded) != 1 {
		t.Errorf("recorded %d files, want 1", len(w.recorded))
	}
}

// Settle after a plain move whose error came after the file landed keeps it as placed;
// one that never moved drops the copy.
func TestSettlePlacing(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "tag", false)
	crashInPlainMove(t, w, j, final)
	if err := j.Settle(0); err != nil {
		t.Fatal(err)
	}
	if st := j.Items()[0].State; st != Placed {
		t.Errorf("moved: state %s, want placed", st)
	}

	w = newWorld(t)
	_, final, j = crashAfter(t, w, "tag", false)
	staged := j.Items()[0].Staged
	if err := j.set(0, func(it *Item) { it.State = Placing }); err != nil {
		t.Fatal(err)
	}
	if err := j.Settle(0); err != nil {
		t.Fatal(err)
	}
	if st := j.Items()[0].State; st != Discarded || exists(staged) || exists(final) {
		t.Errorf("not moved: state %s, staged there=%v, final there=%v", st, exists(staged), exists(final))
	}
}

// The bin switched off between a crash and the next start: recovery never loses the
// track's file, whichever step it stopped at.
func TestRecoverWithTheBinSwitchedOff(t *testing.T) {
	t.Run("placed-retire", func(t *testing.T) {
		w := newWorld(t)
		src := w.write(filepath.Join(w.dl, "01.flac"), "NEW")
		old := w.write(filepath.Join(w.root, "A", "01.mp3"), "OLDMP3")
		final := filepath.Join(w.root, "A", "01.flac")
		j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final, Retire: old, Ref: 1}}}, w.deps)
		_ = j.Stage(0)
		_ = j.Tag(context.Background(), 0, houseTags, nil)
		crashInPlainMove(t, w, j, final)
		_ = j.set(0, func(it *Item) { it.State = Placed })
		w.deps.Bin = library.SingleBin("")
		_, _ = Recover(context.Background(), w.root, w.deps, w.record)
		if !exists(old) || !exists(final) {
			t.Errorf("old there=%v, new there=%v: both must stay with the bin off", exists(old), exists(final))
		}
	})
	for _, st := range []string{"binning-taken", "binned-forward", "binned-restore"} {
		t.Run(st, func(t *testing.T) {
			w := newWorld(t)
			var final string
			var j *Job
			switch st {
			case "binning-taken":
				_, final, j = crashAfter(t, w, "tag", true)
				tmp := tempName(final, "new")
				if err := linkOrCopyNew(j.Items()[0].Staged, tmp); err != nil {
					t.Fatal(err)
				}
				_ = j.set(0, func(it *Item) { it.State, it.Temp = Binning, tmp })
				if _, err := library.RemoveToBin(w.deps.Bin, final); err != nil {
					t.Fatal(err)
				}
			case "binned-forward":
				_, final, _ = crashAfter(t, w, "bin", true)
			case "binned-restore":
				_, final, j = crashAfter(t, w, "bin", true)
				_ = os.Remove(j.Items()[0].Temp)
				_ = os.Remove(j.Items()[0].Staged)
			}
			w.deps.Bin = library.SingleBin("")
			_, _ = Recover(context.Background(), w.root, w.deps, w.record)
			b, err := os.ReadFile(final)
			if err != nil {
				t.Fatalf("no file at the track's path after recovery: %v", err)
			}
			want := "NEW"
			if st == "binned-restore" {
				want = "OLD"
			}
			if !bytes.HasSuffix(b, []byte(want)) {
				t.Errorf("final = %q, want %s", b, want)
			}
		})
	}
}

// A crash after every step of the replace order, file operations and manifest saves
// alike: two starts later the track has exactly one file (the new one once the old is in
// the bin), nothing else is in the album folder, no job is left, and a new file in place
// is recorded.
func TestRecoverEveryPrefixOfReplace(t *testing.T) {
	type step struct {
		name string
		do   func(t *testing.T, w *world, j *Job, final string)
	}
	steps := []step{
		{"set-arriving", func(t *testing.T, w *world, j *Job, final string) {
			_ = j.set(0, func(it *Item) { it.State, it.Temp = Arriving, tempName(final, "new") })
		}},
		{"link-temp", func(t *testing.T, w *world, j *Job, final string) {
			if err := linkOrCopyNew(j.Items()[0].Staged, j.Items()[0].Temp); err != nil {
				t.Fatal(err)
			}
		}},
		{"set-binning", func(t *testing.T, w *world, j *Job, final string) {
			_ = j.set(0, func(it *Item) { it.State = Binning })
		}},
		{"bin-old", func(t *testing.T, w *world, j *Job, final string) {
			m, err := library.RecycleFile(w.bin, final)
			if err != nil {
				t.Fatal(err)
			}
			j.m.Items[0].Binned = m // in memory only: saved by the next step
		}},
		{"set-binned", func(t *testing.T, w *world, j *Job, final string) { _ = j.set(0, func(it *Item) { it.State = Binned }) }},
		{"link-final", func(t *testing.T, w *world, j *Job, final string) {
			if err := os.Link(j.Items()[0].Temp, final); err != nil {
				t.Fatal(err)
			}
		}},
		{"unlink-temp", func(t *testing.T, w *world, j *Job, final string) { _ = os.Remove(j.Items()[0].Temp) }},
		{"unlink-staged", func(t *testing.T, w *world, j *Job, final string) { _ = removeOwn(j.dir, j.Items()[0].Staged) }},
		{"set-placed", func(t *testing.T, w *world, j *Job, final string) { _ = j.set(0, func(it *Item) { it.State = Placed }) }},
	}
	for n := 0; n <= len(steps); n++ {
		name := "none"
		if n > 0 {
			name = steps[n-1].name
		}
		t.Run("after-"+name, func(t *testing.T) {
			w := newWorld(t)
			_, final, j := crashAfter(t, w, "tag", true)
			for _, s := range steps[:n] {
				s.do(t, w, j, final)
			}
			for range 2 {
				_, _ = Recover(context.Background(), w.root, w.deps, w.record)
			}
			b, err := os.ReadFile(final)
			if err != nil {
				t.Fatal("nothing at the track's path")
			}
			if entries, _ := os.ReadDir(filepath.Dir(final)); len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("album folder holds %v", names)
			}
			if jobs := w.stagingJobs(); len(jobs) != 0 {
				t.Errorf("job kept: %v", jobs)
			}
			binned := n >= 4 // the old file is in the bin from "bin-old" on
			isNew := bytes.HasSuffix(b, []byte("NEW"))
			if isNew != binned {
				t.Errorf("final = %q with the old file binned=%v", b, binned)
			}
			if isNew && len(w.recorded) == 0 {
				t.Error("the new file is in place but never recorded")
			}
		})
	}
}

// A job a crash left with an old file in the bin holds that file there: HeldInBin names
// it until the job is settled.
func TestHeldInBin(t *testing.T) {
	w := newWorld(t)
	_, _, j := crashAfter(t, w, "bin", true)
	binned := j.Items()[0].Binned
	if binned == "" {
		t.Fatal("the crash left nothing in the bin")
	}
	if held := HeldInBin(w.root); !held[binned] || len(held) != 1 {
		t.Errorf("held = %v, want %s", held, binned)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if held := HeldInBin(w.root); len(held) != 0 {
		t.Errorf("held after the job settled: %v", held)
	}
}
