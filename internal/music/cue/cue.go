// Package cue reads CD cue sheets (MUS-35): the text file a ripper writes beside a
// whole-disc audio image to say where each track starts. Music imports split such an
// image into one FLAC per track, because Plex (and most players) never read cue sheets.
//
// Only what splitting and matching need is kept: the FILE each track's audio is in, the
// TRACK numbers and types, INDEX 00 (the pregap) and INDEX 01 (the track's start), and
// the TITLE, PERFORMER, ISRC, CATALOG and REM lines rippers fill in.
//
// Cue sheets are written in whatever encoding the ripper's machine used. Decode reads
// UTF-8 (with or without a byte-order mark), UTF-16 with a byte-order mark, and falls
// back to Shift-JIS (common for Japanese rips) when the text plainly is that, else to
// Windows-1252.
package cue

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	xunicode "golang.org/x/text/encoding/unicode"
)

// FramesPerSecond is how many CD frames (sectors) a second holds: INDEX times are
// minutes:seconds:frames.
const FramesPerSecond = 75

// MaxSize is the largest file read as a cue sheet. A real one is a few kilobytes; a big
// file named .cue is something else.
const MaxSize = 1 << 20

// Sheet is a parsed cue sheet.
type Sheet struct {
	Performer string
	Title     string
	Catalog   string // UPC/EAN
	// From REM lines.
	Date       string
	Genre      string
	DiscID     string
	DiscNumber int
	TotalDiscs int
	Files      []File
	Tracks     []Track // in the order the sheet lists them
	// Encoding is what the text was read as: "utf-8", "utf-16", "shift-jis" or
	// "windows-1252".
	Encoding string
}

// File is one FILE line: the audio (or data) file the following INDEX lines point into.
type File struct {
	Name string
	Type string // WAVE, FLAC, MP3, AIFF, BINARY, MOTOROLA… as written, upper case
}

// Pos is a place in one of the sheet's files.
type Pos struct {
	File   int   // index into Sheet.Files
	Frames int64 // from the start of that file
	Set    bool
}

// Track is one TRACK of the sheet.
type Track struct {
	Number    int
	Type      string // AUDIO, MODE1/2352, …, upper case
	Title     string
	Performer string
	ISRC      string
	// Index00 is where the track's pregap starts (unset when the sheet has none). It can
	// be in the file before Index01's (a multi-file rip with the gap appended to the
	// previous file).
	Index00 Pos
	// Index01 is where the track itself starts.
	Index01 Pos
}

// Audio reports whether the track is an audio track (not a data track of an enhanced
// or mixed-mode CD).
func (t Track) Audio() bool { return t.Type == "AUDIO" }

// ErrNotCue is a file that isn't a usable cue sheet.
var ErrNotCue = errors.New("not a cue sheet")

// Parse reads a cue sheet from its raw bytes.
func Parse(raw []byte) (*Sheet, error) {
	if len(raw) > MaxSize {
		return nil, fmt.Errorf("%w: %d bytes is too big", ErrNotCue, len(raw))
	}
	text, enc := Decode(raw)
	s := &Sheet{Encoding: enc}
	cur := -1 // the track being read (-1: the sheet's header)
	file := -1
	for n, line := range strings.Split(text, "\n") {
		fields := splitFields(line)
		if len(fields) == 0 {
			continue
		}
		cmd := strings.ToUpper(fields[0])
		arg := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}
			return ""
		}
		bad := func(why string) error {
			return fmt.Errorf("%w: line %d: %s", ErrNotCue, n+1, why)
		}
		switch cmd {
		case "FILE":
			if len(fields) < 2 {
				return nil, bad("FILE without a name")
			}
			name, typ := fields[1], ""
			if len(fields) > 2 {
				typ = strings.ToUpper(fields[len(fields)-1])
			}
			if len(fields) > 3 {
				// An unquoted name with spaces in it.
				name = strings.Join(fields[1:len(fields)-1], " ")
			}
			s.Files = append(s.Files, File{Name: name, Type: typ})
			file = len(s.Files) - 1
		case "TRACK":
			if file < 0 {
				return nil, bad("TRACK before any FILE")
			}
			num, err := strconv.Atoi(arg(1))
			if err != nil || num < 0 || num > 99 {
				return nil, bad("TRACK number " + strconv.Quote(arg(1)))
			}
			s.Tracks = append(s.Tracks, Track{Number: num, Type: strings.ToUpper(arg(2))})
			cur = len(s.Tracks) - 1
		case "INDEX":
			if cur < 0 {
				return nil, bad("INDEX before any TRACK")
			}
			idx, err := strconv.Atoi(arg(1))
			if err != nil {
				return nil, bad("INDEX number " + strconv.Quote(arg(1)))
			}
			fr, err := ParseTime(arg(2))
			if err != nil {
				return nil, bad(err.Error())
			}
			p := Pos{File: file, Frames: fr, Set: true}
			switch idx {
			case 0:
				s.Tracks[cur].Index00 = p
			case 1:
				s.Tracks[cur].Index01 = p
			}
			// INDEX 02 and up mark places inside a track; nothing splits there.
		case "TITLE", "PERFORMER":
			// Quoted, or (some rippers) bare words.
			v := strings.TrimSpace(strings.Join(fields[1:], " "))
			switch {
			case cur < 0 && cmd == "TITLE":
				s.Title = v
			case cur < 0:
				s.Performer = v
			case cmd == "TITLE":
				s.Tracks[cur].Title = v
			default:
				s.Tracks[cur].Performer = v
			}
		case "ISRC":
			if cur >= 0 {
				s.Tracks[cur].ISRC = strings.TrimSpace(arg(1))
			}
		case "CATALOG":
			s.Catalog = strings.TrimSpace(arg(1))
		case "REM":
			if cur >= 0 {
				continue // per-track REMs (replay gain, comments) aren't used
			}
			v := strings.TrimSpace(strings.Join(fields[min(2, len(fields)):], " "))
			switch strings.ToUpper(arg(1)) {
			case "DATE":
				s.Date = v
			case "GENRE":
				s.Genre = v
			case "DISCID":
				s.DiscID = v
			case "DISCNUMBER":
				s.DiscNumber, _ = strconv.Atoi(v)
			case "TOTALDISCS":
				s.TotalDiscs, _ = strconv.Atoi(v)
			}
		}
		// FLAGS, PREGAP/POSTGAP (silence that isn't in the file), SONGWRITER, CDTEXTFILE
		// and anything unknown: nothing to split or match on.
	}
	if len(s.Files) == 0 || len(s.Tracks) == 0 {
		return nil, fmt.Errorf("%w: no FILE or TRACK", ErrNotCue)
	}
	for i, t := range s.Tracks {
		if !t.Index01.Set {
			return nil, fmt.Errorf("%w: track %d has no INDEX 01", ErrNotCue, t.Number)
		}
		if t.Index00.Set && (t.Index00.File > t.Index01.File ||
			(t.Index00.File == t.Index01.File && t.Index00.Frames > t.Index01.Frames)) {
			return nil, fmt.Errorf("%w: track %d's INDEX 00 is after its INDEX 01", ErrNotCue, t.Number)
		}
		if i > 0 {
			prev := s.Tracks[i-1].Index01
			if t.Index01.File < prev.File || (t.Index01.File == prev.File && t.Index01.Frames <= prev.Frames) {
				return nil, fmt.Errorf("%w: track %d starts before the track ahead of it", ErrNotCue, t.Number)
			}
		}
	}
	return s, nil
}

