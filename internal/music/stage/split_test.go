package stage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The ffmpeg command line for a split: one decode, asegment at the exact sample offsets,
// one FLAC per piece at the image's depth, only the cue's tags.
func TestSplitArgs(t *testing.T) {
	p := SplitPlan{Image: "/dl/Album.wav", Cuts: []int64{88200, 441000}, TotalSamples: 900000, Channels: 2, Bits: 24,
		Tags: []map[string]string{{"title": "One", "track": "1", "isrc": "skipped"}, nil, {"title": "Three", "artist": "A & B"}}}
	args := SplitArgs(p, []string{"/s/0000.flac", "/s/0001.flac", "/s/0002.flac"})
	line := strings.Join(args, " ")
	for _, want := range []string{
		"-nostdin -n -i /dl/Album.wav",
		"-filter_complex [0:a:0]asegment=samples=88200|441000[s0][s1][s2]",
		"-map [s0] -map_metadata -1 -c:a flac -bits_per_raw_sample 24 -metadata title=One -metadata track=1 -fflags +bitexact -flags:a +bitexact -f flac /s/0000.flac",
		"-map [s1] -map_metadata -1 -c:a flac -bits_per_raw_sample 24 -fflags +bitexact -flags:a +bitexact -f flac /s/0001.flac",
		"-metadata title=Three -metadata artist=A & B",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in\n%s", want, line)
		}
	}
	if strings.Contains(line, "isrc") {
		t.Error("only the listed tag keys are written")
	}
	// The image's file name stays one argument whatever it holds.
	if !slices.Contains(args, "/dl/Album.wav") {
		t.Error("image argument")
	}
	// 16-bit: no depth option (ffmpeg's FLAC takes s16 as 16-bit).
	p.Bits = 16
	if strings.Contains(strings.Join(SplitArgs(p, []string{"a", "b", "c"}), " "), "bits_per_raw_sample") {
		t.Error("16-bit asked for a depth")
	}
}

func TestSplitPlanRefusals(t *testing.T) {
	ok := SplitPlan{Image: "x", Cuts: []int64{10, 20}, TotalSamples: 100, Channels: 2, Bits: 16}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*SplitPlan){
		"no channels":     func(p *SplitPlan) { p.Channels = 0 },
		"unknown depth":   func(p *SplitPlan) { p.Bits = 0 },
		"32-bit":          func(p *SplitPlan) { p.Bits = 32 },
		"out of order":    func(p *SplitPlan) { p.Cuts = []int64{20, 10} },
		"a cut at zero":   func(p *SplitPlan) { p.Cuts = []int64{0, 20} },
		"past the end":    func(p *SplitPlan) { p.Cuts = []int64{10, 100} },
		"equal cut point": func(p *SplitPlan) { p.Cuts = []int64{10, 10} },
	} {
		p := ok
		p.Cuts = append([]int64(nil), ok.Cuts...)
		mut(&p)
		if err := p.validate(); !errors.Is(err, ErrSplit) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if ok.want(0, 100) != 10 || ok.want(1, 100) != 10 || ok.want(2, 100) != 80 {
		t.Error("piece lengths")
	}
}

