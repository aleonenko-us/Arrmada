package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// Cue-sheet albums (MUS-35), without ffmpeg: what the matcher is shown for a disc image,
// what goes to Reviews, and the staging recovery. The real split is in music_cue_it_test.go.

// cueSpec is one track of the cue rig's album: its title and length.
type cueSpec struct {
	title string
	ms    int
	zero  bool // listed at position 0: a hidden track before track 1
}

// newCueRig is an import rig whose album (Radiohead – OK Computer, one edition "rel-1")
// has the given discs of tracks, with a finished download in the music category (empty:
// the test writes the image and the cue sheet into r.dl).
func newCueRig(t *testing.T, grabbed bool, discs ...[]cueSpec) *importRig {
	t.Helper()
	h := newStallHarness(t)
	base := &fakeMusicProvider{
		artists: map[string]metadata.ArtistDetails{"a1": {ArtistResult: metadata.ArtistResult{MBID: "a1", Name: "Radiohead"}}},
		albums:  map[string][]metadata.AlbumResult{"a1": {{MBID: "rg1", Title: "OK Computer", Type: "Album", ReleaseDate: "1997-05-21", Year: 1997}}},
		tracks:  map[string][]metadata.TrackResult{},
	}
	sum := metadata.ReleaseSummary{MBID: "rel-1", Title: "OK Computer", Status: "Official", Date: "1997-05-21", Country: "GB",
		Credit: []metadata.CreditPart{{ArtistMBID: "a1", ArtistName: "Radiohead", Name: "Radiohead"}}}
	det := metadata.ReleaseDetail{ReleaseSummary: sum, ReleaseGroupMBID: "rg1"}
	for d, specs := range discs {
		md := metadata.ReleaseMedium{Position: d + 1, Format: "CD", TrackCount: len(specs)}
		pos := 0
		for i, s := range specs {
			if !s.zero {
				pos++
			}
			md.Tracks = append(md.Tracks, metadata.ReleaseTrack{MBID: fmt.Sprintf("rt-%d-%d", d+1, i+1),
				RecordingMBID: "rec-" + strings.ToLower(strings.ReplaceAll(s.title, " ", "-")), Position: pos, Title: s.title, LengthMs: s.ms})
		}
		det.Media = append(det.Media, md)
		sum.Media = append(sum.Media, metadata.ReleaseMedium{Position: d + 1, Format: "CD", TrackCount: len(specs)})
	}
	prov := &editionsProvider{fakeMusicProvider: base,
		releases: map[string][]metadata.ReleaseSummary{"rg1": {sum}},
		details:  map[string]metadata.ReleaseDetail{"rel-1": det}}
	svc := music.NewService(h.c.db, prov, h.c.log)
	h.c.music = svc
	h.c.imp = library.NewImporter(t.TempDir(), h.c.log)
	r := &importRig{h: h, svc: svc, release: "Radiohead - OK Computer (1997) [FLAC]", bin: t.TempDir()}
	h.c.imp.SetRecycleDir(r.bin)
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := svc.AddArtist(h.ctx, "a1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r.artist, r.album = a, a.Albums[0]
	if err := svc.EnsureTracks(h.ctx, r.album); err != nil {
		t.Fatal(err)
	}
	r.dl = filepath.Join(t.TempDir(), r.release)
	if err := os.MkdirAll(r.dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if grabbed {
		h.c.recordMusicGrab(h.ctx, "music", r.album.ID, r.release, "Fake", "", hashFor(r.release))
	}
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(r.release), "name": r.release, "state": "uploading", "progress": 1.0,
		"size": 600 << 10, "amount_left": 0, "category": download.CategoryMusic, "content_path": r.dl,
	})
	h.qbit.mu.Unlock()
	return r
}