// ParseTime reads an INDEX time, mm:ss:ff, as CD frames. Minutes may run past 99.
func ParseTime(v string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("time %q isn't mm:ss:ff", v)
	}
	var n [3]int64
	for i, p := range parts {
		x, err := strconv.ParseInt(p, 10, 64)
		if err != nil || x < 0 {
			return 0, fmt.Errorf("time %q isn't mm:ss:ff", v)
		}
		n[i] = x
	}
	if n[1] > 59 || n[2] >= FramesPerSecond {
		return 0, fmt.Errorf("time %q is out of range", v)
	}
	return (n[0]*60+n[1])*FramesPerSecond + n[2], nil
}

// FormatTime writes frames as mm:ss:ff.
func FormatTime(frames int64) string {
	f := frames % FramesPerSecond
	s := frames / FramesPerSecond
	return fmt.Sprintf("%02d:%02d:%02d", s/60, s%60, f)
}

// splitFields splits a line into its words, a "quoted string" counting as one word
// (without its quotes). An unterminated quote runs to the end of the line.
func splitFields(line string) []string {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	var out []string
	for line != "" {
		if line[0] == '"' {
			end := strings.IndexByte(line[1:], '"')
			if end < 0 {
				out = append(out, line[1:])
				break
			}
			out = append(out, line[1:1+end])
			line = strings.TrimLeftFunc(line[2+end:], unicode.IsSpace)
			continue
		}
		end := strings.IndexFunc(line, unicode.IsSpace)
		if end < 0 {
			out = append(out, line)
			break
		}
		out = append(out, line[:end])
		line = strings.TrimLeftFunc(line[end:], unicode.IsSpace)
	}
	return out
}

// Decode turns a cue sheet's bytes into text and says which encoding it was read as.
func Decode(raw []byte) (string, string) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return string(bytes.ToValidUTF8(raw[3:], []byte("�"))), "utf-8"
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		dec := xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM).NewDecoder()
		if out, err := dec.Bytes(raw); err == nil {
			return string(out), "utf-16"
		}
	}
	if utf8.Valid(raw) {
		return string(raw), "utf-8"
	}
	if s, ok := shiftJIS(raw); ok {
		return s, "shift-jis"
	}
	out, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		return string(bytes.ToValidUTF8(raw, []byte("�"))), "utf-8"
	}
	return string(out), "windows-1252"
}

// shiftJIS reads raw as Shift-JIS when that is plainly what it is: it decodes without a
// single invalid sequence and yields Japanese text. Windows-1252 accented letters are
// often valid Shift-JIS lead bytes too, so a Latin name can "decode": "Pokémon" comes out
// as one kanji between Latin letters, "Émile" as a half-width katakana. Real Japanese text
// has kana, or kanji next to other kanji; a lone kanji wedged between Latin letters, or
// half-width katakana with nothing else Japanese, reads as Windows-1252 instead.
func shiftJIS(raw []byte) (string, bool) {
	out, err := decodeStrict(japanese.ShiftJIS, raw)
	if err != nil {
		return "", false
	}
	rs := []rune(out)
	latin := func(i int) bool {
		return i >= 0 && i < len(rs) && rs[i] < utf8.RuneSelf && unicode.IsLetter(rs[i])
	}
	kana, han, wedged, half := 0, 0, 0, 0
	for i, r := range rs {
		switch {
		case r >= 0xFF61 && r <= 0xFF9F:
			half++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
			if latin(i-1) || latin(i+1) {
				wedged++
			}
		}
	}
	if kana > 0 {
		return out, half <= kana+han
	}
	return out, han >= 2 && han > wedged && half == 0
}

func decodeStrict(enc encoding.Encoding, raw []byte) (string, error) {
	out, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return "", err
	}
	if bytes.Contains(out, []byte("�")) {
		return "", errors.New("invalid sequence")
	}
	return string(out), nil
}
