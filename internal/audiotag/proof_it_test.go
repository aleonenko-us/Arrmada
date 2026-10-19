package audiotag

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The proof harness: every codec the music import will see, through both
// backends, checked against ffmpeg/ffprobe as an independent reader. It only
// runs with ARRMADA_FFMPEG_IT=1 (it needs ffmpeg built with libmp3lame,
// libvorbis, libopus and the native aac/alac/flac encoders).
func requireProof(t testing.TB) {
	t.Helper()
	if os.Getenv("ARRMADA_FFMPEG_IT") != "1" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 to run the audiotag proof harness")
	}
	requireFFmpeg(t)
}

type proofCase struct {
	name    string
	file    string
	args    []string // ffmpeg encoder arguments
	format  string   // expected AudioProperties.Format
	codec   string
	rate    int
	depth   int  // expected bit depth, 0 for lossy
	vorbis  bool // tags are Vorbis comments (ffprobe joins repeats with ";")
	mp3     bool
	flacMD5 bool // has a FLAC STREAMINFO MD5 to compare
}

var proofCases = []proofCase{
	{name: "flac-16-44k", file: "a.flac", args: []string{"-ar", "44100", "-sample_fmt", "s16", "-c:a", "flac"},
		format: "flac", codec: "flac", rate: 44100, depth: 16, vorbis: true, flacMD5: true},
	{name: "flac-24-96k", file: "a.flac", args: []string{"-ar", "96000", "-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:a", "flac"},
		format: "flac", codec: "flac", rate: 96000, depth: 24, vorbis: true, flacMD5: true},
	{name: "ogg-vorbis", file: "a.ogg", args: []string{"-c:a", "libvorbis", "-q:a", "3"},
		format: "ogg", codec: "vorbis", rate: 44100, vorbis: true},
	{name: "opus", file: "a.opus", args: []string{"-c:a", "libopus", "-b:a", "64k"},
		format: "ogg", codec: "opus", rate: 48000, vorbis: true},
	{name: "mp3-cbr", file: "a.mp3", args: []string{"-c:a", "libmp3lame", "-b:a", "192k"},
		format: "mp3", codec: "mp3", rate: 44100, mp3: true},
	{name: "mp3-vbr", file: "a.mp3", args: []string{"-c:a", "libmp3lame", "-q:a", "2"},
		format: "mp3", codec: "mp3", rate: 44100, mp3: true},
	{name: "mp3-id3v23-id3v1", file: "a.mp3", args: []string{"-c:a", "libmp3lame", "-b:a", "128k", "-id3v2_version", "3", "-write_id3v1", "1"},
		format: "mp3", codec: "mp3", rate: 44100, mp3: true},
	{name: "m4a-aac", file: "a.m4a", args: []string{"-c:a", "aac", "-b:a", "128k"},
		format: "mp4", codec: "aac", rate: 44100},
	// moov before mdat: every tag-size change shifts the chunk offsets.
	{name: "m4a-aac-faststart", file: "a.m4a", args: []string{"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart"},
		format: "mp4", codec: "aac", rate: 44100},
	{name: "m4a-alac", file: "a.m4a", args: []string{"-c:a", "alac"},
		format: "mp4", codec: "alac", rate: 44100, depth: 16},
}

// supportsFile reports whether backend b handles case c (the permissive
// backend has no Ogg).
func supportsFile(b Backend, c proofCase) bool {
	return b == TagLib || c.format != "ogg"
}

func TestProofMatrix(t *testing.T) {
	requireProof(t)
	for _, b := range backends {
		for _, c := range proofCases {
			t.Run(b.String()+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				proveOne(t, b, c)
			})
		}
	}
}

