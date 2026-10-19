package audiotag

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// testPNG is a small generated image (never real artwork).
func testPNG(t testing.TB, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{R: shade, G: 40, B: 90, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// blobFLAC writes the smallest file the FLAC path accepts: the marker, a
// STREAMINFO block and some stand-in "frames" (copied byte for byte, never decoded).
func blobFLAC(t *testing.T, dir string) string {
	t.Helper()
	si := make([]byte, 34)
	si[10], si[11], si[12] = 0x0A, 0xC4, 0x42 // 44.1 kHz, 2 ch, 16 bit
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x80, 0, 0, 34}) // last block, STREAMINFO, 34 bytes
	b.Write(si)
	b.Write(bytes.Repeat([]byte{0xFF, 0xF8, 0x12, 0x34}, 512))
	p := filepath.Join(dir, "a.flac")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// blobMP3 writes MPEG frame-sync bytes with no tag at all.
func blobMP3(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, append([]byte{0xFF, 0xFB, 0x90, 0x00}, make([]byte, 4096)...), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The front cover round-trips, a second write without one keeps it, and a new
// one replaces it, while the frames after the metadata stay byte for byte.
func TestFrontCoverRoundTrip(t *testing.T) {
	for name, mk := range map[string]func(*testing.T, string) string{"flac": blobFLAC, "mp3": blobMP3} {
		t.Run(name, func(t *testing.T) {
			p := mk(t, t.TempDir())
			raw0, _ := os.ReadFile(p)
			tail := raw0[len(raw0)-1024:]
			if _, err := ReadFrontCover(p); !errors.Is(err, ErrNoCover) {
				t.Fatalf("fresh file: %v, want ErrNoCover", err)
			}
			first := testPNG(t, 10)
			tags := Tags{KeyTitle: {"One"}, KeyTrackNumber: {"1"}, KeyTrackTotal: {"9"}}
			if err := Write(p, tags, WriteOptions{FrontCover: &Picture{Data: first}}); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadFrontCover(p); err != nil || !bytes.Equal(got, first) {
				t.Fatalf("cover after write: %d bytes, %v", len(got), err)
			}
			if err := Write(p, tags, WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			if got, _ := ReadFrontCover(p); !bytes.Equal(got, first) {
				t.Error("a write without a cover dropped the cover")
			}
			second := testPNG(t, 200)
			if err := Write(p, tags, WriteOptions{FrontCover: &Picture{Data: second, MIME: "image/png"}}); err != nil {
				t.Fatal(err)
			}
			if got, _ := ReadFrontCover(p); !bytes.Equal(got, second) {
				t.Error("the new cover didn't replace the old one")
			}
			raw1, _ := os.ReadFile(p)
			if !bytes.HasSuffix(raw1, tail) {
				t.Error("the audio after the metadata changed")
			}
			if got, err := Read(p); err != nil || got.Get(KeyTitle) != "One" {
				t.Errorf("tags after cover writes: %v %v", got, err)
			}
		})
	}
}

func TestFrontCoverRefusesBadImages(t *testing.T) {
	p := blobFLAC(t, t.TempDir())
	before, _ := os.ReadFile(p)
	for name, pic := range map[string]*Picture{
		"empty":      {},
		"not image":  {Data: []byte("GIF89a....")},
		"wrong mime": {Data: testPNG(t, 1), MIME: "image/jpeg"},
		"too big":    {Data: append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, MaxCoverBytes)...)},
	} {
		if err := Write(p, Tags{KeyTitle: {"x"}}, WriteOptions{FrontCover: pic}); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(before, after) {
		t.Error("a refused cover changed the file")
	}
}

// The Vorbis mirrors are written beside the house keys in FLAC and read back as
// the house values.
func TestFLACWritesVorbisMirrors(t *testing.T) {
	p := blobFLAC(t, t.TempDir())
	if err := Write(p, Tags{KeyTrackNumber: {"3"}, KeyTrackTotal: {"12"}, KeyDiscNumber: {"1"}, KeyDiscTotal: {"2"}, KeyOriginalDate: {"1997-09-22"}}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readFLAC(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"TOTALTRACKS": "12", "TOTALDISCS": "2", "ORIGINALYEAR": "1997", "TRACKTOTAL": "12"} {
		if len(raw[k]) != 1 || raw[k][0] != want {
			t.Errorf("%s = %q, want %q", k, raw[k], want)
		}
	}
	got, _ := Read(p)
	if got.Get(KeyOriginalDate) != "1997-09-22" || got.Get(KeyTrackTotal) != "12" {
		t.Errorf("read back %v", got)
	}
}
