package audiotag

import (
	"strconv"
	"strings"
)

// Tags is the house tag set. A zero value (empty string, nil slice, 0, false)
// means "not present": Write removes that field from the file, and Read leaves it
// zero when the file doesn't have it.
type Tags struct {
	Title string
	// Artist is the display string, e.g. "Simon & Garfunkel".
	Artist string
	// Artists are the individual credited artists, e.g. ["Simon", "Garfunkel"].
	Artists      []string
	Album        string
	AlbumArtist  string
	AlbumArtists []string

	TrackNumber int
	TrackTotal  int
	DiscNumber  int
	DiscTotal   int

	// Date and OriginalDate are kept as written (YYYY, YYYY-MM or YYYY-MM-DD).
	Date         string
	OriginalDate string

	Genres      []string
	Compilation bool

	MusicBrainzRecordingID    string // Picard's "MusicBrainz Recording Id" (MUSICBRAINZ_TRACKID)
	MusicBrainzReleaseTrackID string // Picard's "MusicBrainz Release Track Id"
	MusicBrainzReleaseID      string // the release (album) id
	MusicBrainzReleaseGroupID string
	MusicBrainzArtistIDs      []string
	MusicBrainzAlbumArtistIDs []string

	// ReleaseType is multi-value because MusicBrainz release groups have a primary
	// type plus any number of secondary types, and Picard writes them as separate
	// values (e.g. ["album", "soundtrack"]).
	ReleaseType    []string
	ReleaseStatus  string
	ReleaseCountry string
	Label          string
	CatalogNumber  string
	Barcode        string
	ISRC           string
	Media          string

	// ReplayGain values are kept as strings exactly as taggers write them, e.g.
	// "-6.48 dB" and "0.988553".
	ReplayGainTrackGain string
	ReplayGainTrackPeak string
	ReplayGainAlbumGain string
	ReplayGainAlbumPeak string
}

// Canonical keys for the house fields. They are the Vorbis comment names from
// Picard's mapping table; the MP3 and MP4 code translates them.
const (
	keyTitle            = "TITLE"
	keyArtist           = "ARTIST"
	keyArtists          = "ARTISTS"
	keyAlbum            = "ALBUM"
	keyAlbumArtist      = "ALBUMARTIST"
	keyAlbumArtists     = "ALBUMARTISTS"
	keyTrackNumber      = "TRACKNUMBER"
	keyTrackTotal       = "TRACKTOTAL"
	keyDiscNumber       = "DISCNUMBER"
	keyDiscTotal        = "DISCTOTAL"
	keyDate             = "DATE"
	keyOriginalDate     = "ORIGINALDATE"
	keyGenre            = "GENRE"
	keyCompilation      = "COMPILATION"
	keyMBRecordingID    = "MUSICBRAINZ_TRACKID"
	keyMBReleaseTrackID = "MUSICBRAINZ_RELEASETRACKID"
	keyMBReleaseID      = "MUSICBRAINZ_ALBUMID"
	keyMBReleaseGroupID = "MUSICBRAINZ_RELEASEGROUPID"
	keyMBArtistID       = "MUSICBRAINZ_ARTISTID"
	keyMBAlbumArtistID  = "MUSICBRAINZ_ALBUMARTISTID"
	keyReleaseType      = "RELEASETYPE"
	keyReleaseStatus    = "RELEASESTATUS"
	keyReleaseCountry   = "RELEASECOUNTRY"
	keyLabel            = "LABEL"
	keyCatalogNumber    = "CATALOGNUMBER"
	keyBarcode          = "BARCODE"
	keyISRC             = "ISRC"
	keyMedia            = "MEDIA"
	keyRGTrackGain      = "REPLAYGAIN_TRACK_GAIN"
	keyRGTrackPeak      = "REPLAYGAIN_TRACK_PEAK"
	keyRGAlbumGain      = "REPLAYGAIN_ALBUM_GAIN"
	keyRGAlbumPeak      = "REPLAYGAIN_ALBUM_PEAK"
)

// houseKeys lists every canonical key in the order they are written, so output
// is deterministic.
var houseKeys = []string{
	keyTitle, keyArtist, keyArtists, keyAlbum, keyAlbumArtist, keyAlbumArtists,
	keyTrackNumber, keyTrackTotal, keyDiscNumber, keyDiscTotal,
	keyDate, keyOriginalDate, keyGenre, keyCompilation,
	keyMBRecordingID, keyMBReleaseTrackID, keyMBReleaseID, keyMBReleaseGroupID,
	keyMBArtistID, keyMBAlbumArtistID,
	keyReleaseType, keyReleaseStatus, keyReleaseCountry, keyLabel, keyCatalogNumber,
	keyBarcode, keyISRC, keyMedia,
	keyRGTrackGain, keyRGTrackPeak, keyRGAlbumGain, keyRGAlbumPeak,
}

// fieldMap is the format-neutral middle step between Tags and a file: canonical
// key -> values. Each format only has to translate keys, not know about Tags.
type fieldMap map[string][]string

