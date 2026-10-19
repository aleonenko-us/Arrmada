package metadata

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Editions (MUS-30): every release of a release group, and the full track list of the
// ones that matter (the album's target edition, and the matcher's candidates). Both go
// through the same cache and limiter as every other MusicBrainz question.

// EditionProvider is the editions part of the MusicBrainz client. It is a separate
// interface from MusicProvider so stand-ins that only answer listings keep working: the
// music service falls back to the single-listing path without it.
type EditionProvider interface {
	// AlbumReleases lists every release of a release group, summaries only (one paged
	// browse call; no track lists).
	AlbumReleases(ctx context.Context, releaseGroupMBID string) ([]ReleaseSummary, error)
	// ReleaseTracks reads one release in full: its discs and tracks with recordings,
	// credits and ISRCs.
	ReleaseTracks(ctx context.Context, releaseMBID string) (ReleaseDetail, error)
}

// CreditPart is one artist of a MusicBrainz artist credit, as credited there.
type CreditPart struct {
	ArtistMBID string `json:"artist_mbid"`
	ArtistName string `json:"artist_name"` // the artist's own name
	SortName   string `json:"sort_name,omitempty"`
	Name       string `json:"name"` // as credited here
	JoinPhrase string `json:"join_phrase,omitempty"`
}

// ReleaseMedium is one disc of a release summary or detail.
type ReleaseMedium struct {
	Position   int            `json:"position"`
	Format     string         `json:"format,omitempty"`
	Title      string         `json:"title,omitempty"`
	TrackCount int            `json:"track_count"`
	Tracks     []ReleaseTrack `json:"tracks,omitempty"` // ReleaseTracks only
}

// ReleaseTrack is one track of a release, from ReleaseTracks.
type ReleaseTrack struct {
	MBID          string       `json:"mbid"` // the release track's own id
	RecordingMBID string       `json:"recording_mbid"`
	Position      int          `json:"position"`
	Number        string       `json:"number,omitempty"` // as printed ("A1")
	Title         string       `json:"title"`
	LengthMs      int          `json:"length_ms,omitempty"`
	Credit        []CreditPart `json:"credit,omitempty"`
	ISRCs         []string     `json:"isrcs,omitempty"`
}

// ReleaseSummary is one release of a release group, without its tracks.
type ReleaseSummary struct {
	MBID           string          `json:"mbid"`
	Title          string          `json:"title"`
	Status         string          `json:"status,omitempty"`
	Date           string          `json:"date,omitempty"`
	Country        string          `json:"country,omitempty"`
	Label          string          `json:"label,omitempty"`
	CatalogNumber  string          `json:"catalog_number,omitempty"`
	Barcode        string          `json:"barcode,omitempty"`
	Packaging      string          `json:"packaging,omitempty"`
	Disambiguation string          `json:"disambiguation,omitempty"`
	Credit         []CreditPart    `json:"credit,omitempty"`
	Media          []ReleaseMedium `json:"media,omitempty"`
}

// TrackCount is every track on every disc.
func (r ReleaseSummary) TrackCount() int {
	n := 0
	for _, m := range r.Media {
		n += m.TrackCount
	}
	return n
}

// MediaSummary describes the discs the way people read them: "2×CD", "Digital Media",
// "CD + DVD-Video".
func (r ReleaseSummary) MediaSummary() string {
	type run struct {
		format string
		n      int
	}
	var runs []run
	for _, m := range r.Media {
		f := m.Format
		if f == "" {
			f = "(unknown)"
		}
		if len(runs) > 0 && runs[len(runs)-1].format == f {
			runs[len(runs)-1].n++
			continue
		}
		runs = append(runs, run{f, 1})
	}
	parts := make([]string, 0, len(runs))
	for _, r := range runs {
		if r.n > 1 {
			parts = append(parts, strconv.Itoa(r.n)+"×"+r.format)
		} else {
			parts = append(parts, r.format)
		}
	}
	return strings.Join(parts, " + ")
}

// ReleaseDetail is one release in full.
type ReleaseDetail struct {
	ReleaseSummary
	ReleaseGroupMBID string `json:"release_group_mbid,omitempty"`
}

// mbCredit is MusicBrainz's artist-credit list.
type mbCredit []struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	} `json:"artist"`
}

func (c mbCredit) parts() []CreditPart {
	if len(c) == 0 {
		return nil
	}
	out := make([]CreditPart, 0, len(c))
	for _, n := range c {
		name := n.Name
		if name == "" {
			name = n.Artist.Name
		}
		out = append(out, CreditPart{
			ArtistMBID: n.Artist.ID, ArtistName: n.Artist.Name, SortName: n.Artist.SortName,
			Name: name, JoinPhrase: n.JoinPhrase,
		})
	}
	return out
}