// fakeImageProbe answers for lossless images (by extension) with the given length; the
// virtual tracks never reach it.
func fakeImageProbe(seconds float64) func(context.Context, string) (mediainfo.AudioInfo, error) {
	return func(_ context.Context, path string) (mediainfo.AudioInfo, error) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".flac", ".wav", ".ape", ".wv":
			return mediainfo.AudioInfo{Codec: "flac", BitsPerSample: 16, SampleRateHz: 44100, Channels: 2, DurationSec: seconds}, nil
		case ".mp3":
			return mediainfo.AudioInfo{Codec: "mp3", BitrateKbps: 320, SampleRateHz: 44100, Channels: 2, DurationSec: seconds}, nil
		}
		return mediainfo.AudioInfo{}, errNoFFprobe
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A ripper's cue: written for the .wav, the image compressed to .flac afterwards; a
// five-second hidden track before track 1; track 2 with a two-second pregap.
const okcCue = `REM DATE 1997
PERFORMER "Radiohead"
TITLE "OK Computer"
FILE "Radiohead - OK Computer.wav" WAVE
  TRACK 01 AUDIO
    TITLE "Airbag"
    INDEX 00 00:00:00
    INDEX 01 00:05:00
  TRACK 02 AUDIO
    TITLE "Paranoid Android"
    INDEX 00 00:15:00
    INDEX 01 00:17:00
  TRACK 03 AUDIO
    TITLE "Subterranean Homesick Alien"
    INDEX 01 00:30:00
`

func TestExpandCueSheet(t *testing.T) {
	h := musicTestCoord(t)
	h.c.probeAudioFn = fakeImageProbe(45)
	dl := filepath.Join(t.TempDir(), "Radiohead - OK Computer (1997) [FLAC]")
	blobTrack(t, dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(dl, "Radiohead - OK Computer.cue"), okcCue)
	files, cs, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	if err != nil || cs == nil {
		t.Fatalf("expand: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files: %+v", files)
	}
	mf := h.c.readMatchFiles(withCueSet(h.ctx, cs), files)
	want := []struct {
		name  string
		title string
		ms    int
	}{
		// The pregap rule: track 1 runs to track 2's INDEX 01, its pregap included.
		{"01 - Airbag.flac", "Airbag", 12000},
		{"02 - Paranoid Android.flac", "Paranoid Android", 13000},
		{"03 - Subterranean Homesick Alien.flac", "Subterranean Homesick Alien", 15000},
	}
	for i, w := range want {
		f := mf[i]
		if filepath.Base(f.Path) != w.name || filepath.Dir(f.Path) != dl {
			t.Errorf("file %d: %s", i, f.Path)
		}
		if f.Tags.Title != w.title || f.Tags.Track != i+1 || f.Tags.TrackTotal != 3 || f.Tags.Album != "OK Computer" ||
			f.Tags.Artist != "Radiohead" || f.Tags.AlbumArtist != "Radiohead" || f.Tags.Date != "1997" || f.DurationMs != w.ms {
			t.Errorf("file %d: %+v (%d ms)", i, f.Tags, f.DurationMs)
		}
		if _, err := os.Stat(f.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a virtual track exists on disk: %s", f.Path)
		}
	}
	img := cs.images[0]
	if img.hidden != 0 || img.parts[0] == "" {
		t.Errorf("the five-second lead-in is a hidden track, offered to track 0 only: %+v", img)
	}
	if got := img.cuts; len(got) != 3 || got[0] != 5*44100 || got[1] != 17*44100 || got[2] != 30*44100 {
		t.Errorf("cuts %v", got)
	}
	// What placement sees before the split: a FLAC at the image's depth.
	if info, err := h.c.probeAudio(withCueSet(h.ctx, cs), files[0].Path); err != nil || info.Codec != "flac" || info.BitsPerSample != 16 {
		t.Errorf("virtual probe: %+v %v", info, err)
	}
}

