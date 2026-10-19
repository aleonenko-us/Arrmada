package audiotag

import "strconv"

// Fields is a typed view of the house tags, for callers that would rather not
// work with keys. Tags is the source of truth; Fields converts both ways.
type Fields struct {
	Title           string
	Artist          string
	Artists         []string
	ArtistSort      string
	Album           string
	AlbumArtist     string
	AlbumArtists    []string
	AlbumArtistSort string

	TrackNumber, TrackTotal int
	DiscNumber, DiscTotal   int
	DiscSubtitle            string

	Date         string
	OriginalDate string
	Genres       []string
	Compilation  bool

	MusicBrainzRecordingID    string
	MusicBrainzReleaseTrackID string
	MusicBrainzReleaseID      string
	MusicBrainzReleaseGroupID string
	MusicBrainzArtistIDs      []string
	MusicBrainzAlbumArtistIDs []string
	AcoustID                  string

	ReleaseType    []string
	ReleaseStatus  string
	ReleaseCountry string
	Label          string
	CatalogNumber  string
	Barcode        string
	ISRC           string
	Media          string

	ReplayGainTrackGain string
	ReplayGainTrackPeak string
	ReplayGainAlbumGain string
	ReplayGainAlbumPeak string
}

func (f *Fields) strs() map[string]*string {
	return map[string]*string{
		KeyTitle: &f.Title, KeyArtist: &f.Artist, KeyAlbum: &f.Album, KeyAlbumArtist: &f.AlbumArtist,
		KeyArtistSort: &f.ArtistSort, KeyAlbumArtistSort: &f.AlbumArtistSort, KeyDiscSubtitle: &f.DiscSubtitle,
		KeyAcoustID: &f.AcoustID, KeyDate: &f.Date, KeyOriginalDate: &f.OriginalDate,
		KeyMBRecordingID: &f.MusicBrainzRecordingID, KeyMBReleaseTrackID: &f.MusicBrainzReleaseTrackID,
		KeyMBReleaseID: &f.MusicBrainzReleaseID, KeyMBReleaseGroupID: &f.MusicBrainzReleaseGroupID,
		KeyReleaseStatus: &f.ReleaseStatus, KeyReleaseCountry: &f.ReleaseCountry, KeyLabel: &f.Label,
		KeyCatalogNumber: &f.CatalogNumber, KeyBarcode: &f.Barcode, KeyISRC: &f.ISRC, KeyMedia: &f.Media,
		KeyRGTrackGain: &f.ReplayGainTrackGain, KeyRGTrackPeak: &f.ReplayGainTrackPeak,
		KeyRGAlbumGain: &f.ReplayGainAlbumGain, KeyRGAlbumPeak: &f.ReplayGainAlbumPeak,
	}
}

func (f *Fields) lists() map[string]*[]string {
	return map[string]*[]string{
		KeyArtists: &f.Artists, KeyAlbumArtists: &f.AlbumArtists, KeyGenre: &f.Genres,
		KeyMBArtistID: &f.MusicBrainzArtistIDs, KeyMBAlbumArtistID: &f.MusicBrainzAlbumArtistIDs,
		KeyReleaseType: &f.ReleaseType,
	}
}

func (f *Fields) ints() map[string]*int {
	return map[string]*int{
		KeyTrackNumber: &f.TrackNumber, KeyTrackTotal: &f.TrackTotal,
		KeyDiscNumber: &f.DiscNumber, KeyDiscTotal: &f.DiscTotal,
	}
}

// Tags converts f to Tags, leaving out empty fields.
func (f Fields) Tags() Tags {
	t := Tags{}
	for k, p := range f.strs() {
		if *p != "" {
			t[k] = []string{*p}
		}
	}
	for k, p := range f.lists() {
		if len(*p) > 0 {
			t[k] = append([]string(nil), *p...)
		}
	}
	for k, p := range f.ints() {
		if *p > 0 {
			t[k] = []string{strconv.Itoa(*p)}
		}
	}
	if f.Compilation {
		t[KeyCompilation] = []string{"1"}
	}
	return t
}

// FieldsOf converts Tags (as returned by Read) to Fields.
func FieldsOf(t Tags) Fields {
	var f Fields
	for k, p := range f.strs() {
		*p = t.Get(k)
	}
	for k, p := range f.lists() {
		if len(t[k]) > 0 {
			*p = append([]string(nil), t[k]...)
		}
	}
	for k, p := range f.ints() {
		*p = parseLeadingInt(t.Get(k))
	}
	f.Compilation = parseBool(t.Get(KeyCompilation))
	return f
}
