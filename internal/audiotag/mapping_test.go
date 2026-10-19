package audiotag

import (
	"reflect"
	"testing"
)

// picardMapping is MusicBrainz Picard's tag mapping table for the house keys,
// written out independently of mapping.go so a typo there fails here. Vorbis
// uses the key itself. MP4 "----:X" means "----:com.apple.iTunes:X".
var picardMapping = map[string]struct{ id3, mp4 string }{
	KeyTitle:            {"TIT2", "\xa9nam"},
	KeyArtist:           {"TPE1", "\xa9ART"},
	KeyArtists:          {"TXXX:ARTISTS", "----:ARTISTS"},
	KeyArtistSort:       {"TSOP", "soar"},
	KeyAlbum:            {"TALB", "\xa9alb"},
	KeyAlbumArtist:      {"TPE2", "aART"},
	KeyAlbumArtists:     {"TXXX:ALBUMARTISTS", "----:ALBUMARTISTS"}, // not in Picard's table; follows ARTISTS
	KeyAlbumArtistSort:  {"TSO2", "soaa"},
	KeyDiscSubtitle:     {"TSST", "----:DISCSUBTITLE"},
	KeyAcoustID:         {"TXXX:Acoustid Id", "----:Acoustid Id"},
	KeyTrackNumber:      {"TRCK", "trkn"},
	KeyTrackTotal:       {"TRCK", "trkn"},
	KeyDiscNumber:       {"TPOS", "disk"},
	KeyDiscTotal:        {"TPOS", "disk"},
	KeyDate:             {"TDRC", "\xa9day"},
	KeyOriginalDate:     {"TDOR", "----:ORIGINALDATE"},
	KeyGenre:            {"TCON", "\xa9gen"},
	KeyCompilation:      {"TCMP", "cpil"},
	KeyMBRecordingID:    {"UFID:http://musicbrainz.org", "----:MusicBrainz Track Id"},
	KeyMBReleaseTrackID: {"TXXX:MusicBrainz Release Track Id", "----:MusicBrainz Release Track Id"},
	KeyMBReleaseID:      {"TXXX:MusicBrainz Album Id", "----:MusicBrainz Album Id"},
	KeyMBReleaseGroupID: {"TXXX:MusicBrainz Release Group Id", "----:MusicBrainz Release Group Id"},
	KeyMBArtistID:       {"TXXX:MusicBrainz Artist Id", "----:MusicBrainz Artist Id"},
	KeyMBAlbumArtistID:  {"TXXX:MusicBrainz Album Artist Id", "----:MusicBrainz Album Artist Id"},
	KeyReleaseType:      {"TXXX:MusicBrainz Album Type", "----:MusicBrainz Album Type"},
	KeyReleaseStatus:    {"TXXX:MusicBrainz Album Status", "----:MusicBrainz Album Status"},
	KeyReleaseCountry:   {"TXXX:MusicBrainz Album Release Country", "----:MusicBrainz Album Release Country"},
	KeyLabel:            {"TPUB", "----:LABEL"},
	KeyCatalogNumber:    {"TXXX:CATALOGNUMBER", "----:CATALOGNUMBER"},
	KeyBarcode:          {"TXXX:BARCODE", "----:BARCODE"},
	KeyISRC:             {"TSRC", "----:ISRC"},
	KeyMedia:            {"TMED", "----:MEDIA"},
	KeyRGTrackGain:      {"TXXX:REPLAYGAIN_TRACK_GAIN", "----:replaygain_track_gain"},
	KeyRGTrackPeak:      {"TXXX:REPLAYGAIN_TRACK_PEAK", "----:replaygain_track_peak"},
	KeyRGAlbumGain:      {"TXXX:REPLAYGAIN_ALBUM_GAIN", "----:replaygain_album_gain"},
	KeyRGAlbumPeak:      {"TXXX:REPLAYGAIN_ALBUM_PEAK", "----:replaygain_album_peak"},
}

// id3Name and mp4Name report where the permissive backend stores a key,
// including the specials that live outside the plain tables.
func id3Name(key string) string {
	switch key {
	case KeyTrackNumber, KeyTrackTotal:
		return "TRCK"
	case KeyDiscNumber, KeyDiscTotal:
		return "TPOS"
	case KeyMBRecordingID:
		return "UFID:" + mbUFIDOwner
	}
	for _, f := range id3Fields {
		if f.key == key {
			if f.frame == "TXXX" {
				return "TXXX:" + f.desc
			}
			return f.frame
		}
	}
	return ""
}

func mp4Name(key string) string {
	switch key {
	case KeyTrackNumber, KeyTrackTotal:
		return atomTrack
	case KeyDiscNumber, KeyDiscTotal:
		return atomDisc
	case KeyCompilation:
		return atomCompilation
	}
	for _, f := range mp4Fields {
		if f.key == key {
			if f.atom == "----" {
				return "----:" + f.name
			}
			return f.atom
		}
	}
	return ""
}