// A cue sheet saved in Windows-1252 (as older Windows rippers write it) reads as the
// names it holds, not mojibake.
func TestExpandCueSheetWindows1252(t *testing.T) {
	h := musicTestCoord(t)
	h.c.probeAudioFn = fakeImageProbe(45)
	dl := t.TempDir()
	blobTrack(t, dl, "Album.flac", 1)
	raw, err := charmap.Windows1252.NewEncoder().String("PERFORMER \"Sigur Rós\"\r\nTITLE \"Ágætis byrjun\"\r\nFILE \"Album.wav\" WAVE\r\n" +
		"  TRACK 01 AUDIO\r\n    TITLE \"Svefn-g-englar\"\r\n    INDEX 01 00:00:00\r\n  TRACK 02 AUDIO\r\n    TITLE \"Starálfur\"\r\n    INDEX 01 00:20:00\r\n")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dl, "Album.cue"), raw)
	files, cs, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	if err != nil {
		t.Fatal(err)
	}
	mf := h.c.readMatchFiles(withCueSet(h.ctx, cs), files)
	if len(mf) != 2 || mf[1].Tags.Title != "Starálfur" || mf[1].Tags.Artist != "Sigur Rós" || mf[1].Tags.Album != "Ágætis byrjun" ||
		filepath.Base(mf[1].Path) != "02 - Starálfur.flac" {
		t.Errorf("read as %+v", mf)
	}
}

func TestExpandCueSheetRefusals(t *testing.T) {
	for _, c := range []struct {
		name    string
		seconds float64
		image   string
		cueName string
		why     string
	}{
		{"missing file", 45, "Something Else.flac", "Disc.cue", `names "Radiohead - OK Computer.wav", which isn't in the download`},
		{"cue past the image's end", 20, "Radiohead - OK Computer.flac", "Radiohead - OK Computer.cue", "doesn't fit"},
		{"lossy image", 45, "Radiohead - OK Computer.mp3", "Radiohead - OK Computer.cue", "only lossless images"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := musicTestCoord(t)
			h.c.probeAudioFn = fakeImageProbe(c.seconds)
			dl := t.TempDir()
			blobTrack(t, dl, c.image, 1)
			writeFile(t, filepath.Join(dl, c.cueName), okcCue)
			_, _, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
			var cp *cueProblem
			if !errors.As(err, &cp) || !strings.Contains(cp.why, c.why) {
				t.Fatalf("err = %v, want one saying %q", err, c.why)
			}
		})
	}
}

// An image whose tracks are in the download too, and a cue sheet describing per-track
// files: nothing is split, the track files are matched as they are.
func TestCueSheetsThatDontSplit(t *testing.T) {
	h := musicTestCoord(t)
	h.c.probeAudioFn = fakeImageProbe(45)
	dl := t.TempDir()
	blobTrack(t, dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(dl, "Radiohead - OK Computer.cue"), okcCue)
	for _, n := range []string{"01 - Airbag.flac", "02 - Paranoid Android.flac", "03 - Subterranean Homesick Alien.flac"} {
		blobTrack(t, filepath.Join(dl, "tracks"), n, 2)
		blobTrack(t, dl, n, 2)
	}
	files, cs, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	if err != nil || cs != nil {
		t.Fatalf("expand: %v %v", cs, err)
	}
	for _, f := range files {
		if strings.Contains(f.Path, "OK Computer.flac") {
			t.Errorf("the image is matched too: %v", f.Path)
		}
	}
	if len(files) != 6 {
		t.Errorf("files %d, want the six track files", len(files))
	}

	// A per-track rip carrying the ripper's whole-disc cue sheet for an image never kept
	// ("Range.wav"): an ordinary album, not a Review.
	dl3 := t.TempDir()
	for _, n := range []string{"01 - Airbag.flac", "02 - Paranoid Android.flac", "03 - Subterranean Homesick Alien.flac"} {
		blobTrack(t, dl3, n, 3)
	}
	writeFile(t, filepath.Join(dl3, "Range.cue"), strings.Replace(okcCue, "Radiohead - OK Computer.wav", "Range.wav", 1))
	files, cs, err = h.c.expandCueSheets(h.ctx, dl3, findAudioFiles(dl3))
	if err != nil || cs != nil || len(files) != 3 {
		t.Fatalf("a per-track rip with an image cue: %v %v %v", files, cs, err)
	}

	perTrack := "FILE \"01 - One.flac\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n INDEX 00 03:00:00\n" +
		"FILE \"02 - Two.flac\" WAVE\n INDEX 01 00:00:00\n"
	dl2 := t.TempDir()
	blobTrack(t, dl2, "01 - One.flac", 1)
	blobTrack(t, dl2, "02 - Two.flac", 1)
	writeFile(t, filepath.Join(dl2, "Album.cue"), perTrack)
	in := findAudioFiles(dl2)
	files, cs, err = h.c.expandCueSheets(h.ctx, dl2, in)
	if err != nil || cs != nil || len(files) != 2 {
		t.Fatalf("a per-track cue sheet: %v %v %v", files, cs, err)
	}
}

