package audiotag

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFFmpegMultiValueExperiment records why ffmpeg ("-c copy -metadata") was
// rejected as the tag writer. It tries the three things the house tags need and
// logs what ffmpeg actually produced, checked with this package's reader and the
// raw bytes. It only logs: ffmpeg's behaviour is evidence for a decision, not a
// contract, so a future ffmpeg that fixes this shouldn't break the build. Run
// with -v to see the results.
//
// Observed with ffmpeg 7.1 (Debian): every case keeps only the last value, and
// the MP4 muxer writes no freeform atoms at all.
func TestFFmpegMultiValueExperiment(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	t.Logf("%s", firstLine(run(t, "ffmpeg", "-hide_banner", "-version")))
	meta := filepath.Join(dir, "meta.txt")
	if err := os.WriteFile(meta, []byte(";FFMETADATA1\nARTISTS=Simon\nARTISTS=Garfunkel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ff := func(args ...string) {
		run(t, "ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	}
	report := func(label, p string) {
		raw, _ := os.ReadFile(p)
		tags, err := New(Permissive).Read(p)
		if err != nil {
			t.Logf("RESULT %s: our reader failed: %v", label, err)
			return
		}
		t.Logf("RESULT %s: Artists read back = %q; raw has \"Simon\": %v, \"Garfunkel\": %v; ffprobe ARTISTS = %q",
			label, tags[KeyArtists], bytes.Contains(raw, []byte("Simon")), bytes.Contains(raw, []byte("Garfunkel")),
			probeTags(t, p)["artists"])
	}

	// (a) FLAC: two Vorbis comments with the same key.
	src := makeAudio(t, dir, "a.flac", "-c:a", "flac")
	ff("-i", src, "-c", "copy", "-metadata", "ARTISTS=Simon", "-metadata", "ARTISTS=Garfunkel", filepath.Join(dir, "a1.flac"))
	report("(a) FLAC, -metadata twice", filepath.Join(dir, "a1.flac"))
	ff("-i", src, "-i", meta, "-map", "0", "-map_metadata", "1", "-c", "copy", filepath.Join(dir, "a2.flac"))
	report("(a) FLAC, ffmetadata file with two lines", filepath.Join(dir, "a2.flac"))

	// (b) MP3: an ID3v2.4 TXXX frame with NUL-separated values. A NUL can't be
	// passed in argv, and ffmetadata has no escape for it, so repeating the key
	// is the only way to ask.
	src = makeAudio(t, dir, "b.mp3", "-c:a", "libmp3lame", "-b:a", "128k")
	b1 := filepath.Join(dir, "b1.mp3")
	ff("-i", src, "-c", "copy", "-id3v2_version", "4", "-metadata", "ARTISTS=Simon", "-metadata", "ARTISTS=Garfunkel", b1)
	report("(b) MP3 v2.4, -metadata twice", b1)
	raw, _ := os.ReadFile(b1)
	t.Logf("RESULT (b) MP3: raw contains \"Simon\\x00Garfunkel\": %v", bytes.Contains(raw, []byte("Simon\x00Garfunkel")))

	// (c) MP4: iTunes freeform atoms (----:com.apple.iTunes:NAME).
	src = makeAudio(t, dir, "c.m4a", "-c:a", "aac")
	c1 := filepath.Join(dir, "c1.m4a")
	ff("-i", src, "-c", "copy", "-metadata", "ARTISTS=Simon", "-metadata", "ARTISTS=Garfunkel",
		"-metadata", "MusicBrainz Track Id=abc", c1)
	report("(c) MP4 default muxer", c1)
	c2 := filepath.Join(dir, "c2.m4a")
	ff("-i", src, "-c", "copy", "-movflags", "use_metadata_tags", "-metadata", "ARTISTS=Simon",
		"-metadata", "ARTISTS=Garfunkel", "-metadata", "MusicBrainz Track Id=abc", c2)
	report("(c) MP4 -movflags use_metadata_tags", c2)
	for _, p := range []string{c1, c2} {
		raw, _ := os.ReadFile(p)
		t.Logf("RESULT (c) %s: has freeform \"----\"+\"com.apple.iTunes\": %v; has mdta keys: %v; title (\\xa9nam) kept: %v",
			filepath.Base(p), bytes.Contains(raw, []byte("----")) && bytes.Contains(raw, []byte(itunesMean)),
			bytes.Contains(raw, []byte("mdta")), bytes.Contains(raw, []byte("\xa9nam")))
	}
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return s
}