func TestMappingMatchesPicard(t *testing.T) {
	if len(picardMapping) != len(houseKeys) {
		t.Fatalf("table has %d rows, houseKeys has %d", len(picardMapping), len(houseKeys))
	}
	for _, k := range houseKeys {
		want, ok := picardMapping[k]
		if !ok {
			t.Errorf("%s: no row", k)
			continue
		}
		if got := id3Name(k); got != want.id3 {
			t.Errorf("%s: ID3 %q, want %q", k, got, want.id3)
		}
		if got := mp4Name(k); got != want.mp4 {
			t.Errorf("%s: MP4 %q, want %q", k, got, want.mp4)
		}
	}
}

func TestToTagLib(t *testing.T) {
	in := Tags{KeyTitle: {"x"}, KeyTrackNumber: {"3"}, KeyTrackTotal: {"12"}, KeyDiscNumber: {"1"}}
	for _, ft := range []format{formatMP3, formatMP4} {
		p := toTagLib(ft, in)
		if !reflect.DeepEqual(p[KeyTrackNumber], []string{"3/12"}) || !reflect.DeepEqual(p[KeyDiscNumber], []string{"1"}) {
			t.Errorf("format %d: numbers %q %q", ft, p[KeyTrackNumber], p[KeyDiscNumber])
		}
		if p[KeyTrackTotal] != nil || p[KeyDiscTotal] != nil {
			t.Errorf("format %d: separate totals passed to TagLib", ft)
		}
	}
	for _, ft := range []format{formatFLAC, formatOgg} {
		p := toTagLib(ft, in)
		if !reflect.DeepEqual(p[KeyTrackTotal], []string{"12"}) || !reflect.DeepEqual(p[KeyTrackNumber], []string{"3"}) {
			t.Errorf("format %d: Vorbis numbers %q/%q", ft, p[KeyTrackNumber], p[KeyTrackTotal])
		}
	}
	// Every house key and alias is present, so absent ones are erased.
	p := toTagLib(formatFLAC, in)
	for _, k := range append(HouseKeys(), "TOTALTRACKS", "TOTALDISCS", "ORIGINALYEAR") {
		if _, ok := p[k]; !ok {
			t.Errorf("%s missing from the TagLib map", k)
		}
	}
	if p[KeyArtist] != nil {
		t.Error("absent key should map to nil (erase)")
	}
	// Vorbis gets the mirror spellings of the house values; ID3/MP4 erase them.
	in[KeyOriginalDate] = []string{"1997-09-22"}
	for _, ft := range []format{formatFLAC, formatOgg} {
		p := toTagLib(ft, in)
		if !reflect.DeepEqual(p["TOTALTRACKS"], []string{"12"}) || !reflect.DeepEqual(p["ORIGINALYEAR"], []string{"1997"}) || p["TOTALDISCS"] != nil {
			t.Errorf("format %d: mirrors %q %q %q", ft, p["TOTALTRACKS"], p["TOTALDISCS"], p["ORIGINALYEAR"])
		}
	}
	if p := toTagLib(formatMP3, in); p["TOTALTRACKS"] != nil || p["ORIGINALYEAR"] != nil {
		t.Error("ID3 should not carry the Vorbis mirrors")
	}
}

func TestVorbisMirrors(t *testing.T) {
	got := vorbisMirrors(Tags{KeyTrackTotal: {"9"}, KeyDiscTotal: {"2"}, KeyOriginalDate: {"1975"}})
	want := map[string][]string{"TOTALTRACKS": {"9"}, "TOTALDISCS": {"2"}, "ORIGINALYEAR": {"1975"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mirrors = %v, want %v", got, want)
	}
	if got := vorbisMirrors(Tags{KeyOriginalDate: {"unknown"}}); len(got) != 0 {
		t.Errorf("a date with no year gave %v", got)
	}
	// Reading: the house spelling wins, the mirror fills in when it's alone.
	if r := normalizeRead(map[string][]string{"ORIGINALYEAR": {"1975"}}); r.Get(KeyOriginalDate) != "1975" {
		t.Errorf("ORIGINALYEAR alone read as %v", r)
	}
	if r := normalizeRead(map[string][]string{"ORIGINALYEAR": {"1975"}, "ORIGINALDATE": {"1975-11-21"}}); r.Get(KeyOriginalDate) != "1975-11-21" {
		t.Errorf("ORIGINALDATE should win: %v", r)
	}
}

func TestGenreJoin(t *testing.T) {
	in := Tags{KeyGenre: {"Rock", "Pop"}}
	if got := applyGenreJoin(in, "; "); !reflect.DeepEqual(got[KeyGenre], []string{"Rock; Pop"}) {
		t.Errorf("joined %q", got[KeyGenre])
	}
	if !reflect.DeepEqual(in[KeyGenre], []string{"Rock", "Pop"}) {
		t.Error("applyGenreJoin modified its input")
	}
	if got := applyGenreJoin(in, ""); len(got[KeyGenre]) != 2 {
		t.Error("empty separator should keep separate values")
	}
}
