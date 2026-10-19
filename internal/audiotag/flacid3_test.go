package audiotag

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// prependID3 puts an ID3v2.3 tag holding a title in front of the file.
func prependID3(t *testing.T, path, title string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frame := append([]byte{3}, []byte(title)...)
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
	if err := os.WriteFile(path, append(out, b...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendAPE adds an APEv2 tag (header and footer) with a big binary item, as some
// taggers store a cover.
func appendAPE(t *testing.T, path string, big int) {
	t.Helper()
	var items bytes.Buffer
	_ = binary.Write(&items, binary.LittleEndian, uint32(big))
	_ = binary.Write(&items, binary.LittleEndian, uint32(1<<1))
	items.WriteString("Cover Art (Front)\x00")
	items.Write(bytes.Repeat([]byte{0xFF, 0xFB}, big/2))
	hdr := func(header bool) []byte {
		var b bytes.Buffer
		b.WriteString("APETAGEX")
		_ = binary.Write(&b, binary.LittleEndian, uint32(2000))
		_ = binary.Write(&b, binary.LittleEndian, uint32(items.Len()+32))
		_ = binary.Write(&b, binary.LittleEndian, uint32(1))
		flags := uint32(1 << 31)
		if header {
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

func audioBytes(t *testing.T, path string) []byte {
	t.Helper()
	off, n, err := AudioRange(path)
	if err != nil {
		t.Fatalf("AudioRange(%s): %v", path, err)
	}
	b, _ := os.ReadFile(path)
	return b[off : off+n]
}

// A FLAC behind an ID3v2 tag is a FLAC: its Vorbis comments are read and written, the
// ID3 tag in front is dropped, and the frames are untouched.
func TestFLACBehindID3(t *testing.T) {
	p := blobFLAC(t, t.TempDir())
	before := audioBytes(t, p)
	prependID3(t, p, "id3 title")
	if got := audioBytes(t, p); !bytes.Equal(got, before) {
		t.Fatal("AudioRange doesn't skip the ID3 tag")
	}
	if err := Write(p, Tags{KeyTitle: {"House Title"}}, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !bytes.HasPrefix(raw, []byte("fLaC")) {
		t.Errorf("the file starts %q, want the ID3 tag gone", raw[:4])
	}
	if got, err := Read(p); err != nil || got.Get(KeyTitle) != "House Title" {
		t.Errorf("read back %v %v", got, err)
	}
	if !bytes.Equal(audioBytes(t, p), before) {
		t.Error("the FLAC frames changed")
	}
}

// An MP3's audio range leaves out an ID3v2 tag in front and APE/ID3v1 tags behind, so a
// tag write that drops a big APE cover still shows the same audio.
func TestMP3AudioRangeIgnoresTags(t *testing.T) {
	p := blobMP3(t, t.TempDir())
	before := audioBytes(t, p)
	appendAPE(t, p, 200000)
	if got := audioBytes(t, p); !bytes.Equal(got, before) {
		t.Fatal("AudioRange includes the APE tag")
	}
	if err := Write(p, Tags{KeyTitle: {"x"}}, WriteOptions{FrontCover: &Picture{Data: testPNG(t, 9)}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(audioBytes(t, p), before) {
		t.Error("the MPEG frames changed")
	}
	if _, _, err := AudioRange(p + ".missing"); err == nil {
		t.Error("a missing file has an audio range")
	}
}