// One sheet, two disc images: each track is tagged with its disc.
func TestCueSheetTwoDiscs(t *testing.T) {
	h := musicTestCoord(t)
	h.c.probeAudioFn = fakeImageProbe(40)
	dl := t.TempDir()
	blobTrack(t, dl, "CD1.flac", 1)
	blobTrack(t, dl, "CD2.flac", 2)
	writeFile(t, filepath.Join(dl, "Album.cue"), `PERFORMER "Pink Floyd"
TITLE "The Wall"
FILE "CD1.flac" WAVE
  TRACK 01 AUDIO
    TITLE "In the Flesh?"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "The Thin Ice"
    INDEX 01 00:20:00
FILE "CD2.flac" WAVE
  TRACK 03 AUDIO
    TITLE "Hey You"
    INDEX 01 00:00:00
  TRACK 04 AUDIO
    TITLE "Nobody Home"
    INDEX 01 00:15:00
`)
	files, cs, err := h.c.expandCueSheets(h.ctx, dl, findAudioFiles(dl))
	if err != nil || len(files) != 4 {
		t.Fatalf("expand: %v %v", files, err)
	}
	mf := h.c.readMatchFiles(withCueSet(h.ctx, cs), files)
	got := map[string]string{}
	for _, f := range mf {
		got[filepath.Base(f.Path)] = fmt.Sprintf("%d/%d %s %d", f.Tags.Disc, f.Tags.Track, f.Tags.Title, f.DurationMs)
	}
	for name, want := range map[string]string{
		"1-01 - In the Flesh_.flac": "1/1 In the Flesh? 20000",
		"1-02 - The Thin Ice.flac":  "1/2 The Thin Ice 20000",
		"2-01 - Hey You.flac":       "2/1 Hey You 15000",
		"2-02 - Nobody Home.flac":   "2/2 Nobody Home 25000",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q (all: %v)", name, got[name], want, got)
		}
	}
}

func TestResolveCueFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Album")
	blobTrack(t, dir, "Album.FLAC", 1)
	blobTrack(t, dir, "Other.ape", 1)
	sheet := filepath.Join(dir, "Album.cue")
	for _, c := range []struct {
		name   string
		single bool
		want   string
	}{
		{"Album.FLAC", true, "Album.FLAC"},
		{"album.flac", false, "Album.FLAC"}, // another case
		{"Album.wav", false, "Album.FLAC"},  // compressed after ripping
		{"Other.wav", false, "Other.ape"},   // any audio extension
		{"CDImage.wav", true, "Album.FLAC"}, // the only image, named like the cue
		{"CDImage.wav", false, ""},          // not when the sheet cuts several
		{`..\..\etc\passwd`, true, ""},      // never outside the download
		{`sub\Album.flac`, false, ""},       // a folder that isn't there
	} {
		got := resolveCueFile(root, sheet, c.name, c.single)
		if c.want == "" && got != "" || c.want != "" && got != filepath.Join(dir, c.want) {
			t.Errorf("%q (single %v) = %q, want %q", c.name, c.single, got, c.want)
		}
	}
	if discHint("Album (Disc 2).cue") != 2 || discHint("Album CD1.flac") != 1 || discHint("Discovery.cue") != 0 || discHint("ACDC.flac") != 0 {
		t.Error("discHint")
	}
}

