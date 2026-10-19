package stage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

// The tag write and its check on real files of every shape the review tried (2026-10-20):
// synthetic audio made by ffmpeg (lavfi noise, never real media), tagged through a job
// with the real tag library and the real checks. ARRMADA_FFMPEG_IT=1 only.

func requireIT(t *testing.T) {
	t.Helper()
	if os.Getenv("ARRMADA_FFMPEG_IT") != "1" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 to run the real-file staging tests")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not on PATH but ARRMADA_FFMPEG_IT=1", bin)
		}
	}
}

func itFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

func itJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(r.Intn(256)), G: uint8(r.Intn(256)), B: uint8(r.Intn(256)), A: 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return b.Bytes()
}

// itAPE appends an APEv2 tag (header and footer): a title and, when big > 0, a binary
// cover item of that size.
func itAPE(t *testing.T, path string, big int) {
	t.Helper()
	var items bytes.Buffer
	item := func(key string, val []byte, flags uint32) {
		_ = binary.Write(&items, binary.LittleEndian, uint32(len(val)))
		_ = binary.Write(&items, binary.LittleEndian, flags)
		items.WriteString(key)
		items.WriteByte(0)
		items.Write(val)
	}
	item("Title", []byte("ape title"), 0)
	if big > 0 {
		item("Cover Art (Front)", append([]byte("cover.jpg\x00"), bytes.Repeat([]byte{0x5a}, big)...), 1<<1)
	}
	hdr := func(isHeader bool) []byte {
		var b bytes.Buffer
		b.WriteString("APETAGEX")
		_ = binary.Write(&b, binary.LittleEndian, uint32(2000))
		_ = binary.Write(&b, binary.LittleEndian, uint32(items.Len()+32))
		_ = binary.Write(&b, binary.LittleEndian, uint32(2))
		flags := uint32(1 << 31)
		if isHeader {
			flags |= 1 << 29
		}
		_ = binary.Write(&b, binary.LittleEndian, flags)
		b.Write(make([]byte, 8))
		return b.Bytes()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(hdr(true))
	f.Write(items.Bytes())
	f.Write(hdr(false))
}

// itPrependID3 puts an ID3v2.3 tag in front of the file.
func itPrependID3(t *testing.T, path string) {
	t.Helper()
	b, _ := os.ReadFile(path)
	frame := append([]byte{3}, []byte("id3 title")...)
	var fr bytes.Buffer
	fr.WriteString("TIT2")
	_ = binary.Write(&fr, binary.BigEndian, uint32(len(frame)))
	fr.Write([]byte{0, 0})
	fr.Write(frame)
	pad := make([]byte, 64)
	size := fr.Len() + len(pad)
	out := append([]byte("ID3\x03\x00\x00"), byte(size>>21&0x7f), byte(size>>14&0x7f), byte(size>>7&0x7f), byte(size&0x7f))
	out = append(out, fr.Bytes()...)
	out = append(out, pad...)
	_ = os.WriteFile(path, append(out, b...), 0o644)
}

func itHash(ctx context.Context, p string) (string, error) {
	s, err := mediainfo.AudioMD5(ctx, p)
	if errors.Is(err, mediainfo.ErrNoFFmpeg) {
		return "", ErrNoDecoder
	}
	return s, err
}

var itTags = audiotag.Tags{
	audiotag.KeyTitle: {"Come Together"}, audiotag.KeyArtist: {"AC/DC & Friends"}, audiotag.KeyArtists: {"AC/DC", "Friends"},
	audiotag.KeyAlbum: {"Abbey Road"}, audiotag.KeyAlbumArtist: {"The Beatles"}, audiotag.KeyTrackNumber: {"1"},
	audiotag.KeyTrackTotal: {"17"}, audiotag.KeyDiscNumber: {"1"}, audiotag.KeyDiscTotal: {"1"},
	audiotag.KeyDate: {"1969-09-26"}, audiotag.KeyOriginalDate: {"1969-09-26"}, audiotag.KeyReleaseType: {"album", "compilation"},
	audiotag.KeyMBRecordingID: {"8e3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"}, audiotag.KeyGenre: {"Rock", "Pop"},
	audiotag.KeyCompilation: {"1"}, audiotag.KeyMedia: {"Digital Media"},
}

func TestTagAndCheckRealFormats(t *testing.T) {
	requireIT(t)
	cover := itJPEG(500, 500)
	dir := t.TempDir()
	noise := func(d string, rate int) []string {
		return []string{"-f", "lavfi", "-i", "anoisesrc=d=" + d + ":c=pink:r=" + strconv.Itoa(rate) + ":a=0.3", "-ac", "2"}
	}
	for _, c := range []struct {
		name string
		make func(p string)
		ext  string
	}{
		{"mp3-id3v1", func(p string) {
			itFFmpeg(t, append(noise("12", 44100), "-c:a", "libmp3lame", "-b:a", "320k", "-write_id3v1", "1", p)...)
		}, ".mp3"},
		{"mp3-short-2s", func(p string) { itFFmpeg(t, append(noise("2", 44100), "-c:a", "libmp3lame", "-b:a", "128k", p)...) }, ".mp3"},
		{"mp3-vbr-ape-small", func(p string) {
			itFFmpeg(t, append(noise("12", 44100), "-c:a", "libmp3lame", "-q:a", "2", p)...)
			itAPE(t, p, 0)
		}, ".mp3"},
		{"mp3-short-ape-bigcover", func(p string) {
			itFFmpeg(t, append(noise("3", 44100), "-c:a", "libmp3lame", "-b:a", "128k", p)...)
			itAPE(t, p, 200000)
		}, ".mp3"},
		{"mp3-noxing", func(p string) {
			itFFmpeg(t, append(noise("12", 44100), "-c:a", "libmp3lame", "-q:a", "2", "-write_xing", "0", p)...)
		}, ".mp3"},
		{"opus", func(p string) { itFFmpeg(t, append(noise("12", 48000), "-c:a", "libopus", "-b:a", "128k", p)...) }, ".opus"},
		{"ogg", func(p string) { itFFmpeg(t, append(noise("12", 44100), "-c:a", "libvorbis", p)...) }, ".ogg"},
		{"m4a-alac", func(p string) { itFFmpeg(t, append(noise("12", 44100), "-c:a", "alac", p)...) }, ".m4a"},
		{"m4a-aac", func(p string) { itFFmpeg(t, append(noise("12", 44100), "-c:a", "aac", "-b:a", "256k", p)...) }, ".m4a"},
		{"flac-16", func(p string) { itFFmpeg(t, append(noise("12", 44100), "-c:a", "flac", p)...) }, ".flac"},
		{"flac-id3-prefixed", func(p string) {
			itFFmpeg(t, append(noise("12", 44100), "-c:a", "flac", "-metadata", "title=Vorbis Old Title", p)...)
			itPrependID3(t, p)
		}, ".flac"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.deps.Tagger = DefaultTagger()
			w.deps.AudioHash = itHash
			src := filepath.Join(dir, c.name+c.ext)
			c.make(src)
			before, _ := os.ReadFile(src)
			j, err := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "A", "x"+c.ext)}}}, w.deps)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Stage(0); err != nil {
				t.Fatal(err)
			}
			staged := j.Items()[0].Staged
			if err := j.Tag(context.Background(), 0, itTags, &audiotag.Picture{Data: cover}); err != nil {
				t.Fatalf("Tag: %v", err)
			}
			if c.name == "flac-id3-prefixed" {
				b, _ := os.ReadFile(staged)
				if !bytes.HasPrefix(b, []byte("fLaC")) {
					t.Errorf("staged copy starts %q, want the ID3 tag gone", b[:4])
				}
				out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title", "-of", "default=nw=1", staged).CombinedOutput()
				if !strings.Contains(string(out), "Come Together") {
					t.Errorf("players see the title %q", out)
				}
			}
			if after, _ := os.ReadFile(src); !bytes.Equal(before, after) {
				t.Error("the source changed")
			}
			if err := j.Abandon("test"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A decode that can't run (here: cancelled) is "try again later", never Review.
func TestDecodeFailureIsTransient(t *testing.T) {
	requireIT(t)
	w := newWorld(t)
	w.deps.Tagger = DefaultTagger()
	w.deps.AudioHash = itHash
	src := filepath.Join(t.TempDir(), "a.m4a")
	itFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=d=3:c=pink:r=44100:a=0.3", "-ac", "2", "-c:a", "aac", src)
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "A", "x.m4a")}}}, w.deps)
	_ = j.Stage(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := j.Tag(ctx, 0, itTags, nil)
	if !errors.Is(err, ErrTransient) || errors.Is(err, ErrVerify) {
		t.Errorf("Tag with the decode cancelled: %v, want ErrTransient", err)
	}
	_ = j.Abandon("test")
}
