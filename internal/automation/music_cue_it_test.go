package automation

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/music"
)

// A cue-sheet album end to end on real files (MUS-35): a 45-second synthetic image (lavfi
// noise) and the hand-written okcCue — a five-second hidden track, a pregap on track 2 —
// downloaded, matched, split in staging, house-tagged and placed. Each placed track,
// decoded, must be exactly its slice of the decoded image. ARRMADA_FFMPEG_IT=1 only.

func pcmOf(t *testing.T, path string) []byte {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0", "-c:a", "pcm_s16le", "-f", "s16le", "-").Output()
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return out
}

func TestCueAlbumImportsAsTracks(t *testing.T) {
	requireFFmpegIT(t)
	for _, c := range []struct {
		name      string
		writeTags bool
	}{{"house tags", true}, {"tags off", false}} {
		t.Run(c.name, func(t *testing.T) { cueImportOne(t, c.writeTags) })
	}
}

func cueImportOne(t *testing.T, writeTags bool) {
	album := []cueSpec{{"Airbag", 12000, false}, {"Paranoid Android", 13000, false}, {"Subterranean Homesick Alien", 15000, false}}
	r := newCueRig(t, true, album)
	r.h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
		return MusicPlacementPrefs{WriteTags: writeTags, Extras: map[string]bool{".cue": true, ".log": true}}
	}, nil, MusicAudioHash)
	img := filepath.Join(r.dl, "Radiohead - OK Computer.flac")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anoisesrc=d=45:c=pink:r=44100:a=0.3",
		"-ac", "2", "-sample_fmt", "s16", "-c:a", "flac", img).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	cuePath := filepath.Join(r.dl, "Radiohead - OK Computer.cue")
	writeFile(t, cuePath, okcCue)
	writeFile(t, filepath.Join(r.dl, "Radiohead - OK Computer.log"), "EAC extraction logfile")
	imgBefore, cueBefore := stateOf(t, img), stateOf(t, cuePath)
	whole := pcmOf(t, img)

	r.h.c.ImportMusicDownloads(r.h.ctx)

	al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if al.HaveTracks != 3 {
		var why string
		_ = r.h.c.db.QueryRow(`SELECT reason FROM import_reviews WHERE hash = ?`, hashFor(r.release)).Scan(&why)
		t.Fatalf("have %d of 3 (review: %q)", al.HaveTracks, why)
	}
	if s := r.grabStatus(t); s != grabStatusImported {
		t.Errorf("grab %q, want imported", s)
	}
	tracks, _ := r.svc.Tracks(r.h.ctx, r.album.ID)
	// Track k runs from its INDEX 01 to the next one's: 5–17 s, 17–30 s, 30 s–end; the
	// five seconds before track 1 (no track 0 on MusicBrainz) aren't imported.
	const frame = 4 // bytes per stereo s16 sample
	bounds := []int{5 * 44100, 17 * 44100, 30 * 44100, len(whole) / frame}
	for k, tk := range tracks {
		if !tk.HasFile || filepath.Ext(tk.FilePath) != ".flac" {
			t.Fatalf("track %d: %+v", k+1, tk)
		}
		if got, want := pcmOf(t, tk.FilePath), whole[bounds[k]*frame:bounds[k+1]*frame]; !bytes.Equal(got, want) {
			t.Errorf("track %d isn't its slice of the image: %d bytes, want %d", k+1, len(got), len(want))
		}
		tags, err := audiotag.Read(tk.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if writeTags {
			if tk.TagState != music.TagStateHouse || tags[audiotag.KeyTitle][0] != album[k].title || tags[audiotag.KeyTrackTotal][0] != "3" || tags[audiotag.KeyReleaseCountry][0] != "GB" {
				t.Errorf("track %d house tags: %v %v", k+1, tk.TagState, tags)
			}
		} else if tk.TagState != music.TagStateAsFound || len(tags[audiotag.KeyTitle]) == 0 || tags[audiotag.KeyTitle][0] != album[k].title {
			// As split: the cue sheet's own tags.
			t.Errorf("track %d tags as split: %v %v", k+1, tk.TagState, tags)
		}
	}
	albumDir := filepath.Dir(tracks[0].FilePath)
	for _, extra := range []string{"Radiohead - OK Computer.cue", "Radiohead - OK Computer.log"} {
		if _, err := os.Stat(filepath.Join(albumDir, extra)); err != nil {
			t.Errorf("extra %s not brought along: %v", extra, err)
		}
	}
	entries, _ := os.ReadDir(albumDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "Radiohead - OK Computer.flac") || len(entries) > 5 {
			t.Errorf("album folder: %v", entries)
			break
		}
	}
	// The download is exactly as it was; staging is empty.
	for p, before := range map[string]fileState{img: imgBefore, cuePath: cueBefore} {
		if now := stateOf(t, p); !bytes.Equal(now.content, before.content) || !now.mtime.Equal(before.mtime) || now.links != before.links {
			t.Errorf("%s changed", filepath.Base(p))
		}
	}
	if jobs := stagingJobs(r.h.c.imp.MusicDir()); len(jobs) != 0 {
		t.Errorf("staging left: %v", jobs)
	}
}

