package stage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/audiotag"
)

// The second review's real-file cases (2026-10-20). ARRMADA_FFMPEG_IT=1 only; synthetic
// audio made by ffmpeg.

// A trailer at the end of an MP3 that only looks like a tag (an APE footer whose size is
// garbage, a Lyrics3 size with no LYRICSBEGIN) is refused as unsupported: the writer would
// otherwise drop the audio it claims, and the byte check, reading the same trailer, would
// agree. Nothing is written; the file is placed untagged.
func TestBogusMP3TrailerIsRefused(t *testing.T) {
	requireIT(t)
	for _, tc := range []struct {
		name    string
		trailer func(size int64) []byte
	}{
		{"ape-footer-claims-60pct", func(size int64) []byte {
			var b bytes.Buffer
			b.WriteString("APETAGEX")
			_ = binary.Write(&b, binary.LittleEndian, uint32(2000))
			_ = binary.Write(&b, binary.LittleEndian, uint32(size*6/10))
			_ = binary.Write(&b, binary.LittleEndian, uint32(0))
			_ = binary.Write(&b, binary.LittleEndian, uint32(0))
			b.Write(make([]byte, 8))
			return b.Bytes()
		}},
		{"lyrics3-size-999999", func(int64) []byte { return []byte("999999LYRICS200") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.deps.Tagger = DefaultTagger()
			w.deps.AudioHash = itHash
			src := filepath.Join(t.TempDir(), "x.mp3")
			itFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=d=60:c=pink:r=44100:a=0.3", "-ac", "2", "-c:a", "libmp3lame", "-b:a", "320k", src)
			fi, err := os.Stat(src)
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write(tc.trailer(fi.Size()))
			f.Close()
			before, _ := os.ReadFile(src)
			j, err := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "A", "x.mp3")}}}, w.deps)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Stage(0); err != nil {
				t.Fatal(err)
			}
			// The writer refuses before touching the file: a plain copy writes the same.
			cp := filepath.Join(t.TempDir(), "copy.mp3")
			if err := os.WriteFile(cp, before, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := audiotag.New(audiotag.DefaultBackend).Write(cp, itTags, audiotag.WriteOptions{}); !errors.Is(err, audiotag.ErrUnsupported) {
				t.Fatalf("Write = %v, want audiotag.ErrUnsupported", err)
			}
			if got, _ := os.ReadFile(cp); !bytes.Equal(got, before) {
				t.Errorf("the refused write changed the file: %d bytes, was %d", len(got), len(before))
			}
			if err := j.Tag(context.Background(), 0, itTags, nil); !errors.Is(err, audiotag.ErrUnsupported) {
				t.Fatalf("Tag = %v, want audiotag.ErrUnsupported", err)
			}
			if got, _ := os.ReadFile(src); !bytes.Equal(got, before) {
				t.Error("the source changed")
			}
			_ = j.Abandon("test")
		})
	}
}

// A writer that damages a FLAC's STREAMINFO (total samples and MD5 zeroed) while copying
// the frames intact fails the check: the stream information is compared too.
func TestDamagedFLACStreamInfoFailsTheCheck(t *testing.T) {
	requireIT(t)
	w := newWorld(t)
	real := audiotag.New(audiotag.DefaultBackend)
	w.deps.Tagger = NewTagger(real.Read, real.ReadFrontCover, func(p string, tg audiotag.Tags, o audiotag.WriteOptions) error {
		if err := real.Write(p, tg, o); err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// "fLaC", a 4-byte block header, then STREAMINFO: total samples end in bytes
		// 13..17 (low 36 bits), the MD5 is 18..34.
		si := b[8 : 8+34]
		si[13] &^= 0x0f
		for i := 14; i < 34; i++ {
			si[i] = 0
		}
		return os.WriteFile(p, b, 0o644)
	})
	w.deps.AudioHash = itHash
	src := filepath.Join(t.TempDir(), "x.flac")
	itFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=d=10:c=pink:r=44100:a=0.3", "-ac", "2", "-c:a", "flac", src)
	j, err := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "A", "x.flac")}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Stage(0); err != nil {
		t.Fatal(err)
	}
	if err := j.Tag(context.Background(), 0, itTags, nil); !errors.Is(err, ErrVerify) {
		t.Fatalf("Tag = %v, want ErrVerify", err)
	}
	_ = j.Abandon("test")
}
