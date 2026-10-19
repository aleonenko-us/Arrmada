package music

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/library"
)

// House tags (MUS-33): one tag style, Picard's names and mappings, that Plex, Jellyfin
// and Navidrome all read correctly. audiotag maps each key to Vorbis comments, ID3v2.4
// frames and MP4 atoms (the table in docs/roadmap/MUS.md). Every value comes from the
// matched edition and its credits, never from the file's old tags or a release name.
//
// Fields Arrmada has no source for yet keep what the file arrived with rather than being
// wiped: genres, the AcoustID (MUS-32 fingerprints) and ReplayGain (MUS-50). Every other
// house field with no value is removed, so a stale MusicBrainz id or a wrong album artist
// can't survive a retag. A release that isn't on MusicBrainz gets no MUSICBRAINZ_* field.

// carriedKeys are the house keys kept from the file's own tags.
var carriedKeys = []string{
	audiotag.KeyGenre, audiotag.KeyAcoustID,
	audiotag.KeyRGTrackGain, audiotag.KeyRGTrackPeak, audiotag.KeyRGAlbumGain, audiotag.KeyRGAlbumPeak,
}

// reMBID is a MusicBrainz id: a UUID. Anything else ("deezer:123", a migrated blank) is
// never written to a MUSICBRAINZ_* field.
var reMBID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func mbid(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if reMBID.MatchString(s) {
		return s
	}
	return ""
}

// HouseTags builds the house tags for one release track of the context's edition.
// asFound is what the file carries now (nil for none): only the carried keys are read
// from it. ok is false for a track that isn't on the edition.
func (tc TagContext) HouseTags(releaseTrackID int64, asFound audiotag.Tags) (audiotag.Tags, bool) {
	med, t, ok := tc.Track(releaseTrackID)
	if !ok {
		return nil, false
	}
	e, al := tc.Edition, tc.Album
	rc, trc := tc.releaseCredit(), tc.trackCredit(t)
	out := audiotag.Tags{}
	set := func(k string, vs ...string) {
		var keep []string
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				keep = append(keep, v)
			}
		}
		if len(keep) > 0 {
			out[k] = keep
		}
	}
	num := func(k string, n int) {
		if n > 0 {
			out[k] = []string{strconv.Itoa(n)}
		}
	}

	set(audiotag.KeyTitle, t.Title)
	set(audiotag.KeyArtist, creditText(trc, al.ArtistCredit))
	set(audiotag.KeyArtists, creditNames(trc)...)
	set(audiotag.KeyArtistSort, tc.creditSort(trc))
	title := e.Title
	if strings.TrimSpace(title) == "" {
		title = al.Title
	}
	set(audiotag.KeyAlbum, title)
	set(audiotag.KeyAlbumArtist, creditText(rc, al.ArtistCredit))
	set(audiotag.KeyAlbumArtists, creditNames(rc)...)
	set(audiotag.KeyAlbumArtistSort, tc.creditSort(rc))

	num(audiotag.KeyTrackNumber, t.Position)
	total := med.TrackCount
	if total < len(med.Tracks) {
		total = len(med.Tracks)
	}
	num(audiotag.KeyTrackTotal, total)
	// The disc is always written, also on a one-disc album: players group by it.
	num(audiotag.KeyDiscNumber, max(med.Position, 1))
	num(audiotag.KeyDiscTotal, max(tc.discCount(), 1))
	set(audiotag.KeyDiscSubtitle, med.Title)

	set(audiotag.KeyDate, e.ReleaseDate)
	orig := al.ReleaseDate
	if orig == "" && al.Year > 0 {
		orig = strconv.Itoa(al.Year)
	}
	set(audiotag.KeyOriginalDate, orig)
	if tc.Compilation() {
		out[audiotag.KeyCompilation] = []string{"1"}
	}
	set(audiotag.KeyReleaseType, releaseTypes(al)...)
	set(audiotag.KeyReleaseStatus, strings.ToLower(e.Status))
	set(audiotag.KeyReleaseCountry, e.Country)
	set(audiotag.KeyMedia, med.Format)
	set(audiotag.KeyLabel, e.Label)
	set(audiotag.KeyCatalogNumber, e.CatalogNumber)
	set(audiotag.KeyBarcode, e.Barcode)
	if isrcs := tc.ISRCs[t.RecordingID]; len(isrcs) > 0 {
		set(audiotag.KeyISRC, isrcs[0])
	}

	if tc.onMusicBrainz() {
		set(audiotag.KeyMBRecordingID, mbid(t.RecordingMBID))
		set(audiotag.KeyMBReleaseTrackID, mbid(t.MBID))
		set(audiotag.KeyMBReleaseID, mbid(e.MBID))
		set(audiotag.KeyMBReleaseGroupID, mbid(al.MBID))
		set(audiotag.KeyMBArtistID, creditMBIDs(trc)...)
		set(audiotag.KeyMBAlbumArtistID, creditMBIDs(rc)...)
	}
	for _, k := range carriedKeys {
		if vs := asFound[k]; len(vs) > 0 {
			out[k] = append([]string(nil), vs...)
		}
	}
	return out, true
}

