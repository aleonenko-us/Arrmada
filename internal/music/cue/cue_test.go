package cue

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	xunicode "golang.org/x/text/encoding/unicode"
)

// A typical EAC single-file rip: track 1's two-second pregap (INDEX 00 at 0), a pregap on
// track 3, a data track at the end (an enhanced CD).
const eacSheet = `REM GENRE "Alternative Rock"
REM DATE 1997
REM DISCID 9A0B7C0C
REM COMMENT "ExactAudioCopy v1.6"
CATALOG 0724385522925
PERFORMER "Radiohead"
TITLE "OK Computer"
FILE "Radiohead - OK Computer.wav" WAVE
  TRACK 01 AUDIO
    TITLE "Airbag"
    PERFORMER "Radiohead"
    ISRC GBAYE9700131
    INDEX 00 00:00:00
    INDEX 01 00:00:32
  TRACK 02 AUDIO
    TITLE "Paranoid Android"
    INDEX 01 04:44:10
  TRACK 03 AUDIO
    TITLE "Subterranean Homesick Alien"
    FLAGS DCP
    INDEX 00 11:07:20
    INDEX 01 11:09:20
    INDEX 02 12:00:00
  TRACK 04 MODE1/2352
    INDEX 01 15:40:00
`

func TestParseEAC(t *testing.T) {
	s, err := Parse([]byte(eacSheet))
	if err != nil {
		t.Fatal(err)
	}
	if s.Performer != "Radiohead" || s.Title != "OK Computer" || s.Date != "1997" || s.Genre != "Alternative Rock" ||
		s.DiscID != "9A0B7C0C" || s.Catalog != "0724385522925" || s.Encoding != "utf-8" {
		t.Errorf("header: %+v", s)
	}
	if len(s.Files) != 1 || s.Files[0].Name != "Radiohead - OK Computer.wav" || s.Files[0].Type != "WAVE" {
		t.Fatalf("files: %+v", s.Files)
	}
	if len(s.Tracks) != 4 {
		t.Fatalf("tracks: %d", len(s.Tracks))
	}
	one := s.Tracks[0]
	if one.Title != "Airbag" || one.Performer != "Radiohead" || one.ISRC != "GBAYE9700131" ||
		one.Index00 != (Pos{0, 0, true}) || one.Index01 != (Pos{0, 32, true}) {
		t.Errorf("track 1: %+v", one)
	}
	if s.Tracks[1].Index00.Set {
		t.Errorf("track 2 has no INDEX 00: %+v", s.Tracks[1])
	}
	three := s.Tracks[2]
	if three.Index00.Frames != (11*60+7)*75+20 || three.Index01.Frames != (11*60+9)*75+20 {
		t.Errorf("track 3 indexes: %+v", three)
	}
	if s.Tracks[3].Audio() || !s.Tracks[2].Audio() {
		t.Errorf("data track: %+v", s.Tracks[3])
	}
	if !s.IsImage(0) || !s.HoldsAudio(0) {
		t.Error("the one file is an image")
	}
	// Segments: the lead-in (track 1's pregap), then each track to the next INDEX 01 —
	// track 3's pregap (INDEX 00 at 11:07:20) stays at the end of track 2.
	want := []Segment{
		{SegLeadIn, -1, 0, 32},
		{SegTrack, 0, 32, (4*60+44)*75 + 10},
		{SegTrack, 1, (4*60+44)*75 + 10, (11*60+9)*75 + 20},
		{SegTrack, 2, (11*60+9)*75 + 20, (15*60 + 40) * 75},
		{SegData, 3, (15*60 + 40) * 75, -1},
	}
	if got := s.Segments(0); !reflect.DeepEqual(got, want) {
		t.Errorf("segments:\n got %+v\nwant %+v", got, want)
	}
}

// Two discs in one sheet, one image each; the second disc's numbering restarts.
const twoDiscSheet = `PERFORMER "Pink Floyd"
TITLE "The Wall"
FILE "CD1.flac" WAVE
  TRACK 01 AUDIO
    TITLE "In the Flesh?"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "The Thin Ice"
    INDEX 01 03:19:50
FILE "CD2.flac" WAVE
  TRACK 01 AUDIO
    TITLE "Hey You"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Is There Anybody Out There?"
    INDEX 00 04:38:00
    INDEX 01 04:40:00
`

func TestParseMultiFileImages(t *testing.T) {
	s, err := Parse([]byte(twoDiscSheet))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 2 || !s.IsImage(0) || !s.IsImage(1) {
		t.Fatalf("files: %+v", s.Files)
	}
	if got := s.TracksIn(1); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Errorf("tracks in CD2: %v", got)
	}
	segs := s.Segments(1)
	if len(segs) != 2 || segs[0].Start != 0 || segs[0].End != (4*60+40)*75 || segs[1].End != -1 {
		t.Errorf("CD2 segments: %+v", segs)
	}
}

// EAC's "multiple files with gaps (noncompliant)": one file per track, each pregap at the
// end of the file before (INDEX 00 of the next track in the previous FILE). Not an image.
const perTrackSheet = `TITLE "Album"
FILE "01 - One.wav" WAVE
  TRACK 01 AUDIO
    TITLE "One"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Two"
    INDEX 00 03:58:12
FILE "02 - Two.wav" WAVE
    INDEX 01 00:00:00
  TRACK 03 AUDIO
    TITLE "Three"
FILE "03 - Three.wav" WAVE
    INDEX 01 00:00:00
`

