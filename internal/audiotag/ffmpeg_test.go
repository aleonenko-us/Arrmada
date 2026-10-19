package audiotag

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests make synthetic audio with ffmpeg (a sine tone; never real media)
// and use ffmpeg/ffprobe as an independent reader. Without ffmpeg on PATH they
// skip, unless ARRMADA_FFMPEG_IT=1 says ffmpeg is expected. The full format
// matrix lives in proof_it_test.go.
func requireFFmpeg(t testing.TB) {
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

func run(t testing.TB, name string, args ...string) []byte {
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

// makeAudio encodes a 2 s stereo sine tone into dir/name with some
// pre-existing tags: a house tag (title) that Write must replace and non-house
// tags it must keep.
func makeAudio(t testing.TB, dir, name string, codec ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2:sample_rate=44100", "-ac", "2"}
	args = append(args, codec...)
	args = append(args,
		"-metadata", "title=Old Title",
		"-metadata", "comment=keep me",
		"-metadata", "custom_note=keep me too",
		p)
	run(t, "ffmpeg", args...)
	return p
}

// audioHash hashes the compressed audio packets: it changes if any audio byte
// (or an MP4 chunk offset) is wrong, but not when only tags change.
func audioHash(t testing.TB, p string) string {
	t.Helper()
	return strings.TrimSpace(string(run(t, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", p, "-map", "0:a", "-c", "copy", "-f", "md5", "-")))
}

// pcmHash hashes the decoded audio.
func pcmHash(t testing.TB, p string) string {
	t.Helper()
	return strings.TrimSpace(string(run(t, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", p, "-map", "0:a", "-f", "md5", "-")))
}

// probeTags returns ffprobe's view of the container and stream tags (Ogg keeps
// its comments on the stream), keys lower-cased.
func probeTags(t testing.TB, p string) map[string]string {
	t.Helper()
	out := run(t, "ffprobe", "-hide_banner", "-loglevel", "error", "-show_entries", "format_tags:stream_tags", "-of", "json", p)
	var v struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Tags map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("ffprobe json: %v\n%s", err, out)
	}
	m := map[string]string{}
	add := func(tags map[string]string) {
		for k, val := range tags {
			m[strings.ToLower(k)] = val
		}
	}
	for _, s := range v.Streams {
		add(s.Tags)
	}
	add(v.Format.Tags)
	return m
}

// injectFreeform adds a non-house freeform atom to an MP4 through the package's
// own rewrite path (ffmpeg can't write one).
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

// A foreign freeform atom must survive writes by either backend.
func TestMP4ForeignFreeformSurvives(t *testing.T) {
	requireFFmpeg(t)
	for _, b := range backends {
		t.Run(b.String(), func(t *testing.T) {
			p := makeAudio(t, t.TempDir(), "x.m4a", "-c:a", "aac", "-b:a", "96k")
			injectFreeform(t, p, "MOOD", "calm")
			for i := 0; i < 2; i++ { // twice: once adding house tags, once replacing them
				if err := New(b).Write(p, houseFixture().Tags(), WriteOptions{}); err != nil {
					t.Fatal(err)
				}
				if got := probeTags(t, p)["mood"]; got != "calm" {
					t.Fatalf("write %d: MOOD = %q", i+1, got)
				}
			}
		})
	}
}

// A write must not reach the file through another hardlink: the seeding copy
// stays byte-identical.
func TestWriteBreaksHardlink(t *testing.T) {
	requireFFmpeg(t)
	for _, b := range backends {
		t.Run(b.String(), func(t *testing.T) {
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
			tg := New(b)
			if err := tg.Write(p, houseFixture().Tags(), WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			if now, _ := os.ReadFile(seed); !bytes.Equal(now, orig) {
				t.Error("hardlinked copy was modified")
			}
			if tags, _ := tg.Read(seed); tags.Get(KeyTitle) != "Old Title" {
				t.Errorf("seed title %q", tags.Get(KeyTitle))
			}
			if tags, _ := tg.Read(p); tags.Get(KeyTitle) != houseFixture().Title {
				t.Errorf("written title %q", tags.Get(KeyTitle))
			}
			a, _ := os.Stat(p)
			s, _ := os.Stat(seed)
			if os.SameFile(a, s) {
				t.Error("written path still shares the inode")
			}
		})
	}
}

// An MP3 with no ID3v2 tag at all gets one; the audio is untouched.
func TestMP3WithoutTag(t *testing.T) {
	requireFFmpeg(t)
	for _, b := range backends {
		t.Run(b.String(), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "bare.mp3")
			run(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1",
				"-c:a", "libmp3lame", "-b:a", "128k", "-id3v2_version", "0", "-write_xing", "0", p)
			if raw, _ := os.ReadFile(p); bytes.HasPrefix(raw, []byte("ID3")) {
				t.Fatal("setup: file already has an ID3 tag")
			}
			hash0 := audioHash(t, p)
			want := houseFixture().Tags()
			tg := New(b)
			if err := tg.Write(p, want, WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			got, err := tg.Read(p)
			if err != nil {
				t.Fatal(err)
			}
			diffTags(t, got, want)
			if h := audioHash(t, p); h != hash0 {
				t.Error("audio changed")
			}
		})
	}
}
