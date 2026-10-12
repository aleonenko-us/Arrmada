package audiobook

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercises the real ffmpeg on generated tones — never real media. It runs only with
// ARRMADA_FFMPEG_IT set, inside the app image (which bundles ffmpeg), so CI and a
// developer's machine stay green and fast.
func TestMergeAgainstRealFFmpeg(t *testing.T) {
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real merges")
	}
	if !Available() {
		t.Fatal("ARRMADA_FFMPEG_IT is set but ffmpeg/ffprobe aren't on PATH")
	}
	ctx := context.Background()
	dir := t.TempDir()
	gen := func(name string, args ...string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		full := append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)
		full = append(full, p)
		if out, err := exec.Command("ffmpeg", full...).CombinedOutput(); err != nil {
			t.Fatalf("generating %s: %v %s", name, err, out)
		}
		return p
	}
	tone := func(freq string, secs string) []string {
		return []string{"-f", "lavfi", "-i", "sine=frequency=" + freq + ":duration=" + secs}
	}
	cover := gen("cover.png", "-f", "lavfi", "-i", "color=c=orange:s=64x64", "-frames:v", "1")
	srcTags := []string{"-metadata", "title=Chapter 1", "-metadata", "album=The Tone Book (Unabridged)",
		"-metadata", "artist=a. sine", "-metadata", "track=1/2"}

	aac1 := gen("aac1.m4a", append(append(tone("440", "3"), "-c:a", "aac"), srcTags...)...)
	aac2 := gen("aac2.m4a", append(tone("660", "2"), "-c:a", "aac")...)
	// A tagged MP3 with an embedded cover, and one without.
	mp31 := gen("m1.mp3", append(append(tone("440", "2"), "-i", cover, "-map", "0:a", "-map", "1:v", "-c:a", "libmp3lame",
		"-c:v", "png", "-disposition:v:0", "attached_pic", "-id3v2_version", "3"), srcTags...)...)
	mp32 := gen("m2.mp3", append(tone("550", "2"), "-c:a", "libmp3lame")...)
	// An MP3 whose cover is a GIF, which an MP4 can't hold: the merge goes ahead without it.
	gifCover := gen("cover.gif", "-f", "lavfi", "-i", "color=c=blue:s=32x32", "-frames:v", "1", "-c:v", "gif")
	mp3gif := filepath.Join(dir, "gif.mp3")
	gifArgs := append(append([]string{"-y", "-loglevel", "error"}, tone("440", "2")...), "-i", gifCover, "-map", "0:a", "-map", "1:v",
		"-c:a", "libmp3lame", "-c:v", "copy", "-disposition:v:0", "attached_pic", "-id3v2_version", "3", mp3gif)
	haveGIF := exec.Command("ffmpeg", gifArgs...).Run() == nil

	opts := MergeOptions{Title: "Library Title", Author: "Library Author", Series: "The Sine Cycle", SeriesPosition: 2,
		Year: 2021, Narrator: "B. Square"}
	for _, c := range []struct {
		name      string
		files     []string
		wantCopy  bool
		wantSecs  float64
		wantCover bool
		skip      bool
	}{
		{"matching AAC", []string{aac1, aac2}, true, 5, false, false},
		{"MP3 with cover", []string{mp31, mp32}, false, 4, true, false},
		{"MP3 with a cover MP4 can't hold", []string{mp3gif, mp32}, false, 4, false, !haveGIF},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.skip {
				t.Skip("this ffmpeg can't write a GIF cover into an MP3")
			}
			if p := PlanFor(ctx, c.files); p.Copy != c.wantCopy {
				t.Errorf("copy = %v, want %v (plan %+v)", p.Copy, c.wantCopy, p)
			}
			// A hidden temp name with no audio extension: the format must not be guessed
			// from the name.
			out := filepath.Join(dir, ".Book.m4b.merging")
			_ = os.Remove(out)
			res, err := Merge(ctx, c.files, out, opts)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if math.Abs(res.SourceSeconds-c.wantSecs) > 0.5 || math.Abs(res.OutputSeconds-res.SourceSeconds) > 0.5 {
				t.Errorf("durations = %+v, want about %.0fs both", res, c.wantSecs)
			}
			if d, err := Duration(ctx, out); err != nil || math.Abs(d-res.OutputSeconds) > 0.01 {
				t.Errorf("Duration = %v, %v", d, err)
			}
			// The library's record, not the chapter file's tags.
			tags := readTags(ctx, out)
			for k, want := range map[string]string{"title": "Library Title", "artist": "Library Author",
				"album_artist": "Library Author", "album": "Library Title", "composer": "B. Square",
				"grouping": "The Sine Cycle #2", "genre": "Audiobook", "date": "2021", "media_type": "2"} {
				if tags[k] != want {
					t.Errorf("%s tag = %q, want %q (tags %v)", k, tags[k], want, tags)
				}
			}
			if tags["track"] != "" {
				t.Errorf("a chapter's track number carried over: %v", tags)
			}
			ch, _ := exec.Command("ffprobe", "-v", "quiet", "-show_entries", "chapter=id", "-of", "csv=p=0", out).Output()
			if n := len(strings.Fields(string(ch))); n != len(c.files) {
				t.Errorf("chapters = %d, want %d", n, len(c.files))
			}
			v, _ := exec.Command("ffprobe", "-v", "quiet", "-select_streams", "v",
				"-show_entries", "stream_disposition=attached_pic", "-of", "csv=p=0", out).Output()
			pic := strings.TrimSpace(string(v))
			if hasCover := pic != ""; hasCover != c.wantCover {
				t.Errorf("cover present = %v, want %v", hasCover, c.wantCover)
			}
			if c.wantCover && pic != "1" {
				t.Errorf("the cover isn't marked attached_pic: %q", pic)
			}
		})
	}

	if _, err := Duration(ctx, filepath.Join(dir, "missing.mp3")); err == nil {
		t.Error("Duration of a missing file must be an error")
	}
}
