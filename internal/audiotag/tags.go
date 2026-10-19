package audiotag

import (
	"fmt"
	"strconv"
	"strings"
)

// Tags holds house tags as key -> values. Keys are the upper-case names from
// Picard's tag mapping table as they appear in Vorbis comments (which are also
// TagLib's property names), e.g. "ARTISTS" or "MUSICBRAINZ_TRACKID"; use the
// Key* constants. Only house keys are allowed (see HouseKeys).
//
// Track and disc numbers are plain decimal strings in TRACKNUMBER/TRACKTOTAL and
// DISCNUMBER/DISCTOTAL; the format code turns them into "n/total" where the
// format wants that. COMPILATION is "1" when set and absent otherwise.
type Tags map[string][]string

// House keys. Every one is round-tripped in every supported format.
const (
	KeyTitle            = "TITLE"
	KeyArtist           = "ARTIST"  // display string, e.g. "Simon & Garfunkel"
	KeyArtists          = "ARTISTS" // multi: each credited artist
	KeyAlbum            = "ALBUM"
	KeyAlbumArtist      = "ALBUMARTIST"
	KeyAlbumArtists     = "ALBUMARTISTS" // multi
	KeyTrackNumber      = "TRACKNUMBER"
	KeyTrackTotal       = "TRACKTOTAL"
	KeyDiscNumber       = "DISCNUMBER"
	KeyDiscTotal        = "DISCTOTAL"
	KeyDate             = "DATE"
	KeyOriginalDate     = "ORIGINALDATE"
	KeyGenre            = "GENRE" // multi
	KeyCompilation      = "COMPILATION"
	KeyMBRecordingID    = "MUSICBRAINZ_TRACKID" // Picard's name for the recording id
	KeyMBReleaseTrackID = "MUSICBRAINZ_RELEASETRACKID"
	KeyMBReleaseID      = "MUSICBRAINZ_ALBUMID"
	KeyMBReleaseGroupID = "MUSICBRAINZ_RELEASEGROUPID"
	KeyMBArtistID       = "MUSICBRAINZ_ARTISTID"      // multi
	KeyMBAlbumArtistID  = "MUSICBRAINZ_ALBUMARTISTID" // multi
	KeyReleaseType      = "RELEASETYPE"               // multi: primary type, then secondary types
	KeyReleaseStatus    = "RELEASESTATUS"
	KeyReleaseCountry   = "RELEASECOUNTRY"
	KeyLabel            = "LABEL"
	KeyCatalogNumber    = "CATALOGNUMBER"
	KeyBarcode          = "BARCODE"
	KeyISRC             = "ISRC"
	KeyMedia            = "MEDIA"
	KeyRGTrackGain      = "REPLAYGAIN_TRACK_GAIN"
	KeyRGTrackPeak      = "REPLAYGAIN_TRACK_PEAK"
	KeyRGAlbumGain      = "REPLAYGAIN_ALBUM_GAIN"
	KeyRGAlbumPeak      = "REPLAYGAIN_ALBUM_PEAK"
)

// houseKeys lists every house key in write order, so output is deterministic.
var houseKeys = []string{
	KeyTitle, KeyArtist, KeyArtists, KeyAlbum, KeyAlbumArtist, KeyAlbumArtists,
	KeyTrackNumber, KeyTrackTotal, KeyDiscNumber, KeyDiscTotal,
	KeyDate, KeyOriginalDate, KeyGenre, KeyCompilation,
	KeyMBRecordingID, KeyMBReleaseTrackID, KeyMBReleaseID, KeyMBReleaseGroupID,
	KeyMBArtistID, KeyMBAlbumArtistID,
	KeyReleaseType, KeyReleaseStatus, KeyReleaseCountry, KeyLabel, KeyCatalogNumber,
	KeyBarcode, KeyISRC, KeyMedia,
	KeyRGTrackGain, KeyRGTrackPeak, KeyRGAlbumGain, KeyRGAlbumPeak,
}

var multiKeys = map[string]bool{
	KeyArtists: true, KeyAlbumArtists: true, KeyGenre: true,
	KeyMBArtistID: true, KeyMBAlbumArtistID: true, KeyReleaseType: true,
}