// mbRelease is the part of a MusicBrainz release answer editions read.
type mbRelease struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Date           string   `json:"date"`
	Country        string   `json:"country"`
	Barcode        string   `json:"barcode"`
	Packaging      string   `json:"packaging"`
	Disambiguation string   `json:"disambiguation"`
	ArtistCredit   mbCredit `json:"artist-credit"`
	LabelInfo      []struct {
		CatalogNumber string `json:"catalog-number"`
		Label         *struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"label-info"`
	ReleaseGroup *struct {
		ID string `json:"id"`
	} `json:"release-group"`
	Media []struct {
		Position   int    `json:"position"`
		Format     string `json:"format"`
		Title      string `json:"title"`
		TrackCount int    `json:"track-count"`
		Tracks     []struct {
			ID           string   `json:"id"`
			Title        string   `json:"title"`
			Number       string   `json:"number"`
			Position     int      `json:"position"`
			Length       int      `json:"length"`
			ArtistCredit mbCredit `json:"artist-credit"`
			Recording    struct {
				ID           string   `json:"id"`
				Title        string   `json:"title"`
				Length       int      `json:"length"`
				ISRCs        []string `json:"isrcs"`
				ArtistCredit mbCredit `json:"artist-credit"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

func (r mbRelease) summary() ReleaseSummary {
	s := ReleaseSummary{
		MBID: r.ID, Title: r.Title, Status: r.Status, Date: r.Date, Country: r.Country,
		Barcode: r.Barcode, Packaging: r.Packaging, Disambiguation: r.Disambiguation,
		Credit: r.ArtistCredit.parts(),
	}
	for _, li := range r.LabelInfo {
		if s.Label == "" && li.Label != nil {
			s.Label = li.Label.Name
		}
		if s.CatalogNumber == "" {
			s.CatalogNumber = strings.TrimSpace(li.CatalogNumber)
		}
	}
	for i, m := range r.Media {
		pos := m.Position
		if pos < 1 {
			pos = i + 1
		}
		count := m.TrackCount
		if count == 0 {
			count = len(m.Tracks)
		}
		md := ReleaseMedium{Position: pos, Format: m.Format, Title: m.Title, TrackCount: count}
		for _, t := range m.Tracks {
			length := t.Length
			if length == 0 {
				length = t.Recording.Length
			}
			credit := t.ArtistCredit.parts()
			if len(credit) == 0 {
				credit = t.Recording.ArtistCredit.parts()
			}
			md.Tracks = append(md.Tracks, ReleaseTrack{
				MBID: t.ID, RecordingMBID: t.Recording.ID, Position: t.Position, Number: t.Number,
				Title: t.Title, LengthMs: length, Credit: credit, ISRCs: t.Recording.ISRCs,
			})
		}
		s.Media = append(s.Media, md)
	}
	return s
}

// mbReleasePage is how many releases one browse call asks for (MusicBrainz's maximum).
const mbReleasePage = 100

// mbReleasePages bounds the browse: 20 pages is 2,000 releases, past any real release
// group (the biggest have a few hundred).
const mbReleasePages = 20

// AlbumReleases lists every release of a release group, paged 100 at a time, with labels,
// credits and disc formats but no tracks.
func (m *MusicBrainz) AlbumReleases(ctx context.Context, releaseGroupMBID string) ([]ReleaseSummary, error) {
	if strings.TrimSpace(releaseGroupMBID) == "" {
		return nil, fmt.Errorf("musicbrainz: empty release-group id")
	}
	var out []ReleaseSummary
	for page := 0; page < mbReleasePages; page++ {
		var body struct {
			Releases []mbRelease `json:"releases"`
			Count    int         `json:"release-count"`
		}
		q := url.Values{
			"release-group": {releaseGroupMBID}, "inc": {"artist-credits+labels+media"},
			"limit": {strconv.Itoa(mbReleasePage)}, "offset": {strconv.Itoa(page * mbReleasePage)},
		}
		if err := m.lookup(ctx, mbKindReleases, "/release", q, &body); err != nil {
			return nil, err
		}
		for _, r := range body.Releases {
			out = append(out, r.summary())
		}
		if len(body.Releases) < mbReleasePage || len(out) >= body.Count {
			break
		}
	}
	return out, nil
}

// ReleaseTracks reads one release with its discs, tracks, recordings, credits and ISRCs.
func (m *MusicBrainz) ReleaseTracks(ctx context.Context, releaseMBID string) (ReleaseDetail, error) {
	if strings.TrimSpace(releaseMBID) == "" {
		return ReleaseDetail{}, fmt.Errorf("musicbrainz: empty release id")
	}
	var r mbRelease
	err := m.lookup(ctx, mbKindRelease, "/release/"+url.PathEscape(releaseMBID),
		url.Values{"inc": {"artist-credits+isrcs+labels+recordings+release-groups"}}, &r)
	if errors.Is(err, errNotFound) {
		return ReleaseDetail{}, fmt.Errorf("musicbrainz: no release %s", releaseMBID)
	}
	if err != nil {
		return ReleaseDetail{}, err
	}
	d := ReleaseDetail{ReleaseSummary: r.summary()}
	if r.ReleaseGroup != nil {
		d.ReleaseGroupMBID = r.ReleaseGroup.ID
	}
	return d, nil
}

var _ EditionProvider = (*MusicBrainz)(nil)