func (m fieldMap) first(key string) string {
	if v := m[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// strField and multiField bind a canonical key to a Tags field, so the
// conversion in both directions is driven by one table.
type strField struct {
	key string
	get func(*Tags) *string
}

type multiField struct {
	key string
	get func(*Tags) *[]string
}

var strFields = []strField{
	{keyTitle, func(t *Tags) *string { return &t.Title }},
	{keyArtist, func(t *Tags) *string { return &t.Artist }},
	{keyAlbum, func(t *Tags) *string { return &t.Album }},
	{keyAlbumArtist, func(t *Tags) *string { return &t.AlbumArtist }},
	{keyDate, func(t *Tags) *string { return &t.Date }},
	{keyOriginalDate, func(t *Tags) *string { return &t.OriginalDate }},
	{keyMBRecordingID, func(t *Tags) *string { return &t.MusicBrainzRecordingID }},
	{keyMBReleaseTrackID, func(t *Tags) *string { return &t.MusicBrainzReleaseTrackID }},
	{keyMBReleaseID, func(t *Tags) *string { return &t.MusicBrainzReleaseID }},
	{keyMBReleaseGroupID, func(t *Tags) *string { return &t.MusicBrainzReleaseGroupID }},
	{keyReleaseStatus, func(t *Tags) *string { return &t.ReleaseStatus }},
	{keyReleaseCountry, func(t *Tags) *string { return &t.ReleaseCountry }},
	{keyLabel, func(t *Tags) *string { return &t.Label }},
	{keyCatalogNumber, func(t *Tags) *string { return &t.CatalogNumber }},
	{keyBarcode, func(t *Tags) *string { return &t.Barcode }},
	{keyISRC, func(t *Tags) *string { return &t.ISRC }},
	{keyMedia, func(t *Tags) *string { return &t.Media }},
	{keyRGTrackGain, func(t *Tags) *string { return &t.ReplayGainTrackGain }},
	{keyRGTrackPeak, func(t *Tags) *string { return &t.ReplayGainTrackPeak }},
	{keyRGAlbumGain, func(t *Tags) *string { return &t.ReplayGainAlbumGain }},
	{keyRGAlbumPeak, func(t *Tags) *string { return &t.ReplayGainAlbumPeak }},
}

var multiFields = []multiField{
	{keyArtists, func(t *Tags) *[]string { return &t.Artists }},
	{keyAlbumArtists, func(t *Tags) *[]string { return &t.AlbumArtists }},
	{keyGenre, func(t *Tags) *[]string { return &t.Genres }},
	{keyMBArtistID, func(t *Tags) *[]string { return &t.MusicBrainzArtistIDs }},
	{keyMBAlbumArtistID, func(t *Tags) *[]string { return &t.MusicBrainzAlbumArtistIDs }},
	{keyReleaseType, func(t *Tags) *[]string { return &t.ReleaseType }},
}

// fields converts Tags to a fieldMap, leaving out everything that is empty.
func (t Tags) fields() fieldMap {
	m := fieldMap{}
	for _, f := range strFields {
		if v := strings.TrimSpace(*f.get(&t)); v != "" {
			m[f.key] = []string{v}
		}
	}
	for _, f := range multiFields {
		var vals []string
		for _, v := range *f.get(&t) {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			m[f.key] = vals
		}
	}
	setInt := func(key string, n int) {
		if n > 0 {
			m[key] = []string{strconv.Itoa(n)}
		}
	}
	setInt(keyTrackNumber, t.TrackNumber)
	setInt(keyTrackTotal, t.TrackTotal)
	setInt(keyDiscNumber, t.DiscNumber)
	setInt(keyDiscTotal, t.DiscTotal)
	if t.Compilation {
		m[keyCompilation] = []string{"1"}
	}
	return m
}

// tagsFromFields converts a fieldMap read from a file back to Tags. Single-value
// fields take the first value when a file has several.
func tagsFromFields(m fieldMap) Tags {
	var t Tags
	for _, f := range strFields {
		*f.get(&t) = m.first(f.key)
	}
	for _, f := range multiFields {
		var vals []string
		for _, v := range m[f.key] {
			if v != "" {
				vals = append(vals, v)
			}
		}
		*f.get(&t) = vals
	}
	// "3/12" in TRACKNUMBER is common in Vorbis comments written by older tools;
	// the explicit total wins when both are present.
	t.TrackNumber, t.TrackTotal = parsePair(m.first(keyTrackNumber))
	if n := parseLeadingInt(m.first(keyTrackTotal)); n > 0 {
		t.TrackTotal = n
	}
	t.DiscNumber, t.DiscTotal = parsePair(m.first(keyDiscNumber))
	if n := parseLeadingInt(m.first(keyDiscTotal)); n > 0 {
		t.DiscTotal = n
	}
	t.Compilation = parseBool(m.first(keyCompilation))
	return t
}

// parsePair parses "n" or "n/total" (ID3 TRCK/TPOS style).
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
