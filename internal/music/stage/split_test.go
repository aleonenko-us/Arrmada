package stage

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// The ffmpeg command lines for a split: the image decoded once to raw 32-bit PCM, each
// piece encoded from raw PCM on stdin at the image's rate, layout and depth, with only the
// cue's tags.
func TestSplitArgs(t *testing.T) {
	if got := strings.Join(decodeArgs("/dl/Album.wav"), " "); got != "-hide_banner -loglevel error -nostdin -i /dl/Album.wav -map 0:a:0 -c:a pcm_s32le -f s32le -" {
		t.Errorf("decode: %s", got)
	}
	p := SplitPlan{Image: "/dl/Album.wav", Cuts: []int64{88200, 441000}, TotalSamples: 900000, Rate: 96000, Channels: 2, Bits: 24,
		Tags: []map[string]string{{"title": "One", "track": "1", "isrc": "skipped"}, nil, {"title": "Three", "artist": "A & B"}}}
	for k, want := range []string{
		"-hide_banner -loglevel error -nostdin -n -f s32le -ar 96000 -ch_layout stereo -i - -map_metadata -1 -c:a flac -bits_per_raw_sample 24 -metadata title=One -metadata track=1 -fflags +bitexact -flags:a +bitexact -f flac /s/0000.flac",
		"-hide_banner -loglevel error -nostdin -n -f s32le -ar 96000 -ch_layout stereo -i - -map_metadata -1 -c:a flac -bits_per_raw_sample 24 -fflags +bitexact -flags:a +bitexact -f flac /s/0001.flac",
	} {
		if got := strings.Join(encodeArgs(p, k, []string{"/s/0000.flac", "/s/0001.flac"}[k]), " "); got != want {
			t.Errorf("piece %d:\n got %s\nwant %s", k+1, got, want)
		}
	}
	args := encodeArgs(p, 2, "/s/0002.flac")
	if !slices.Contains(args, "artist=A & B") || strings.Contains(strings.Join(args, " "), "isrc") {
		t.Errorf("tags: %q", args)
	}
	// 16-bit: fed as s16le (narrowed by Arrmada), no depth option; a layout ffmpeg didn't
	// know falls back to the usual one for the channel count.
	p.Bits, p.Layout, p.Channels = 16, "unknown", 1
	line := strings.Join(encodeArgs(p, 0, "x.flac"), " ")
	if !strings.Contains(line, "-f s16le -ar 96000 -ch_layout mono -i -") || strings.Contains(line, "bits_per_raw_sample") {
		t.Errorf("16-bit mono: %s", line)
	}
	p.Layout, p.Channels = "5.1(side)", 6
	if line := strings.Join(encodeArgs(p, 0, "x.flac"), " "); !strings.Contains(line, "-ch_layout 5.1(side)") {
		t.Errorf("layout: %s", line)
	}
}

func TestSplitPlanRefusals(t *testing.T) {
	ok := SplitPlan{Image: "x", Cuts: []int64{10, 20}, TotalSamples: 100, Rate: 44100, Channels: 2, Bits: 16}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*SplitPlan){
		"no channels":     func(p *SplitPlan) { p.Channels = 0 },
		"no rate":         func(p *SplitPlan) { p.Rate = 0 },
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

// A disk that fills up mid-split is tried again later, not sent to Review.
func TestDiskFullIsNotAReview(t *testing.T) {
	if !diskFull(nil, errors.New("exit status 1"), []byte("[out#0/flac] Error writing trailer: No space left on device")) {
		t.Error("ffmpeg's message")
	}
	if !diskFull(&os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}, nil, nil) {
		t.Error("ENOSPC")
	}
	if diskFull(errors.New("broken pipe"), errors.New("exit status 1"), []byte("Invalid data found")) {
		t.Error("an ordinary failure")
	}
}

// The MD5 FLAC keeps of its samples, computed from the 32-bit decoded stream: 16-bit as
// two bytes a sample, 24-bit as three, sign kept.
func TestStreamMD5(t *testing.T) {
	samples := []int32{0, 1, -1, 32767, -32768, 1234, -4321}
	var s32, s16 []byte
	for _, v := range samples {
		s32 = binary.LittleEndian.AppendUint32(s32, uint32(v<<16))
		s16 = binary.LittleEndian.AppendUint16(s16, uint16(int16(v)))
	}
	m := newStreamMD5(16)
	m.write(s32)
	if got, want := m.sum(), md5.Sum(s16); got != want {
		t.Errorf("16-bit md5")
	}
	var s24 []byte
	s32 = s32[:0]
	for _, v := range []int32{0, 1, -1, 8388607, -8388608, 70000} {
		s32 = binary.LittleEndian.AppendUint32(s32, uint32(v<<8))
		s24 = append(s24, byte(v), byte(v>>8), byte(v>>16))
	}
	m = newStreamMD5(24)
	m.write(s32)
	if got, want := m.sum(), md5.Sum(s24); got != want {
		t.Errorf("24-bit md5")
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
	notSplit := filepath.Join(root, "20261020T101012-cccccccc")
	for dir, kind := range map[string]string{other: KindSplit, notSplit: KindImport} {
		if err := os.MkdirAll(filepath.Join(dir, filesDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeManifest(dir, &Manifest{Version: 1, Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	if j.isScratch(filepath.Join(notSplit, filesDir, "0001.flac")) {
		t.Error("another import job's staged copy read as a split piece")
	}
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
