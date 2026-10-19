package automation

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/cue"
)

// The review fixes for cue-sheet albums (MUS-35): transient probe failures retry, two
// sheets disagreeing about one image go to Review, the image's own length is checked
// against the disc, AcoustID can veto, WAV headers that lie are refused, and nothing is
// read through a symlink.

// A probe that times out (or is killed) isn't the image's fault: no Review, the next sweep
// tries again. One where ffprobe ran and couldn't read the file is.
func TestCueProbeFailures(t *testing.T) {
	album := []cueSpec{{"Airbag", 12000, false}, {"Paranoid Android", 13000, false}, {"Subterranean Homesick Alien", 15000, false}}
	r := newCueRig(t, true, album)
	r.h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) {
		return mediainfo.AudioInfo{}, context.DeadlineExceeded
	}
	blobTrack(t, r.dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(r.dl, "Radiohead - OK Computer.cue"), okcCue)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if r.h.c.hasReview(r.h.ctx, hashFor(r.release)) {
		t.Fatalf("a probe timeout became a Review: %q", reviewReason(r))
	}

	if runtime.GOOS == "windows" {
		return
	}
	exit := exec.Command("sh", "-c", "exit 1").Run()
	var ee *exec.ExitError
	if !errors.As(exit, &ee) {
		t.Skip("no shell to make an exit error with")
	}
	r.h.c.probeAudioFn = func(context.Context, string) (mediainfo.AudioInfo, error) { return mediainfo.AudioInfo{}, exit }
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if rv := reviewFor(t, r.h.c, hashFor(r.release)); !strings.Contains(rv.Reason, "Couldn't read the disc image") {
		t.Errorf("review %q", rv.Reason)
	}
}

// Two cue sheets for one image: the same cuts (another encoding, an EAC copy) are fine;
// different cuts go to Review rather than one being picked.
func TestTwoCueSheetsForOneImage(t *testing.T) {
	h := musicTestCoord(t)
	h.c.probeAudioFn = fakeImageProbe(45)
	dl := t.TempDir()
	blobTrack(t, dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(dl, "Radiohead - OK Computer.cue"), okcCue)
	writeFile(t, filepath.Join(dl, "Radiohead - OK Computer (EAC).cue"), strings.ReplaceAll(okcCue, "\n", "\r\n"))
	files, cs, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	if err != nil || cs == nil || len(files) != 3 {
		t.Fatalf("the same sheet twice: %v %v", files, err)
	}
	writeFile(t, filepath.Join(dl, "Radiohead - OK Computer (EAC).cue"), strings.Replace(okcCue, "INDEX 01 00:30:00", "INDEX 01 00:31:00", 1))
	_, _, err = h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	var cp *cueProblem
	if !errors.As(err, &cp) || !strings.Contains(cp.why, "cut it differently") {
		t.Fatalf("err = %v", err)
	}
}

// The image's track audio must be the disc's listed length, give or take a few seconds.
func TestCueLengthMismatch(t *testing.T) {
	s, err := cue.Parse([]byte(okcCue))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(seconds float64) (*cueSet, []*cueImage, []placement) {
		ci := &cueImage{path: "/dl/x.flac", info: mediainfo.AudioInfo{SampleRateHz: 44100}, segs: s.Segments(0), hidden: 0,
			total: int64(seconds * 44100), cuts: []int64{5 * 44100, 17 * 44100, 30 * 44100}}
		cs := &cueSet{parts: map[string]cuePart{"/dl/01.flac": {img: ci, seg: 1}}}
		plan := []placement{{t: music.Track{DiscNumber: 1, TrackNumber: 1}, f: music.AudioFile{Path: "/dl/01.flac"}}}
		return cs, []*cueImage{ci}, plan
	}
	tracks := []music.Track{
		{DiscNumber: 1, TrackNumber: 0, DurationSec: 5}, // a listed track 0 isn't part of the disc's length
		{DiscNumber: 1, TrackNumber: 1, DurationSec: 12}, {DiscNumber: 1, TrackNumber: 2, DurationSec: 13},
		{DiscNumber: 1, TrackNumber: 3, DurationSec: 15}, {DiscNumber: 2, TrackNumber: 1, DurationSec: 300},
	}
	for _, c := range []struct {
		seconds float64
		ok      bool
	}{{45, true}, {47.5, true}, {52, false}, {38, false}} {
		cs, imgs, plan := mk(c.seconds)
		why := cueLengthMismatch(cs, imgs, tracks, plan)
		if (why == "") != c.ok {
			t.Errorf("a %.1f s image: %q", c.seconds, why)
		}
	}
	// A track of unknown length: nothing to compare.
	cs, imgs, plan := mk(90)
	tracks[2].DurationSec = 0
	if why := cueLengthMismatch(cs, imgs, tracks, plan); why != "" {
		t.Errorf("unknown length: %q", why)
	}
}