// readAliases are other spellings taggers use for house keys. They are read as
// the house key and removed on write, so a file never carries two totals.
var readAliases = map[string]string{
	"TOTALTRACKS": KeyTrackTotal,
	"TOTALDISCS":  KeyDiscTotal,
}

// HouseKeys returns the keys Read reports and Write manages, in write order.
func HouseKeys() []string { return append([]string(nil), houseKeys...) }

func isHouseKey(k string) bool {
	for _, h := range houseKeys {
		if h == k {
			return true
		}
	}
	return false
}

// Get returns the first value of key, or "".
func (t Tags) Get(key string) string {
	if v := t[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// first is Get under the name the format code uses.
func (t Tags) first(key string) string { return t.Get(key) }

// normalizeRead turns whatever a format yielded (any key case, aliases,
// "n/total" numbers, "true" booleans, extra keys) into canonical house Tags.
// Both backends go through it, so they report files identically.
func normalizeRead(raw map[string][]string) Tags {
	up := map[string][]string{}
	for k, vs := range raw {
		k = strings.ToUpper(strings.TrimSpace(k))
		if a, ok := readAliases[k]; ok {
			if _, dup := raw[a]; dup {
				continue // the canonical spelling wins
			}
			k = a
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				up[k] = append(up[k], v)
			}
		}
	}

	t := Tags{}
	for _, k := range houseKeys {
		vs := up[k]
		if len(vs) == 0 {
			continue
		}
		if !multiKeys[k] {
			vs = vs[:1] // a single-value field keeps its first value
		}
		t[k] = vs
	}
	normalizePair(t, up, KeyTrackNumber, KeyTrackTotal)
	normalizePair(t, up, KeyDiscNumber, KeyDiscTotal)
	if parseBool(t.Get(KeyCompilation)) {
		t[KeyCompilation] = []string{"1"}
	} else {
		delete(t, KeyCompilation)
	}
	return t
}

// normalizePair splits "3/12" in the number key; an explicit total wins.
func normalizePair(t Tags, up map[string][]string, nKey, tKey string) {
	first := func(k string) string {
		if v := up[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	n, total := parsePair(first(nKey))
	if x := parseLeadingInt(first(tKey)); x > 0 {
		total = x
	}
	delete(t, nKey)
	delete(t, tKey)
	if n > 0 {
		t[nKey] = []string{strconv.Itoa(n)}
	}
	if total > 0 {
		t[tKey] = []string{strconv.Itoa(total)}
	}
}

// prepareWrite validates caller input and returns canonical Tags: upper-case
// house keys only, trimmed non-empty values, one value for single-value keys,
// numeric track/disc fields and a "1"-or-absent COMPILATION.
func prepareWrite(in Tags) (Tags, error) {
	out := Tags{}
	for k, vs := range in {
		key := strings.ToUpper(strings.TrimSpace(k))
		if !isHouseKey(key) {
			return nil, fmt.Errorf("audiotag: %q is not a house key", k)
		}
		var vals []string
		for _, v := range vs {
			if strings.ContainsAny(v, "\x00\v") {
				return nil, fmt.Errorf("audiotag: %s value contains a control character", key)
			}
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		if len(vals) > 1 && !multiKeys[key] {
			return nil, fmt.Errorf("audiotag: %s takes one value, got %d", key, len(vals))
		}
		out[key] = append(out[key], vals...)
	}
	for _, k := range []string{KeyTrackNumber, KeyTrackTotal, KeyDiscNumber, KeyDiscTotal} {
		if v := out.Get(k); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 65535 {
				return nil, fmt.Errorf("audiotag: %s must be a number from 1 to 65535, got %q", k, v)
			}
			out[k] = []string{strconv.Itoa(n)}
		}
	}
	if v := out.Get(KeyCompilation); v != "" {
		if parseBool(v) {
			out[KeyCompilation] = []string{"1"}
		} else {
			delete(out, KeyCompilation)
		}
	}
	return out, nil
}

// parsePair parses "n" or "n/total" (ID3 TRCK/TPOS and MP4 property style).
func parsePair(s string) (n, total int) {
	a, b, _ := strings.Cut(s, "/")
	return parseLeadingInt(a), parseLeadingInt(b)
}

// parseLeadingInt reads the leading digits of s ("07", " 3 ", "3 of 12"),
// returning 0 when there are none.
func parseLeadingInt(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
