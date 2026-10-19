package audiotag

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogem/id3v2/v2"
)

// id3LegacyDateFrames are the ID3v2.3 date frames that TDRC/TDOR replace in v2.4.
// They are read as a fallback and dropped on write, since the file becomes v2.4.
var id3LegacyDateFrames = []string{"TYER", "TDAT", "TIME", "TORY", "TRDA"}

// id3Sep separates multiple values inside one ID3v2.4 text or TXXX frame.
const id3Sep = "\x00"

func readMP3(f *os.File) (map[string][]string, error) {
	tag, _, err := parseID3(f)
	if err != nil {
		return nil, err
	}
	m := map[string][]string{}
	txxx := tag.GetFrames("TXXX")
	for _, fd := range id3Fields {
		var raw string
		if fd.frame == "TXXX" {
			for _, fr := range txxx {
				if u, ok := fr.(id3v2.UserDefinedTextFrame); ok && strings.EqualFold(u.Description, fd.desc) {
					raw = u.Value
					break
				}
			}
		} else {
			raw = tag.GetTextFrame(fd.frame).Text
		}
		if vals := splitID3(raw); len(vals) > 0 {
			m[fd.key] = vals
		}
	}
	// "n/total"; normalizeRead splits it.
	m[KeyTrackNumber] = splitID3(tag.GetTextFrame("TRCK").Text)
	m[KeyDiscNumber] = splitID3(tag.GetTextFrame("TPOS").Text)
	for _, fr := range tag.GetFrames("UFID") {
		if u, ok := fr.(id3v2.UFIDFrame); ok && u.OwnerIdentifier == mbUFIDOwner {
			m[KeyMBRecordingID] = []string{string(u.Identifier)}
		}
	}
	// ID3v2.3 files carry the year in TYER/TORY instead of TDRC/TDOR.
	if len(m[KeyDate]) == 0 {
		m[KeyDate] = splitID3(tag.GetTextFrame("TYER").Text)
	}
	if len(m[KeyOriginalDate]) == 0 {
		m[KeyOriginalDate] = splitID3(tag.GetTextFrame("TORY").Text)
	}
	return m, nil
}

// writeMP3 rebuilds the ID3v2 tag as v2.4 and copies the MPEG audio after it
// unchanged. Trailing ID3v1, Lyrics3 and APE tags are dropped: the ID3v2.4 tag
// is the only tag the file keeps, so stale copies can't contradict it.
func writeMP3(src *os.File, dst io.Writer, t Tags, opts WriteOptions) error {
	tag, tagEnd, err := parseID3(src)
	if err != nil {
		return err
	}

	// Drop every frame the house set owns, keeping foreign TXXX/UFID frames.
	for _, fd := range id3Fields {
		if fd.frame != "TXXX" {
			tag.DeleteFrames(fd.frame)
		}
	}
	for _, id := range append([]string{"TRCK", "TPOS"}, id3LegacyDateFrames...) {
		tag.DeleteFrames(id)
	}
	// Copy before deleting: DeleteFrames hands the sequence back to a pool.
	oldTXXX := append([]id3v2.Framer(nil), tag.GetFrames("TXXX")...)
	tag.DeleteFrames("TXXX")
	for _, fr := range oldTXXX {
		if u, ok := fr.(id3v2.UserDefinedTextFrame); ok && !isID3HouseTXXX(u.Description) {
			tag.AddUserDefinedTextFrame(u)
		}
	}
	oldUFID := append([]id3v2.Framer(nil), tag.GetFrames("UFID")...)
	tag.DeleteFrames("UFID")
	for _, fr := range oldUFID {
		if u, ok := fr.(id3v2.UFIDFrame); ok && u.OwnerIdentifier != mbUFIDOwner {
			tag.AddUFIDFrame(u)
		}
	}

	tag.SetVersion(4)
	for _, fd := range id3Fields {
		vals := t[fd.key]
		if len(vals) == 0 {
			continue
		}
		text := strings.Join(vals, id3Sep)
		if fd.frame == "TXXX" {
			tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{
				Encoding: id3v2.EncodingUTF8, Description: fd.desc, Value: text,
			})
		} else {
			tag.AddTextFrame(fd.frame, id3v2.EncodingUTF8, text)
		}
	}
	if v := joinPair(t, KeyTrackNumber, KeyTrackTotal); v != "" {
		tag.AddTextFrame("TRCK", id3v2.EncodingUTF8, v)
	}
	if v := joinPair(t, KeyDiscNumber, KeyDiscTotal); v != "" {
		tag.AddTextFrame("TPOS", id3v2.EncodingUTF8, v)
	}
	if id := t.first(KeyMBRecordingID); id != "" {
		tag.AddUFIDFrame(id3v2.UFIDFrame{OwnerIdentifier: mbUFIDOwner, Identifier: []byte(id)})
	}
	if opts.FrontCover != nil {
		if err := setMP3Cover(tag, opts.FrontCover); err != nil {
			return err
		}
	}

	audioEnd, err := mp3TrailerStart(src)
	if err != nil {
		return err
	}
	if audioEnd < tagEnd {
		return fmt.Errorf("%w: MP3 trailing tags overlap the ID3v2 tag", ErrUnsupported)
	}
	if _, err := tag.WriteTo(dst); err != nil {
		return err
	}
	return copyRange(dst, src, tagEnd, audioEnd-tagEnd)
}