// With an AcoustID key, two split tracks are listened to: all recognised as other
// recordings is a veto; one agreeing, or none recognised, isn't.
func TestCueSoundsWrong(t *testing.T) {
	h := musicTestCoord(t)
	plan := []placement{
		{t: music.Track{Title: "Airbag", RecordingMBID: "rec-airbag"}, f: music.AudioFile{Path: "/s/0001.flac"}, scratch: true},
		{t: music.Track{Title: "Paranoid Android", RecordingMBID: "rec-pa"}, f: music.AudioFile{Path: "/s/0002.flac"}, scratch: true},
		{t: music.Track{Title: "Lucky", RecordingMBID: "rec-lucky"}, f: music.AudioFile{Path: "/s/0003.flac"}, scratch: true},
	}
	if why := h.c.cueSoundsWrong(h.ctx, plan); why != "" {
		t.Errorf("no key: %q", why)
	}
	fp := &fakeFingerprinter{recs: map[string][]string{"0001.flac": {"rec-other"}, "0002.flac": {"rec-other-2"}}}
	h.c.SetMusicFingerprinter(fp)
	if why := h.c.cueSoundsWrong(h.ctx, plan); !strings.Contains(why, "Airbag") || fp.calls != 2 {
		t.Errorf("veto: %q (%d calls)", why, fp.calls)
	}
	fp.recs["0002.flac"] = []string{"REC-PA"}
	if why := h.c.cueSoundsWrong(h.ctx, plan); why != "" {
		t.Errorf("one agrees: %q", why)
	}
	fp.recs = map[string][]string{}
	if why := h.c.cueSoundsWrong(h.ctx, plan); why != "" {
		t.Errorf("none recognised: %q", why)
	}
}

func wavFile(t *testing.T, path string, declared uint32, audio int) {
	t.Helper()
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = append(b, make([]byte, 16)...)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, declared)
	b = append(b, make([]byte, audio)...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWAVHeaderConsistency(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name     string
		declared uint32
		audio    int
		ok       bool
	}{
		{"exact", 4000, 4000, true},
		{"a tag chunk after the audio", 4000, 4000 + 1024, true},
		{"cut short", 8000, 4000, false},
		{"header far smaller than the file", 4000, 4000 + 20<<20, false},
	} {
		p := filepath.Join(dir, c.name+".wav")
		wavFile(t, p, c.declared, c.audio)
		if err := wavConsistent(p); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	other := filepath.Join(dir, "x.flac")
	writeFile(t, other, "fLaC not a wav")
	if err := wavConsistent(other); err != nil {
		t.Errorf("not a WAV: %v", err)
	}
}

// A cue's FILE that is a symlink, or reached through a symlinked folder, out of the
// download is never read.
func TestResolveCueFileSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	blobTrack(t, outside, "Secret.flac", 1)
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "Secret.flac"), filepath.Join(root, "Album.flac")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	sheet := filepath.Join(root, "Album.cue")
	for _, name := range []string{"Album.flac", "linked/Secret.flac"} {
		if got := resolveCueFile(root, sheet, name, false); got != "" {
			t.Errorf("%s resolved to %s", name, got)
		}
	}
}