// onMusicBrainz reports whether the edition's ids are MusicBrainz's (a gap-fill release
// from Deezer or Discogs writes none).
func (tc TagContext) onMusicBrainz() bool {
	src := strings.ToLower(strings.TrimSpace(tc.Edition.Source))
	return src == "" || src == "musicbrainz"
}

// creditText is a credit as it reads, or fallback when there's no credit.
func creditText(c Credit, fallback string) string {
	if s := strings.TrimSpace(c.Display); s != "" {
		return s
	}
	if s := strings.TrimSpace(CreditDisplay(c.Names)); s != "" {
		return s
	}
	return fallback
}

// creditNames is each credited artist, as credited.
func creditNames(c Credit) []string {
	out := make([]string, 0, len(c.Names))
	for _, n := range c.Names {
		out = append(out, n.Name)
	}
	return out
}

// creditMBIDs is each credited artist's MBID, in the order of creditNames — or nothing at
// all when one is missing, since a reader pairs the two lists by position.
func creditMBIDs(c Credit) []string {
	out := make([]string, 0, len(c.Names))
	for _, n := range c.Names {
		id := mbid(n.ArtistMBID)
		if id == "" {
			return nil
		}
		out = append(out, id)
	}
	return out
}

// creditSort is the credit with each artist's sort name ("Beatles, The & Preston,
// Billy"), or "" when no artist in it has one.
func (tc TagContext) creditSort(c Credit) string {
	var b strings.Builder
	sorted := false
	for _, n := range c.Names {
		name := n.Name
		if s := tc.Sorts[n.ArtistID]; s != "" {
			name, sorted = s, true
		}
		b.WriteString(name)
		b.WriteString(n.JoinPhrase)
	}
	if !sorted {
		return ""
	}
	return strings.TrimSpace(b.String())
}

// releaseTypes is MusicBrainz's release-group type list in Picard's lower case: the
// primary type, then each secondary type ("album", "compilation").
func releaseTypes(al Album) []string {
	var out []string
	if p := strings.TrimSpace(al.PrimaryType); p != "" {
		out = append(out, strings.ToLower(p))
	}
	for _, s := range strings.Split(al.SecondaryTypes, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// Name is what a release track's file is named from (MUS-34), with the file's measured
// facts. ok is false for a track that isn't on the edition.
func (tc TagContext) Name(releaseTrackID int64, facts FileFacts) (library.MusicName, bool) {
	med, t, ok := tc.Track(releaseTrackID)
	if !ok {
		return library.MusicName{}, false
	}
	al, e := tc.Album, tc.Edition
	// The album's stored year, exactly as the folder has always been named from (an album
	// with no year stored gets none, though its release date may have one).
	orig := al.Year
	n := library.MusicName{
		// The library artist the album is filed under (Various Artists for a compilation):
		// what the album's folder has always been named after.
		AlbumArtist:     tc.Artist.Name,
		AlbumArtistSort: tc.Artist.SortName,
		AlbumTitle:      al.Title,
		ReleaseYear:     yearOf(e.ReleaseDate),
		OriginalYear:    orig,
		AlbumType:       al.AlbumType,
		Edition:         e.Disambiguation,
		Media:           med.Format,
		AlbumMBID:       mbid(al.MBID),
		ReleaseMBID:     mbid(e.MBID),
		Compilation:     tc.Compilation(),
		Disc:            max(med.Position, 1),
		DiscCount:       max(tc.discCount(), 1),
		DiscTitle:       med.Title,
		Track:           t.Position,
		TrackTitle:      t.Title,
		TrackArtist:     creditText(tc.trackCredit(t), al.ArtistCredit),
		Quality:         string(facts.Quality),
		Codec:           facts.Codec,
		BitDepth:        facts.BitDepth,
		SampleRate:      facts.SampleRate,
	}
	if n.AlbumArtist == "" {
		n.AlbumArtist = creditText(tc.releaseCredit(), al.ArtistCredit)
	}
	return n, true
}
