package audiotag

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bogem/id3v2/v2"
)

// id3Field maps a canonical key to an ID3v2.4 frame: a plain text frame (desc
// empty) or a TXXX frame with that description. Names follow Picard.
type id3Field struct {
	key   string
	frame string
	desc  string
}

var id3Fields = []id3Field{
	{keyTitle, "TIT2", ""},
	{keyArtist, "TPE1", ""},
	{keyArtists, "TXXX", "ARTISTS"},
	{keyAlbum, "TALB", ""},
	{keyAlbumArtist, "TPE2", ""},
	{keyAlbumArtists, "TXXX", "ALBUMARTISTS"},
	{keyDate, "TDRC", ""},
	{keyOriginalDate, "TDOR", ""},
	{keyGenre, "TCON", ""},
	{keyCompilation, "TCMP", ""},
	{keyMBReleaseTrackID, "TXXX", "MusicBrainz Release Track Id"},
	{keyMBReleaseID, "TXXX", "MusicBrainz Album Id"},
	{keyMBReleaseGroupID, "TXXX", "MusicBrainz Release Group Id"},
	{keyMBArtistID, "TXXX", "MusicBrainz Artist Id"},
	{keyMBAlbumArtistID, "TXXX", "MusicBrainz Album Artist Id"},
	{keyReleaseType, "TXXX", "MusicBrainz Album Type"},
	{keyReleaseStatus, "TXXX", "MusicBrainz Album Status"},
	{keyReleaseCountry, "TXXX", "MusicBrainz Album Release Country"},
	{keyLabel, "TPUB", ""},
	{keyCatalogNumber, "TXXX", "CATALOGNUMBER"},
	{keyBarcode, "TXXX", "BARCODE"},
	{keyISRC, "TSRC", ""},
	{keyMedia, "TMED", ""},
	{keyRGTrackGain, "TXXX", "REPLAYGAIN_TRACK_GAIN"},
	{keyRGTrackPeak, "TXXX", "REPLAYGAIN_TRACK_PEAK"},
	{keyRGAlbumGain, "TXXX", "REPLAYGAIN_ALBUM_GAIN"},
	{keyRGAlbumPeak, "TXXX", "REPLAYGAIN_ALBUM_PEAK"},
}

// The MusicBrainz recording id lives in a UFID frame, not TXXX (as Picard does).
const mbUFIDOwner = "http://musicbrainz.org"

// id3LegacyDateFrames are the ID3v2.3 date frames that TDRC/TDOR replace in v2.4.
// They are read as a fallback and dropped on write, since the file becomes v2.4.
var id3LegacyDateFrames = []string{"TYER", "TDAT", "TIME", "TORY", "TRDA"}

// id3Sep separates multiple values inside one ID3v2.4 text or TXXX frame.
const id3Sep = "\x00"

func readMP3(f *os.File) (fieldMap, error) {
	tag, _, err := parseID3(f)
	if err != nil {
		return nil, err
	}
	m := fieldMap{}
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
	if n, t := parsePair(tag.GetTextFrame("TRCK").Text); n > 0 || t > 0 {
		setPair(m, keyTrackNumber, keyTrackTotal, n, t)
	}
	if n, t := parsePair(tag.GetTextFrame("TPOS").Text); n > 0 || t > 0 {
		setPair(m, keyDiscNumber, keyDiscTotal, n, t)
	}
	for _, fr := range tag.GetFrames("UFID") {
		if u, ok := fr.(id3v2.UFIDFrame); ok && u.OwnerIdentifier == mbUFIDOwner {
			m[keyMBRecordingID] = []string{string(u.Identifier)}
		}
	}
	// ID3v2.3 files carry the year in TYER/TORY instead of TDRC/TDOR.
	if _, ok := m[keyDate]; !ok {
		if y := strings.TrimSpace(tag.GetTextFrame("TYER").Text); y != "" {
			m[keyDate] = []string{y}
		}
	}
	if _, ok := m[keyOriginalDate]; !ok {
		if y := strings.TrimSpace(tag.GetTextFrame("TORY").Text); y != "" {
			m[keyOriginalDate] = []string{y}
		}
	}
	return m, nil
}

func setPair(m fieldMap, nKey, tKey string, n, t int) {
	if n > 0 {
		m[nKey] = []string{strconv.Itoa(n)}
	}
	if t > 0 {
		m[tKey] = []string{strconv.Itoa(t)}
	}
}

// writeMP3 rebuilds the ID3v2 tag as v2.4 and copies everything after the old
// tag (the MPEG frames and any ID3v1 tag at the end) unchanged.
func writeMP3(src *os.File, dst io.Writer, m fieldMap) error {
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
		vals := m[fd.key]
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
	if v := id3Pair(m, keyTrackNumber, keyTrackTotal); v != "" {
		tag.AddTextFrame("TRCK", id3v2.EncodingUTF8, v)
	}
	if v := id3Pair(m, keyDiscNumber, keyDiscTotal); v != "" {
		tag.AddTextFrame("TPOS", id3v2.EncodingUTF8, v)
	}
	if id := m.first(keyMBRecordingID); id != "" {
		tag.AddUFIDFrame(id3v2.UFIDFrame{OwnerIdentifier: mbUFIDOwner, Identifier: []byte(id)})
	}

	if _, err := tag.WriteTo(dst); err != nil {
		return err
	}
	if _, err := src.Seek(tagEnd, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

// id3Pair renders TRCK/TPOS: "n" or "n/total". A total without a number can't
// be expressed, so it is dropped.
func id3Pair(m fieldMap, nKey, tKey string) string {
	n, t := m.first(nKey), m.first(tKey)
	switch {
	case n == "":
		return ""
	case t == "":
		return n
	}
	return n + "/" + t
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

	if _, err := f.Seek(0, io.SeekStart); err != nil {
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