func TestParsePerTrackSheet(t *testing.T) {
	s, err := Parse([]byte(perTrackSheet))
	if err != nil {
		t.Fatal(err)
	}
	for f := range s.Files {
		if s.IsImage(f) {
			t.Errorf("file %d read as an image", f)
		}
	}
	two := s.Tracks[1]
	if two.Index00 != (Pos{0, (3*60+58)*75 + 12, true}) || two.Index01 != (Pos{1, 0, true}) {
		t.Errorf("track 2's pregap is in the previous file: %+v", two)
	}
}

func TestParseErrors(t *testing.T) {
	for name, sheet := range map[string]string{
		"empty":          "",
		"no file":        "TRACK 01 AUDIO\n INDEX 01 00:00:00\n",
		"no index 01":    "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 00 00:00:00\n",
		"bad frames":     "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:75\n",
		"bad time":       "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 1:2\n",
		"backwards":      "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 01:00:00\n TRACK 02 AUDIO\n INDEX 01 00:30:00\n",
		"pregap after":   "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n INDEX 00 02:00:00\n INDEX 01 01:00:00\n",
		"a binary blob":  "\x00\x01\x02 not a cue",
		"track no index": "FILE \"a.wav\" WAVE\n TRACK 01 AUDIO\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n",
	} {
		if _, err := Parse([]byte(sheet)); !errors.Is(err, ErrNotCue) {
			t.Errorf("%s: err = %v, want ErrNotCue", name, err)
		}
	}
}

// Every encoding rippers write comes out as the same text.
func TestDecodeEncodings(t *testing.T) {
	sheet := func(performer, title string) string {
		return "PERFORMER \"" + performer + "\"\r\nTITLE \"" + title + "\"\r\nFILE \"x.flac\" WAVE\r\n  TRACK 01 AUDIO\r\n    TITLE \"" + title +
			"\"\r\n    INDEX 01 00:00:00\r\n  TRACK 02 AUDIO\r\n    INDEX 01 01:00:00\r\n"
	}
	latin := sheet("Björk", "Café Señor – Ñ")
	jp := sheet("椎名林檎", "無罪モラトリアム")
	cp1252, err := charmap.Windows1252.NewEncoder().String(latin)
	if err != nil {
		t.Fatal(err)
	}
	sjis, err := japanese.ShiftJIS.NewEncoder().String(jp)
	if err != nil {
		t.Fatal(err)
	}
	u16le, _ := xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM).NewEncoder().String(latin)
	u16be, _ := xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM).NewEncoder().String(jp)
	for _, c := range []struct {
		name, raw, enc, performer, title string
	}{
		{"utf-8", latin, "utf-8", "Björk", "Café Señor – Ñ"},
		{"utf-8 with BOM", "\xEF\xBB\xBF" + jp, "utf-8", "椎名林檎", "無罪モラトリアム"},
		{"utf-16le with BOM", u16le, "utf-16", "Björk", "Café Señor – Ñ"},
		{"utf-16be with BOM", u16be, "utf-16", "椎名林檎", "無罪モラトリアム"},
		{"windows-1252", cp1252, "windows-1252", "Björk", "Café Señor – Ñ"},
		{"shift-jis", sjis, "shift-jis", "椎名林檎", "無罪モラトリアム"},
	} {
		s, err := Parse([]byte(c.raw))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if s.Encoding != c.enc || s.Performer != c.performer || s.Title != c.title || s.Tracks[0].Title != c.title {
			t.Errorf("%s: read as %s: %q / %q", c.name, s.Encoding, s.Performer, s.Title)
		}
	}
}

// Latin names whose Windows-1252 bytes happen to be valid Shift-JIS stay Latin.
func TestDecodeLatinNotJapanese(t *testing.T) {
	for _, name := range []string{"Pokémon", "Émile Zola", "Sigur Rós Ágætis byrjun", "Mötley Crüe", "Straße"} {
		raw, err := charmap.Windows1252.NewEncoder().String("TITLE \"" + name + "\"")
		if err != nil {
			t.Fatal(err)
		}
		got, enc := Decode([]byte(raw))
		if enc != "windows-1252" || !strings.Contains(got, name) {
			t.Errorf("%q read as %s: %q", name, enc, got)
		}
	}
}

func TestTimes(t *testing.T) {
	for _, c := range []struct {
		in     string
		frames int64
	}{{"00:00:00", 0}, {"00:02:00", 150}, {"04:44:10", (4*60+44)*75 + 10}, {"123:59:74", (123*60+59)*75 + 74}} {
		got, err := ParseTime(c.in)
		if err != nil || got != c.frames {
			t.Errorf("%s = %d, %v", c.in, got, err)
		}
		if back := FormatTime(got); back != c.in {
			t.Errorf("FormatTime(%d) = %s", got, back)
		}
	}
	// 588 samples a frame at 44.1 kHz, 640 at 48 kHz: exact.
	if Samples(75, 44100) != 44100 || Samples(1, 44100) != 588 || Samples(1, 48000) != 640 || Samples(3, 96000) != 3840 {
		t.Error("Samples")
	}
}

func TestQuotesAndOddSpacing(t *testing.T) {
	got := splitFields(`  TITLE   "Two  spaces kept"  `)
	if !reflect.DeepEqual(got, []string{"TITLE", "Two  spaces kept"}) {
		t.Errorf("quoted: %q", got)
	}
	got = splitFields(`FILE "unterminated name.flac WAVE`)
	if !reflect.DeepEqual(got, []string{"FILE", "unterminated name.flac WAVE"}) {
		t.Errorf("unterminated: %q", got)
	}
	got = splitFields("PERFORMER Unquoted Name\r")
	if !reflect.DeepEqual(got, []string{"PERFORMER", "Unquoted", "Name"}) {
		t.Errorf("unquoted: %q", got)
	}
}