// A split interrupted halfway (ffmpeg killed, the app restarted): the next start deletes
// its pieces and its folder, and the image is as it was.
func TestRecoverDeletesAnInterruptedSplit(t *testing.T) {
	w := newWorld(t)
	img := w.write(filepath.Join(w.dl, "Album", "Album.flac"), "THE-WHOLE-DISC")
	before := snap(t, img)
	j, err := Start(w.root, Manifest{Kind: KindSplit, Items: []Item{{Source: img}, {Source: img}, {Source: img}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	items := j.Items()
	for _, it := range items {
		if filepath.Ext(it.Staged) != ".flac" || it.Final != "" {
			t.Fatalf("a split piece: %+v", it)
		}
	}
	// Two pieces written, the third half-written; one already checked.
	w.write(items[0].Staged, "PIECE-1")
	w.write(items[1].Staged, "PIECE-2")
	w.write(items[2].Staged, "PIE")
	if err := j.set(0, func(it *Item) { it.State = Verified }); err != nil {
		t.Fatal(err)
	}
	rep, err := Recover(context.Background(), w.root, w.deps, w.record)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RolledBak != 3 || rep.Kept != 0 || len(w.recorded) != 0 {
		t.Errorf("report %+v, recorded %d", rep, len(w.recorded))
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("staging left: %v", jobs)
	}
	before.same(t, img)
}

// An import of split pieces: each piece is deleted once its tagged copy is in the
// library; the space check counts one piece, not all of them; a file outside a split job
// is never deleted, whatever its item says.
func TestScratchPiecesGoOncePlaced(t *testing.T) {
	w := newWorld(t)
	img := w.write(filepath.Join(w.dl, "Album", "Album.flac"), "WHOLE")
	split, err := Start(w.root, Manifest{Kind: KindSplit, Items: []Item{{Source: img}, {Source: img}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	pieces := split.SplitFiles()
	w.write(pieces[0], strings.Repeat("1", 1000))
	w.write(pieces[1], strings.Repeat("2", 3000))
	// A download file wrongly marked scratch must survive.
	notOurs := w.write(filepath.Join(w.dl, "Album", "03 - Three.flac"), "SEEDING")
	finals := []string{
		filepath.Join(w.root, "A", "Album", "01.flac"), filepath.Join(w.root, "A", "Album", "02.flac"), filepath.Join(w.root, "A", "Album", "03.flac"),
	}
	imp, err := Start(w.root, Manifest{Kind: KindImport, Items: []Item{
		{Source: pieces[0], Final: finals[0], Ref: 1, Scratch: true},
		{Source: pieces[1], Final: finals[1], Ref: 2, Scratch: true},
		{Source: notOurs, Final: finals[2], Ref: 3, Scratch: true},
	}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	var asked uint64
	imp.d.FreeBytes = func(string) (uint64, bool) { return asked, true }
	// Needs the biggest piece (3000) plus the seeding file (7), plus the margin.
	need := uint64(3000+7) + (64 << 20) + (3000+7)/50
	asked = need - 1
	if err := imp.CheckSpace(); !errors.Is(err, ErrNoSpace) {
		t.Errorf("one byte short: %v", err)
	}
	asked = need
	if err := imp.CheckSpace(); err != nil {
		t.Errorf("exactly enough: %v", err)
	}
	for i := range imp.Items() {
		if err := imp.Stage(i); err != nil {
			t.Fatal(err)
		}
		if err := imp.Untagged(i); err != nil {
			t.Fatal(err)
		}
		if err := imp.Place(i); err != nil {
			t.Fatal(err)
		}
		_ = imp.Recorded(i)
	}
	for i, p := range pieces {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("piece %d still in staging after it was placed: %v", i, err)
		}
	}
	if b, err := os.ReadFile(notOurs); err != nil || string(b) != "SEEDING" {
		t.Errorf("a download file was deleted: %v", err)
	}
	for _, f := range finals {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("not placed: %v", err)
		}
	}
	if err := imp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := split.Abandon("import finished"); err != nil {
		t.Fatal(err)
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("staging left: %v", jobs)
	}
}

func TestIsScratch(t *testing.T) {
	root := filepath.Join(t.TempDir(), DirName)
	j := &Job{dir: filepath.Join(root, "20261020T101010-aaaaaaaa")}
	other := filepath.Join(root, "20261020T101011-bbbbbbbb")
	for p, want := range map[string]bool{
		filepath.Join(other, filesDir, "0001.flac"):                                    true,
		filepath.Join(j.dir, filesDir, "0001.flac"):                                    false, // its own copies
		filepath.Join(other, "0001.flac"):                                              false,
		filepath.Join(other, filesDir, "sub", "0001.flac"):                             false,
		filepath.Join(filepath.Dir(root), "music", filesDir, "0001.flac"):              false,
		filepath.Join(t.TempDir(), DirName, "20261020T101011-bbbbbbbb", filesDir, "x"): false, // another staging folder
		filepath.Join(root, "not-a-job", filesDir, "0001.flac"):                        false,
	} {
		if got := j.isScratch(p); got != want {
			t.Errorf("isScratch(%s) = %v, want %v", p, got, want)
		}
	}
}
