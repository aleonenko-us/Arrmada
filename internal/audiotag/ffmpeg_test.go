package audiotag

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These tests make synthetic audio with ffmpeg (a 1 s sine tone; never real
// media) and use ffmpeg/ffprobe as an independent reader. Without ffmpeg on
// PATH they skip, unless ARRMADA_FFMPEG_IT=1 says ffmpeg is expected.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("ARRMADA_FFMPEG_IT") == "1" {
				t.Fatalf("%s not on PATH but ARRMADA_FFMPEG_IT=1", bin)
			}
			t.Skipf("%s not on PATH", bin)
		}
	}
}

func run(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out
}

// makeAudio encodes a sine tone into dir/name with some pre-existing tags: a
// house tag (title) that Write must replace and non-house tags it must keep.
func makeAudio(t *testing.T, dir, name string, codec ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1"}
	args = append(args, codec...)
	args = append(args,
		"-metadata", "title=Old Title",
		"-metadata", "comment=keep me",
		"-metadata", "custom_note=keep me too",
		p)
	run(t, "ffmpeg", args...)
	return p
}

// audioHash hashes the compressed audio packets, so it changes if any audio
// byte (or an MP4 chunk offset) is wrong, but not when only tags change.
func audioHash(t *testing.T, p string) string {
	t.Helper()
	return strings.TrimSpace(string(run(t, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", p, "-map", "0:a", "-c", "copy", "-f", "md5", "-")))
}

// probeTags returns ffprobe's view of the container tags, keys lower-cased.
func probeTags(t *testing.T, p string) map[string]string {
	t.Helper()
	out := run(t, "ffprobe", "-hide_banner", "-loglevel", "error", "-show_entries", "format_tags", "-of", "json", p)
	var v struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("ffprobe json: %v\n%s", err, out)
	}
	m := map[string]string{}
	for k, val := range v.Format.Tags {
		m[strings.ToLower(k)] = val
	}
	return m
}

type formatCase struct {
	name  string
	file  string
	codec []string
	// keptTags are non-house tags (ffprobe keys) that must survive every Write.
	keptTags []string
}

var formatCases = []formatCase{
	{"flac", "x.flac", []string{"-c:a", "flac"}, []string{"comment", "custom_note"}},
	{"mp3", "x.mp3", []string{"-c:a", "libmp3lame", "-b:a", "128k"}, []string{"comment", "custom_note"}},
	// An ID3v2.3 source is upgraded to v2.4 on write, keeping its other frames.
	{"mp3-v23", "x.mp3", []string{"-c:a", "libmp3lame", "-b:a", "128k", "-id3v2_version", "3"}, []string{"comment", "custom_note"}},
	// ffmpeg's MP4 muxer drops unknown keys, so only comment and encoder exist.
	// The default layout puts moov after mdat; faststart puts it first, which
	// makes every tag-size change shift the chunk offsets.
	{"m4a", "x.m4a", []string{"-c:a", "aac", "-b:a", "96k"}, []string{"comment", "encoder"}},
	{"m4a-faststart", "x.m4a", []string{"-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart"}, []string{"comment", "encoder"}},
}

func TestRoundTripAllFormats(t *testing.T) {
	requireFFmpeg(t)
	for _, fc := range formatCases {
		t.Run(fc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := makeAudio(t, dir, fc.file, fc.codec...)
			hash0 := audioHash(t, p)
			before := probeTags(t, p)
			for _, k := range fc.keptTags {
				if before[k] == "" {
					t.Fatalf("setup: ffmpeg did not write %q: %v", k, before)
				}
			}
			if strings.HasPrefix(fc.name, "m4a") {
				// MP4 can't get a foreign freeform atom from ffmpeg; add one
				// through the same rewrite path to prove it survives.
				injectFreeform(t, p, "MOOD", "calm")
			}

			// 1. Write every house field and read it back.
			want := houseFixture()
			if err := Write(p, want); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got, err := Read(p)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			diffTags(t, got, want)
			if h := audioHash(t, p); h != hash0 {
				t.Errorf("audio changed: %s -> %s", hash0, h)
			}

			// 2. Independent reader: ffprobe sees the new values and the kept tags.
			after := probeTags(t, p)
			t.Logf("ffprobe after full write: %v", after)
			if after["title"] != want.Title || after["album"] != want.Album || after["artist"] != want.Artist {
				t.Errorf("ffprobe title/album/artist = %q / %q / %q", after["title"], after["album"], after["artist"])
			}
			for _, k := range fc.keptTags {
				if after[k] != before[k] {
					t.Errorf("non-house tag %q: %q -> %q", k, before[k], after[k])
				}
			}
			checkProbeMulti(t, fc.name, after)
			if strings.HasPrefix(fc.name, "m4a") {
				if after["mood"] != "calm" {
					t.Errorf("foreign freeform MOOD lost: %v", after)
				}
			}
			raw, _ := os.ReadFile(p)
			switch {
			case fc.name == "mp3":
				// ffprobe shows only the first value, so check the frame bytes:
				// one TXXX value with the artists NUL-separated.
				if !bytes.Contains(raw, []byte("ARTISTS\x00"+strings.Join(want.Artists, "\x00"))) {
					t.Error("TXXX:ARTISTS does not hold NUL-separated values")
				}
			case strings.HasPrefix(fc.name, "m4a"):
				// One ARTISTS freeform atom with consecutive data children.
				var datas []byte
				for _, a := range want.Artists {
					datas = append(datas, dataBox(dataUTF8, []byte(a))...)
				}
				if !bytes.Contains(raw, append(box("name", u32(0), []byte("ARTISTS")), datas...)) {
					t.Error("----:com.apple.iTunes:ARTISTS does not hold one data atom per value")
				}
			}
			if fc.name == "flac" {
				// ARTISTS must be two separate comments, not one joined value.
				for _, a := range want.Artists {
					if !bytes.Contains(raw, []byte("ARTISTS="+a)) {
						t.Errorf("no separate comment ARTISTS=%s", a)
					}
				}
				n := bytes.Count(raw, []byte("ARTISTS=")) - bytes.Count(raw, []byte("ALBUMARTISTS="))
				if n != 2 {
					t.Errorf("ARTISTS= appears %d times, want 2", n)
				}
			}

			// 3. Re-write a changed subset: Read, modify, Write.
			next := got
			next.Title = "Hoppípolla"
			next.Artists = []string{"Jónsi", "Kjartan", "Georg", "Orri"}
			next.Genres = []string{"Ambient"}
			next.Compilation = false
			next.Barcode = ""
			next.TrackNumber = 4
			next.MusicBrainzArtistIDs = next.MusicBrainzArtistIDs[:1]
			if err := Write(p, next); err != nil {
				t.Fatalf("second Write: %v", err)
			}
			got2, err := Read(p)
			if err != nil {
				t.Fatalf("second Read: %v", err)
			}
			diffTags(t, got2, next)
			if h := audioHash(t, p); h != hash0 {
				t.Errorf("audio changed on second write: %s -> %s", hash0, h)
			}
			after2 := probeTags(t, p)
			for _, k := range fc.keptTags {
				if after2[k] != before[k] {
					t.Errorf("after second write, non-house tag %q: %q -> %q", k, before[k], after2[k])
				}
			}
			if strings.HasPrefix(fc.name, "m4a") && after2["mood"] != "calm" {
				t.Error("foreign freeform MOOD lost on second write")
			}
			if after2["title"] != next.Title {
				t.Errorf("ffprobe title %q", after2["title"])
			}

			// 4. Clearing everything removes the house tags but nothing else.
			if err := Write(p, Tags{}); err != nil {
				t.Fatalf("clearing Write: %v", err)
			}
			if got3, err := Read(p); err != nil || !reflect.DeepEqual(got3, Tags{}) {
				t.Errorf("after clearing: %+v, %v", got3, err)
			}
			after3 := probeTags(t, p)
			for _, k := range fc.keptTags {
				if after3[k] != before[k] {
					t.Errorf("after clearing, non-house tag %q lost", k)
				}
			}
			if h := audioHash(t, p); h != hash0 {
				t.Errorf("audio changed after clearing")
			}
			assertOnlyFile(t, dir, fc.file)
		})
	}
}

// checkProbeMulti checks what ffmpeg's demuxers make of the multi-value fields.
// ffmpeg 7.1 joins repeated Vorbis comments with ";" but shows only the first
// value of an ID3v2.4 multi-value frame or a multi-data MP4 atom, so all values
// are required for FLAC and the first value elsewhere.
func checkProbeMulti(t *testing.T, format string, tags map[string]string) {
	t.Helper()
	want := houseFixture()
	if format == "flac" {
		for k, vals := range map[string][]string{"artists": want.Artists, "genre": want.Genres, "musicbrainz_artistid": want.MusicBrainzArtistIDs} {
			if got := tags[k]; got != strings.Join(vals, ";") {
				t.Errorf("flac: ffprobe %s = %q, want all of %q", k, got, vals)
			}
		}
	}
	if v := tags["artists"]; !strings.Contains(v, want.Artists[0]) {
		t.Errorf("%s: ffprobe ARTISTS = %q, want it to contain %q", format, v, want.Artists[0])
	}
	t.Logf("%s: ffprobe ARTISTS=%q GENRE=%q MusicBrainz Artist Id=%q", format,
		tags["artists"], tags["genre"], tags["musicbrainz artist id"]+tags["musicbrainz_artistid"])
}

// injectFreeform adds a non-house freeform atom to an MP4 through the package's
// own rewrite path.
func injectFreeform(t *testing.T, p, name, value string) {
	t.Helper()
	item := box("----", box("mean", u32(0), []byte(itunesMean)), box("name", u32(0), []byte(name)), dataBox(dataUTF8, []byte(value)))
	src, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	err = replaceFile(p, func(dst *os.File) error {
		if err := rewriteMP4(src, dst, func(items []ilstItem) []byte {
			var b []byte
			for _, it := range items {
				b = append(b, it.raw...)
			}
			return append(b, item...)
		}); err != nil {
			return err
		}
		return src.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A write must not reach the file through another hardlink: the seeding copy
// stays byte-identical.
func TestWriteBreaksHardlink(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	p := makeAudio(t, dir, "x.flac", "-c:a", "flac")
	seed := filepath.Join(dir, "seeding.flac")
	if err := os.Link(p, seed); err != nil {
		t.Skipf("hardlinks not supported here: %v", err)
	}
	orig, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(p, houseFixture()); err != nil {
		t.Fatal(err)
	}
	now, _ := os.ReadFile(seed)
	if !bytes.Equal(now, orig) {
		t.Error("hardlinked copy was modified")
	}
	if tags, _ := Read(seed); tags.Title != "Old Title" {
		t.Errorf("seed title %q", tags.Title)
	}
	if tags, _ := Read(p); tags.Title != houseFixture().Title {
		t.Errorf("written title %q", tags.Title)
	}
	a, _ := os.Stat(p)
	b, _ := os.Stat(seed)
	if os.SameFile(a, b) {
		t.Error("written path still shares the inode")
	}
}

// An MP3 with no ID3v2 tag at all gets one; the audio is untouched.
func TestMP3WithoutTag(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bare.mp3")
	run(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:a", "libmp3lame", "-b:a", "128k", "-id3v2_version", "0", "-write_xing", "0", p)
	if b, _ := os.ReadFile(p); bytes.HasPrefix(b, []byte("ID3")) {
		t.Fatal("setup: file already has an ID3 tag")
	}
	hash0 := audioHash(t, p)
	want := houseFixture()
	if err := Write(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	diffTags(t, got, want)
	if h := audioHash(t, p); h != hash0 {
		t.Error("audio changed")
	}
}