func proveOne(t *testing.T, b Backend, c proofCase) {
	dir := t.TempDir()
	p := makeAudio(t, dir, c.file, c.args...)
	tg := New(b)
	if !supportsFile(b, c) {
		if err := tg.Write(p, houseFixture().Tags(), WriteOptions{}); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported format: Write returned %v, want ErrUnsupported", err)
		}
		t.Skipf("%s backend does not support %s (ErrUnsupported, as designed)", b, c.format)
	}

	pcm0, pkt0, si0 := pcmHash(t, p), audioHash(t, p), streamInfoMD5(t, p, c)
	before := probeTags(t, p)
	if before["comment"] == "" {
		t.Fatalf("setup: ffmpeg wrote no comment: %v", before)
	}
	if c.name == "mp3-id3v23-id3v1" {
		raw, _ := os.ReadFile(p)
		if raw[3] != 3 || !bytes.Equal(raw[len(raw)-128:len(raw)-125], []byte("TAG")) {
			t.Fatal("setup: expected an ID3v2.3 tag and an ID3v1 tag")
		}
	}

	// Properties.
	props, err := tg.Properties(p)
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	if props.Format != c.format || props.Codec != c.codec || props.SampleRate != c.rate || props.Channels != 2 ||
		(c.depth != 0 && props.BitDepth != c.depth) || props.Duration < 1900*time.Millisecond || props.Duration > 2100*time.Millisecond {
		t.Errorf("Properties = %+v; want format %s codec %s rate %d depth %d, 2 ch, ~2s", props, c.format, c.codec, c.rate, c.depth)
	}

	// 1. Full house set, read back by this backend and the other one.
	want := houseFixture().Tags()
	if err := tg.Write(p, want, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := tg.Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	diffTags(t, got, want)
	for _, other := range backends {
		if other != b && supportsFile(other, c) {
			o, err := New(other).Read(p)
			if err != nil {
				t.Errorf("%s read of %s-written file: %v", other, b, err)
			}
			t.Run("read-by-"+other.String(), func(t *testing.T) { diffTags(t, o, want) })
		}
	}
	checkAudioSame(t, p, c, pcm0, pkt0, si0)
	checkMP3Header(t, p, c)

	// 2. Independent reader: ffprobe sees the values and the kept tags.
	after := probeTags(t, p)
	if after["title"] != want.Get(KeyTitle) || after["album"] != want.Get(KeyAlbum) || after["artist"] != want.Get(KeyArtist) {
		t.Errorf("ffprobe title/album/artist = %q / %q / %q", after["title"], after["album"], after["artist"])
	}
	for _, k := range []string{KeyArtists, KeyGenre, KeyMBArtistID, KeyAlbumArtists} {
		pk := ffprobeKey(k, c)
		if c.vorbis {
			// ffmpeg joins repeated Vorbis comments with ";": all values visible.
			if after[pk] != strings.Join(want[k], ";") {
				t.Errorf("ffprobe %s = %q, want all of %q", pk, after[pk], want[k])
			}
		} else if !strings.HasPrefix(after[pk], want[k][0]) {
			// ffmpeg shows only the first value of an ID3/MP4 multi-value field.
			t.Errorf("ffprobe %s = %q, want it to start with %q", pk, after[pk], want[k][0])
		}
	}
	checkKept(t, before, after)

	// 3. A changed subset: Read, modify, Write.
	next := FieldsOf(got)
	next.Title = "Hoppípolla"
	next.Artists = []string{"Jónsi", "Kjartan", "Georg", "Orri"}
	next.Genres = []string{"Ambient"}
	next.Compilation = false
	next.Barcode = ""
	next.TrackNumber = 4
	next.MusicBrainzArtistIDs = next.MusicBrainzArtistIDs[:1]
	if err := tg.Write(p, next.Tags(), WriteOptions{}); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	got2, err := tg.Read(p)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	diffTags(t, got2, next.Tags())
	checkAudioSame(t, p, c, pcm0, pkt0, si0)
	checkMP3Header(t, p, c)
	checkKept(t, before, probeTags(t, p))

	// 4. Clearing removes the house tags and nothing else.
	if err := tg.Write(p, Tags{}, WriteOptions{}); err != nil {
		t.Fatalf("clearing Write: %v", err)
	}
	if got3, err := tg.Read(p); err != nil || len(got3) != 0 {
		t.Errorf("after clearing: %v, %v", got3, err)
	}
	checkAudioSame(t, p, c, pcm0, pkt0, si0)
	checkKept(t, before, probeTags(t, p))
	assertOnlyFile(t, dir, c.file)
}

// ffprobeKey is the key ffprobe reports a house key under.
func ffprobeKey(k string, c proofCase) string {
	if !c.vorbis && k == KeyMBArtistID {
		return "musicbrainz artist id" // the TXXX description / freeform name
	}
	return strings.ToLower(k)
}

// checkKept compares every non-house tag ffprobe saw before the first write.
func checkKept(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if k == "title" {
			continue // the only house tag ffmpeg wrote
		}
		if after[k] != v {
			t.Errorf("non-house tag %q: %q -> %q", k, v, after[k])
		}
	}
}

