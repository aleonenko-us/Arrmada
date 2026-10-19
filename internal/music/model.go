package music

import "strings"

// The music model (MUS-28), MusicBrainz's own shape with Arrmada's ids:
//
//	albums (release group) -< Edition (music_releases) -< Medium (disc) -< ReleaseTrack
//	ReleaseTrack >- Recording;  TrackFile: at most one per ReleaseTrack, or an album extra
//	Credit: one or more artists with join phrases, used by albums, releases and tracks
//
// Album and Track (music.go) stay the shapes the pages and the automation read: an
// album's Tracks are its target edition's release tracks, each with its file folded in.

// Credit is an artist credit: "Daft Punk feat. Pharrell Williams" is one credit with two
// names. An identical credit is stored once (CreditKey).
type Credit struct {
	ID      int64        `json:"id"`
	Display string       `json:"display"`
	Names   []CreditName `json:"names,omitempty"`
}

// CreditName is one artist in a credit, as credited there.
type CreditName struct {
	ArtistID   int64  `json:"artist_id"`
	ArtistMBID string `json:"artist_mbid,omitempty"`
	Name       string `json:"name"` // as credited, which may differ from the artist's own name
	JoinPhrase string `json:"join_phrase,omitempty"`
}

// CreditKey is the identity of a credit: the artists' MBIDs with the join phrases between
// them, "mbid" for a single artist and "mbid| feat. |mbid" for two. Two credits with the
// same key are the same credit, so it is stored once.
func CreditKey(names []CreditName) string {
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(n.ArtistMBID)
		if n.JoinPhrase != "" || i < len(names)-1 {
			b.WriteByte('|')
			b.WriteString(n.JoinPhrase)
		}
	}
	return b.String()
}

// CreditDisplay renders a credit as it reads: each credited name followed by its join
// phrase.
func CreditDisplay(names []CreditName) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n.Name)
		b.WriteString(n.JoinPhrase)
	}
	return b.String()
}

// Edition is one edition of an album: a pressing, a reissue, the deluxe version. The
// migration's legacy edition has no MBID; MUS-30 re-keys it to a real one.
type Edition struct {
	ID             int64    `json:"id"`
	AlbumID        int64    `json:"album_id"`
	MBID           string   `json:"mbid,omitempty"`
	Title          string   `json:"title"`
	CreditID       int64    `json:"artist_credit_id,omitempty"`
	Status         string   `json:"status,omitempty"`
	ReleaseDate    string   `json:"release_date,omitempty"`
	Country        string   `json:"country,omitempty"`
	Label          string   `json:"label,omitempty"`
	CatalogNumber  string   `json:"catalog_number,omitempty"`
	Barcode        string   `json:"barcode,omitempty"`
	Packaging      string   `json:"packaging,omitempty"`
	Disambiguation string   `json:"disambiguation,omitempty"`
	MediaSummary   string   `json:"media_summary,omitempty"`
	TrackCount     int      `json:"track_count"`
	DiscCount      int      `json:"disc_count"`
	Source         string   `json:"source"`
	Legacy         bool     `json:"legacy,omitempty"`
	TracksFetched  bool     `json:"tracks_fetched"`
	FetchedAt      string   `json:"fetched_at,omitempty"`
	StaleSince     string   `json:"stale_since,omitempty"`
	Media          []Medium `json:"media,omitempty"` // filled by the full edition read only
	// Target marks the album's target edition in an editions list.
	Target bool `json:"target,omitempty"`
}

// Medium is one disc of an edition.
type Medium struct {
	ID         int64          `json:"id"`
	ReleaseID  int64          `json:"release_id"`
	Position   int            `json:"position"` // disc number, from 1
	Format     string         `json:"format,omitempty"`
	Title      string         `json:"title,omitempty"`
	TrackCount int            `json:"track_count"`
	Tracks     []ReleaseTrack `json:"tracks,omitempty"`
}

// Recording is the distinct audio; one recording appears on many releases.
type Recording struct {
	ID             int64  `json:"id"`
	MBID           string `json:"mbid,omitempty"`
	Title          string `json:"title"`
	CreditID       int64  `json:"artist_credit_id,omitempty"`
	LengthMs       int    `json:"length_ms,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
}

// ReleaseTrack is a recording at a position on one disc of one edition, with its file.
type ReleaseTrack struct {
	ID            int64      `json:"id"`
	MediumID      int64      `json:"medium_id"`
	RecordingID   int64      `json:"recording_id"`
	RecordingMBID string     `json:"recording_mbid,omitempty"`
	MBID          string     `json:"mbid,omitempty"` // the release track's own MBID
	Position      int        `json:"position"`
	Number        string     `json:"number,omitempty"`
	Title         string     `json:"title"`
	CreditID      int64      `json:"artist_credit_id,omitempty"` // 0: the release's credit
	LengthMs      int        `json:"length_ms,omitempty"`
	Monitored     bool       `json:"monitored"`
	File          *TrackFile `json:"file,omitempty"`
}

// TrackFile is one audio file on disk, measured. ReleaseTrackID 0 is an album extra: a
// file that belongs to the album but to no track of its target edition, kept and listed,
// never deleted.
type TrackFile struct {
	ID             int64   `json:"id"`
	AlbumID        int64   `json:"album_id"`
	ReleaseTrackID int64   `json:"release_track_id,omitempty"`
	Path           string  `json:"path"`
	SizeBytes      int64   `json:"size_bytes"`
	MtimeUnix      int64   `json:"mtime_unix,omitempty"`
	Container      string  `json:"container,omitempty"`
	Codec          string  `json:"codec,omitempty"`
	Lossless       bool    `json:"lossless"`
	BitrateKbps    int     `json:"bitrate_kbps,omitempty"`
	BitrateMode    string  `json:"bitrate_mode,omitempty"`
	SampleRateHz   int     `json:"sample_rate_hz,omitempty"`
	BitDepth       int     `json:"bit_depth,omitempty"`
	Channels       int     `json:"channels,omitempty"`
	DurationMs     int     `json:"duration_ms,omitempty"`
	Quality        Quality `json:"quality,omitempty"` // the measured tier, or an old scan's extension tag
	ProbedAt       string  `json:"probed_at,omitempty"`
	TagState       string  `json:"tag_state,omitempty"`
	SourceRelease  string  `json:"source_release,omitempty"`
	DownloadHash   string  `json:"-"`
	MissingSince   string  `json:"missing_since,omitempty"`
	AddedAt        string  `json:"added_at,omitempty"`
}

// ExternalID is an identifier from another source (or a merged-away MBID) for one music
// entity.
type ExternalID struct {
	EntityType string `json:"entity_type"` // artist | album | release | recording | release_track
	EntityID   int64  `json:"entity_id"`
	Source     string `json:"source"` // musicbrainz_old | isrc | upc | deezer | discogs_master | discogs_release | acoustid | wikidata
	Value      string `json:"value"`
}

// Tag states of a TrackFile.
const (
	TagStateAsFound = "as_found" // the tags the file arrived with
	TagStateHouse   = "house"    // rewritten in the house style (MUS-33)
	TagStateFailed  = "failed"
)

// splitAlbumType turns the stored display type into MusicBrainz's primary and secondary
// types: the primary types map across, and the secondary ones (only ever stored as the
// whole type) are an Album with that secondary type, as MusicBrainz files them. The SQL in
// migration 0339 does the same for existing rows.
func splitAlbumType(t string) (primary, secondary string) {
	switch t {
	case "Live", "Compilation", "Soundtrack", "Remix":
		return "Album", t
	case "":
		return "Album", ""
	}
	return t, ""
}
