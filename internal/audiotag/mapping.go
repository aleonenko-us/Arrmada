package audiotag

import (
	"strconv"
	"strings"
)

// This file holds the per-format names for every house key, following
// MusicBrainz Picard's tag mapping table. Vorbis comments (FLAC, Ogg) use the
// house key itself. mapping_test.go pins every row.
//
// Specials handled outside the plain tables:
//   - TRACKNUMBER/TRACKTOTAL and DISCNUMBER/DISCTOTAL are one "n/total" value
//     in ID3 (TRCK/TPOS) and one binary atom in MP4 (trkn/disk).
//   - The MusicBrainz recording id is an ID3 UFID frame owned by
//     "http://musicbrainz.org", not a TXXX frame.
//   - COMPILATION is TCMP in ID3 and the integer atom cpil in MP4.
//   - MP4 genres can optionally be joined into one value (WriteOptions).

// id3Field maps a house key to an ID3v2.4 text frame (desc empty) or a TXXX
// frame with that description.
type id3Field struct {
	key   string
	frame string
	desc  string
}

var id3Fields = []id3Field{
	{KeyTitle, "TIT2", ""},
	{KeyArtist, "TPE1", ""},
	{KeyArtists, "TXXX", "ARTISTS"},
	{KeyAlbum, "TALB", ""},
	{KeyAlbumArtist, "TPE2", ""},
	{KeyAlbumArtists, "TXXX", "ALBUMARTISTS"},
	{KeyDate, "TDRC", ""},
	{KeyOriginalDate, "TDOR", ""},
	{KeyGenre, "TCON", ""},
	{KeyCompilation, "TCMP", ""},
	{KeyMBReleaseTrackID, "TXXX", "MusicBrainz Release Track Id"},
	{KeyMBReleaseID, "TXXX", "MusicBrainz Album Id"},
	{KeyMBReleaseGroupID, "TXXX", "MusicBrainz Release Group Id"},
	{KeyMBArtistID, "TXXX", "MusicBrainz Artist Id"},
	{KeyMBAlbumArtistID, "TXXX", "MusicBrainz Album Artist Id"},
	{KeyReleaseType, "TXXX", "MusicBrainz Album Type"},
	{KeyReleaseStatus, "TXXX", "MusicBrainz Album Status"},
	{KeyReleaseCountry, "TXXX", "MusicBrainz Album Release Country"},
	{KeyLabel, "TPUB", ""},
	{KeyCatalogNumber, "TXXX", "CATALOGNUMBER"},
	{KeyBarcode, "TXXX", "BARCODE"},
	{KeyISRC, "TSRC", ""},
	{KeyMedia, "TMED", ""},
	{KeyRGTrackGain, "TXXX", "REPLAYGAIN_TRACK_GAIN"},
	{KeyRGTrackPeak, "TXXX", "REPLAYGAIN_TRACK_PEAK"},
	{KeyRGAlbumGain, "TXXX", "REPLAYGAIN_ALBUM_GAIN"},
	{KeyRGAlbumPeak, "TXXX", "REPLAYGAIN_ALBUM_PEAK"},
}

// mbUFIDOwner owns the UFID frame that holds the MusicBrainz recording id.
const mbUFIDOwner = "http://musicbrainz.org"

// mp4Field maps a house key to a standard atom, or to a freeform
// "----:com.apple.iTunes:<name>" atom when atom is "----". Freeform names are
// matched case-insensitively on read, since taggers disagree (replaygain_*).
type mp4Field struct {
	key  string
	atom string
	name string
}

var mp4Fields = []mp4Field{
	{KeyTitle, "\xa9nam", ""},
	{KeyArtist, "\xa9ART", ""},
	{KeyArtists, "----", "ARTISTS"},
	{KeyAlbum, "\xa9alb", ""},
	{KeyAlbumArtist, "aART", ""},
	{KeyAlbumArtists, "----", "ALBUMARTISTS"},
	{KeyDate, "\xa9day", ""},
	{KeyOriginalDate, "----", "ORIGINALDATE"},
	{KeyGenre, "\xa9gen", ""},
	{KeyMBRecordingID, "----", "MusicBrainz Track Id"},
	{KeyMBReleaseTrackID, "----", "MusicBrainz Release Track Id"},
	{KeyMBReleaseID, "----", "MusicBrainz Album Id"},
	{KeyMBReleaseGroupID, "----", "MusicBrainz Release Group Id"},
	{KeyMBArtistID, "----", "MusicBrainz Artist Id"},
	{KeyMBAlbumArtistID, "----", "MusicBrainz Album Artist Id"},
	{KeyReleaseType, "----", "MusicBrainz Album Type"},
	{KeyReleaseStatus, "----", "MusicBrainz Album Status"},
	{KeyReleaseCountry, "----", "MusicBrainz Album Release Country"},
	{KeyLabel, "----", "LABEL"},
	{KeyCatalogNumber, "----", "CATALOGNUMBER"},
	{KeyBarcode, "----", "BARCODE"},
	{KeyISRC, "----", "ISRC"},
	{KeyMedia, "----", "MEDIA"},
	{KeyRGTrackGain, "----", "replaygain_track_gain"},
	{KeyRGTrackPeak, "----", "replaygain_track_peak"},
	{KeyRGAlbumGain, "----", "replaygain_album_gain"},
	{KeyRGAlbumPeak, "----", "replaygain_album_peak"},
}

// joinPair renders an ID3/MP4-style "n/total" value. A total without a number
// can't be expressed there, so it is dropped.
func joinPair(t Tags, nKey, tKey string) string {
	n, total := t.Get(nKey), t.Get(tKey)
	switch {
	case n == "":
		return ""
	case total == "":
		return n
	}
	return n + "/" + total
}

// applyGenreJoin implements WriteOptions.MP4GenreSeparator.
func applyGenreJoin(t Tags, sep string) Tags {
	if sep == "" || len(t[KeyGenre]) < 2 {
		return t
	}
	out := Tags{}
	for k, v := range t {
		out[k] = v
	}
	out[KeyGenre] = []string{strings.Join(t[KeyGenre], sep)}
	return out
}

// toTagLib converts canonical Tags to the property map go-taglib writes. Every
// house key is present: absent ones map to an empty slice, which go-taglib turns
// into "erase", so the call replaces exactly the house set and nothing else.
func toTagLib(f format, t Tags) map[string][]string {
	props := map[string][]string{}
	for _, k := range houseKeys {
		props[k] = t[k] // nil erases
	}
	for alias := range readAliases {
		props[alias] = nil
	}
	if f == formatMP3 || f == formatMP4 {
		// TagLib's ID3v2 and MP4 property maps take "n/total" in TRACKNUMBER and
		// DISCNUMBER; a separate TRACKTOTAL would become a stray TXXX/freeform.
		props[KeyTrackNumber] = nonEmpty(joinPair(t, KeyTrackNumber, KeyTrackTotal))
		props[KeyDiscNumber] = nonEmpty(joinPair(t, KeyDiscNumber, KeyDiscTotal))
		props[KeyTrackTotal] = nil
		props[KeyDiscTotal] = nil
	}
	return props
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// itoa16 caps a track/disc number at the 16 bits MP4 stores.
func itoa16(s string) uint16 {
	n, _ := strconv.Atoi(s)
	return uint16(min(max(n, 0), 65535))
}