// makeImage encodes seconds of stereo noise as a 16-bit, 44.1 kHz FLAC disc image.
func makeImage(t *testing.T, path string, seconds string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anoisesrc=d="+seconds+":c=pink:r=44100:a=0.3",
		"-ac", "2", "-sample_fmt", "s16", "-c:a", "flac", path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func reviewReason(r *importRig) string {
	var why string
	_ = r.h.c.db.QueryRow(`SELECT reason FROM import_reviews WHERE hash = ?`, hashFor(r.release)).Scan(&why)
	return why
}

// Two discs, one image each, in one cue sheet (a multi-FILE cue) — and the same album as
// two disc folders with a cue sheet each: every track lands on its disc, exactly its
// slice of its image.
func TestCueTwoDiscImports(t *testing.T) {
	requireFFmpegIT(t)
	discs := [][]cueSpec{{{"In the Flesh", 20000, false}, {"The Thin Ice", 20000, false}}, {{"Hey You", 15000, false}, {"Nobody Home", 25000, false}}}
	cd := func(n int) string {
		return "  TRACK 01 AUDIO\n    TITLE \"" + discs[n][0].title + "\"\n    INDEX 01 00:00:00\n" +
			"  TRACK 02 AUDIO\n    TITLE \"" + discs[n][1].title + "\"\n    INDEX 01 00:" + []string{"20", "15"}[n] + ":00\n"
	}
	head := "PERFORMER \"Radiohead\"\nTITLE \"OK Computer\"\n"
	for _, layout := range []string{"one sheet", "a sheet per disc folder"} {
		t.Run(layout, func(t *testing.T) {
			r := newCueRig(t, true, discs...)
			r.h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs { return MusicPlacementPrefs{WriteTags: true} }, nil, MusicAudioHash)
			var imgs [2]string
			if layout == "one sheet" {
				imgs = [2]string{filepath.Join(r.dl, "CD1.flac"), filepath.Join(r.dl, "CD2.flac")}
				writeFile(t, filepath.Join(r.dl, "OK Computer.cue"), head+"FILE \"CD1.wav\" WAVE\n"+cd(0)+"FILE \"CD2.wav\" WAVE\n"+cd(1))
			} else {
				for n := range imgs {
					dir := filepath.Join(r.dl, []string{"CD1", "CD2"}[n])
					imgs[n] = filepath.Join(dir, "OK Computer.flac")
					writeFile(t, filepath.Join(dir, "OK Computer.cue"), head+"FILE \"OK Computer.flac\" WAVE\n"+cd(n))
				}
			}
			makeImage(t, imgs[0], "40")
			makeImage(t, imgs[1], "40")
			r.h.c.ImportMusicDownloads(r.h.ctx)
			if al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID); al.HaveTracks != 4 {
				t.Fatalf("have %d of 4 (review: %q)", al.HaveTracks, reviewReason(r))
			}
			tracks, _ := r.svc.Tracks(r.h.ctx, r.album.ID)
			for _, tk := range tracks {
				whole := pcmOf(t, imgs[tk.DiscNumber-1])
				from, to := 0, 20*44100
				switch {
				case tk.DiscNumber == 1 && tk.TrackNumber == 2:
					from, to = 20*44100, len(whole)/4
				case tk.DiscNumber == 2 && tk.TrackNumber == 1:
					from, to = 0, 15*44100
				case tk.DiscNumber == 2:
					from, to = 15*44100, len(whole)/4
				}
				if !bytes.Equal(pcmOf(t, tk.FilePath), whole[from*4:to*4]) {
					t.Errorf("disc %d track %d (%s) isn't its slice of its image", tk.DiscNumber, tk.TrackNumber, tk.Title)
				}
			}
			if jobs := stagingJobs(r.h.c.imp.MusicDir()); len(jobs) != 0 {
				t.Errorf("staging left: %v", jobs)
			}
		})
	}
}

// A hidden track before track 1 is kept as track 0 when MusicBrainz lists one.
func TestCueHiddenTrackKeptWhenListed(t *testing.T) {
	requireFFmpegIT(t)
	album := []cueSpec{{"Hidden", 5000, true}, {"Airbag", 12000, false}, {"Paranoid Android", 13000, false}, {"Subterranean Homesick Alien", 15000, false}}
	r := newCueRig(t, true, album)
	r.h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs { return MusicPlacementPrefs{WriteTags: true} }, nil, MusicAudioHash)
	img := filepath.Join(r.dl, "Radiohead - OK Computer.flac")
	makeImage(t, img, "45")
	writeFile(t, filepath.Join(r.dl, "Radiohead - OK Computer.cue"), okcCue)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	tracks, _ := r.svc.Tracks(r.h.ctx, r.album.ID)
	whole := pcmOf(t, img)
	var zero *music.Track
	for i := range tracks {
		if tracks[i].TrackNumber == 0 {
			zero = &tracks[i]
		}
	}
	if zero == nil || !zero.HasFile {
		t.Fatalf("track 0: %+v (review %q)", zero, reviewReason(r))
	}
	if !bytes.Equal(pcmOf(t, zero.FilePath), whole[:5*44100*4]) {
		t.Error("track 0 isn't the five seconds before track 1")
	}
}