// Through the whole import: a grabbed cue-sheet album the matcher is sure of reaches the
// split; one whose cue doesn't fit its image, or names a file that isn't there, goes to
// Reviews with the reason and nothing placed.
func TestCueImportReviews(t *testing.T) {
	album := []cueSpec{{"Airbag", 12000, false}, {"Paranoid Android", 13000, false}, {"Subterranean Homesick Alien", 15000, false}}

	r := newCueRig(t, true, album)
	r.h.c.probeAudioFn = fakeImageProbe(20)
	blobTrack(t, r.dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(r.dl, "Radiohead - OK Computer.cue"), okcCue)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	rv := reviewFor(t, r.h.c, hashFor(r.release))
	if rv.ReasonCode != ReasonImportFailed || !strings.Contains(rv.Reason, "doesn't fit") || rv.ExpectedID != r.album.ID {
		t.Fatalf("review %+v", rv)
	}

	r = newCueRig(t, true, album)
	r.h.c.probeAudioFn = fakeImageProbe(45)
	blobTrack(t, r.dl, "Something.flac", 1)
	writeFile(t, filepath.Join(r.dl, "Disc.cue"), okcCue)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if rv := reviewFor(t, r.h.c, hashFor(r.release)); !strings.Contains(rv.Reason, "isn't in the download") {
		t.Fatalf("review %+v", rv)
	}

	// A good cue over a file that isn't really audio: the matcher is sure (titles and
	// lengths), so the split runs — and fails (no ffmpeg here, or ffmpeg can't read a
	// blob): Reviews, nothing placed, the download as it was.
	r = newCueRig(t, true, album)
	r.h.c.probeAudioFn = fakeImageProbe(45)
	img := blobTrack(t, r.dl, "Radiohead - OK Computer.flac", 1)
	writeFile(t, filepath.Join(r.dl, "Radiohead - OK Computer.cue"), okcCue)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	rv = reviewFor(t, r.h.c, hashFor(r.release))
	if !strings.Contains(rv.Reason, "Splitting the cue-sheet disc image into tracks failed") {
		t.Fatalf("review %+v", rv)
	}
	if al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID); al.HaveTracks != 0 {
		t.Errorf("placed %d", al.HaveTracks)
	}
	if fi, err := os.Stat(img); err != nil || fi.Size() != 300<<10 {
		t.Errorf("the image changed: %v", err)
	}
	if jobs := stagingJobs(r.h.c.imp.MusicDir()); len(jobs) != 0 {
		t.Errorf("staging left: %v", jobs)
	}
}

// stagingJobs lists the job folders in a music folder's staging area.
func stagingJobs(musicDir string) []string {
	entries, _ := os.ReadDir(stage.Root(musicDir))
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// A split interrupted by a crash is deleted at the next start.
func TestRecoverMusicStagingDeletesAnInterruptedSplit(t *testing.T) {
	h, _ := musicImportHarness(t)
	img := blobTrack(t, t.TempDir(), "Album.flac", 1)
	j, err := stage.Start(h.c.imp.MusicDir(), stage.Manifest{Kind: stage.KindSplit, Items: []stage.Item{{Source: img}, {Source: img}}}, h.c.stageDeps())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range j.SplitFiles() {
		writeFile(t, p, "half a track")
	}
	rep, err := h.c.RecoverMusicStaging(h.ctx)
	if err != nil || rep.RolledBak != 2 {
		t.Fatalf("recover %+v %v", rep, err)
	}
	if _, err := os.Stat(j.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the split job is still there: %v", err)
	}
	if fi, err := os.Stat(img); err != nil || fi.Size() != 300<<10 {
		t.Errorf("the image: %v", err)
	}
}