func checkAudioSame(t *testing.T, p string, c proofCase, pcm0, pkt0, si0 string) {
	t.Helper()
	if h := pcmHash(t, p); h != pcm0 {
		t.Errorf("decoded audio changed: %s -> %s", pcm0, h)
	}
	if h := audioHash(t, p); h != pkt0 {
		t.Errorf("audio packets changed: %s -> %s", pkt0, h)
	}
	if h := streamInfoMD5(t, p, c); h != si0 {
		t.Errorf("FLAC STREAMINFO MD5 changed: %s -> %s", si0, h)
	}
}

// checkMP3Header insists on exactly one tag: ID3v2.4 at the front, no ID3v1,
// Lyrics3 or APE at the end.
func checkMP3Header(t *testing.T, p string, c proofCase) {
	t.Helper()
	if !c.mp3 {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("ID3\x04")) {
		t.Errorf("MP3 does not start with an ID3v2.4 header: % x", raw[:5])
	}
	if len(raw) >= 128 && bytes.Equal(raw[len(raw)-128:len(raw)-125], []byte("TAG")) {
		t.Error("ID3v1 tag left at the end")
	}
	if bytes.Contains(raw, []byte("APETAGEX")) || bytes.Contains(raw, []byte("LYRICS200")) {
		t.Error("APE or Lyrics3 tag left in the file")
	}
}

// streamInfoMD5 returns the MD5 of the decoded audio that the FLAC encoder
// stored in STREAMINFO ("" for other formats).
func streamInfoMD5(t *testing.T, p string, c proofCase) string {
	t.Helper()
	if !c.flacMD5 {
		return ""
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// "fLaC", 4-byte block header, then STREAMINFO with the MD5 at 18..34.
	if len(raw) < 42 || string(raw[:4]) != "fLaC" || raw[4]&0x7F != 0 {
		t.Fatal("no STREAMINFO")
	}
	return fmt.Sprintf("%x", raw[8+18:8+34])
}

// Eight writes at once on different files, per backend, under -race.
func TestProofParallelWrites(t *testing.T) {
	requireProof(t)
	for _, b := range backends {
		t.Run(b.String(), func(t *testing.T) {
			dir := t.TempDir()
			var cases []proofCase
			for _, c := range proofCases {
				if supportsFile(b, c) {
					cases = append(cases, c)
				}
			}
			paths := make([]string, 8)
			for i := range paths {
				c := cases[i%len(cases)]
				paths[i] = makeAudio(t, dir, fmt.Sprintf("p%d%s", i, filepath.Ext(c.file)), c.args...)
			}
			tg := New(b)
			var wg sync.WaitGroup
			errs := make([]error, len(paths))
			for i, p := range paths {
				wg.Add(1)
				go func() {
					defer wg.Done()
					f := houseFixture()
					f.Title = fmt.Sprintf("parallel %d", i)
					errs[i] = tg.Write(p, f.Tags(), WriteOptions{})
				}()
			}
			wg.Wait()
			for i, p := range paths {
				if errs[i] != nil {
					t.Errorf("%s: %v", filepath.Base(p), errs[i])
					continue
				}
				got, err := tg.Read(p)
				if err != nil || got.Get(KeyTitle) != fmt.Sprintf("parallel %d", i) {
					t.Errorf("%s: title %q, %v", filepath.Base(p), got.Get(KeyTitle), err)
				}
			}
		})
	}
}

// BenchmarkProof times Read and Write per backend and format:
// ARRMADA_FFMPEG_IT=1 go test -run '^$' -bench Proof ./internal/audiotag/
func BenchmarkProof(b *testing.B) {
	requireProof(b)
	for _, be := range backends {
		for _, c := range []proofCase{proofCases[0], proofCases[4], proofCases[7]} {
			p := makeAudio(b, b.TempDir(), c.file, c.args...)
			tg := New(be)
			tags := houseFixture().Tags()
			b.Run(be.String()+"/"+c.name+"/write", func(b *testing.B) {
				for b.Loop() {
					if err := tg.Write(p, tags, WriteOptions{}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(be.String()+"/"+c.name+"/read", func(b *testing.B) {
				for b.Loop() {
					if _, err := tg.Read(p); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