func isID3HouseTXXX(desc string) bool {
	for _, fd := range id3Fields {
		if fd.frame == "TXXX" && strings.EqualFold(fd.desc, desc) {
			return true
		}
	}
	return false
}

func splitID3(s string) []string {
	var out []string
	for _, v := range strings.Split(s, id3Sep) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseID3 parses the ID3v2 tag at the start of f and returns it with the byte
// offset where the audio begins (0 when there is no tag).
//
// bogem/id3v2 ignores tag- and frame-level flags. Rather than let it misread or
// silently corrupt such a tag on rewrite, the raw header and frame flags are
// checked first and anything it can't handle is refused with ErrUnsupported:
// ID3v2.2, unsynchronisation, extended headers, footers, and compressed,
// encrypted, grouped or length-prefixed frames. These are rare in practice.
func parseID3(f *os.File) (*id3v2.Tag, int64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(f, hdr); err != nil || string(hdr[:3]) != "ID3" {
		// No ID3v2 tag: start an empty v2.4 one. Audio begins at byte 0.
		return id3v2.NewEmptyTag(), 0, nil
	}
	ver, flags := hdr[3], hdr[5]
	if ver != 3 && ver != 4 {
		return nil, 0, fmt.Errorf("%w: ID3v2.%d", ErrUnsupported, ver)
	}
	if flags&0xC0 != 0 || (ver == 4 && flags&0x10 != 0) {
		return nil, 0, fmt.Errorf("%w: ID3v2 tag uses unsynchronisation, an extended header or a footer", ErrUnsupported)
	}
	size := int64(synchsafe(hdr[6:10]))
	body := make([]byte, size)
	if _, err := io.ReadFull(f, body); err != nil {
		return nil, 0, fmt.Errorf("id3: truncated tag: %w", err)
	}
	if err := checkID3Frames(body, ver); err != nil {
		return nil, 0, err
	}

	tagEnd := 10 + size
	tag, err := id3v2.ParseReader(io.NewSectionReader(f, 0, tagEnd), id3v2.Options{Parse: true})
	if err != nil {
		return nil, 0, fmt.Errorf("id3: %w", err)
	}
	return tag, tagEnd, nil
}

// checkID3Frames walks the frame headers looking for flags bogem/id3v2 would
// mishandle, and for frames that overrun the tag.
func checkID3Frames(body []byte, ver byte) error {
	// Format-flag bits: v2.4 grouping/compression/encryption/unsync/data length
	// (0x4F); v2.3 compression/encryption/grouping (0xE0).
	badFlags := byte(0xE0)
	if ver == 4 {
		badFlags = 0x4F
	}
	for pos := 0; pos+10 <= len(body); {
		if body[pos] == 0 {
			return nil // padding
		}
		var n int
		if ver == 4 {
			n = int(synchsafe(body[pos+4 : pos+8]))
		} else {
			n = int(binary.BigEndian.Uint32(body[pos+4 : pos+8]))
		}
		if body[pos+9]&badFlags != 0 {
			return fmt.Errorf("%w: ID3 frame %q is compressed, encrypted or unsynchronised", ErrUnsupported, body[pos:pos+4])
		}
		if n < 0 || pos+10+n > len(body) {
			return fmt.Errorf("%w: ID3 frame %q overruns the tag", ErrUnsupported, body[pos:pos+4])
		}
		pos += 10 + n
	}
	return nil
}

func synchsafe(b []byte) uint32 {
	return uint32(b[0]&0x7F)<<21 | uint32(b[1]&0x7F)<<14 | uint32(b[2]&0x7F)<<7 | uint32(b[3]&0x7F)
}

// mp3TrailerStart returns the offset where trailing tags (ID3v1, Lyrics3v2,
// APEv2, in any stacking order) begin, i.e. the end of the audio.
func mp3TrailerStart(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	end := fi.Size()
	read := func(off int64, n int) []byte {
		if off < 0 {
			return nil
		}
		b := make([]byte, n)
		if _, err := f.ReadAt(b, off); err != nil {
			return nil
		}
		return b
	}
	for {
		switch {
		case string(read(end-128, 3)) == "TAG":
			end -= 128
		case string(read(end-9, 9)) == "LYRICS200":
			// A Lyrics3v2 tag: its size (6 digits) counts from "LYRICSBEGIN" to just before
			// the size field. Believed only when "LYRICSBEGIN" is really there: a size we
			// trusted blindly would have the writer cut audio off the file.
			sz := read(end-15, 6)
			n := parseLeadingInt(string(sz))
			start := end - 15 - int64(n)
			if len(sz) != 6 || n < 11 || start < 0 || string(read(start, 11)) != "LYRICSBEGIN" {
				return 0, fmt.Errorf("%w: a Lyrics3 tag that doesn't hold together", ErrUnsupported)
			}
			end = start
		case string(read(end-32, 8)) == "APETAGEX":
			start, err := apeTagStart(read, end)
			if err != nil {
				return 0, err
			}
			end = start
		default:
			return end, nil
		}
	}
}

// apeTagStart checks the APEv2 tag whose footer ends at end and returns where the tag
// begins. Its size is believed only when the tag holds together: with a header, the header
// is at the start the size gives; without one, the items the footer counts run exactly up
// to the footer. Anything else (a truncated or corrupt tag, a stray "APETAGEX" in the
// audio) is ErrUnsupported: the file is left as it is rather than cut where a bad size says.
func apeTagStart(read func(off int64, n int) []byte, end int64) (int64, error) {
	bad := func(why string) (int64, error) {
		return 0, fmt.Errorf("%w: an APE tag that doesn't hold together (%s)", ErrUnsupported, why)
	}
	footer := read(end-32, 32)
	if len(footer) != 32 {
		return bad("no footer")
	}
	size := int64(binary.LittleEndian.Uint32(footer[12:16])) // items + footer
	count := binary.LittleEndian.Uint32(footer[16:20])
	hasHeader := binary.LittleEndian.Uint32(footer[20:24])&(1<<31) != 0
	if size < 32 || size > end {
		return bad("size")
	}
	itemsStart := end - size
	start := itemsStart
	if hasHeader {
		start -= 32
		if start < 0 || string(read(start, 8)) != "APETAGEX" {
			return bad("no header where the size points")
		}
	}
	// Walk the items: each is a 4-byte value size, 4-byte flags, a NUL-ended key and the
	// value; together they must end exactly at the footer.
	pos, itemsEnd := itemsStart, end-32
	for i := uint32(0); i < count; i++ {
		hdr := read(pos, 8)
		if len(hdr) != 8 {
			return bad("item header")
		}
		vlen := int64(binary.LittleEndian.Uint32(hdr[0:4]))
		pos += 8
		keyEnd := int64(-1)
		for k := pos; k < itemsEnd && k < pos+256; k++ {
			if b := read(k, 1); len(b) == 1 && b[0] == 0 {
				keyEnd = k
				break
			}
		}
		if keyEnd < 0 {
			return bad("item key")
		}
		pos = keyEnd + 1 + vlen
		if pos > itemsEnd {
			return bad("item size")
		}
	}
	if pos != itemsEnd {
		return bad("items don't end at the footer")
	}
	return start, nil
}

// stripMP3Trailers truncates trailing ID3v1/Lyrics3/APE tags from f.
func stripMP3Trailers(f *os.File) error {
	end, err := mp3TrailerStart(f)
	if err != nil {
		return err
	}
	return f.Truncate(end)
}
