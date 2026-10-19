package metadata

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A release browse page as MusicBrainz sends it (trimmed to the fields read).
const browseJSON = `{"release-count": 2, "release-offset": 0, "releases": [
 {"id": "rel-gb", "title": "OK Computer", "status": "Official", "date": "1997-05-21", "country": "GB",
  "barcode": "724385522925", "packaging": "Jewel Case", "disambiguation": "",
  "artist-credit": [{"name": "Radiohead", "joinphrase": "", "artist": {"id": "mb-rh", "name": "Radiohead", "sort-name": "Radiohead"}}],
  "label-info": [{"catalog-number": "NODATA 02", "label": {"name": "Parlophone"}}],
  "media": [{"position": 1, "format": "CD", "track-count": 12}]},
 {"id": "rel-dlx", "title": "OK Computer", "status": "Official", "date": "2009-03-24", "country": "XE",
  "disambiguation": "Collector's Edition",
  "media": [{"position": 1, "format": "CD", "track-count": 12}, {"position": 2, "format": "CD", "track-count": 14}, {"position": 3, "format": "DVD-Video", "track-count": 9}]}
]}`

const releaseJSON = `{"id": "rel-gb", "title": "OK Computer", "status": "Official", "date": "1997-05-21", "country": "GB",
 "release-group": {"id": "rg-okc"},
 "artist-credit": [{"name": "Radiohead", "joinphrase": "", "artist": {"id": "mb-rh", "name": "Radiohead"}}],
 "media": [{"position": 1, "format": "CD", "track-count": 2, "tracks": [
   {"id": "rt-1", "number": "1", "position": 1, "title": "Airbag", "length": 284000,
    "recording": {"id": "rec-1", "title": "Airbag", "length": 284200, "isrcs": ["GBAYE9700245"]}},
   {"id": "rt-2", "number": "A2", "position": 2, "title": "Paranoid Android", "length": 0,
    "artist-credit": [{"name": "Radiohead", "joinphrase": " feat. ", "artist": {"id": "mb-rh", "name": "Radiohead"}},
                      {"name": "A Guest", "joinphrase": "", "artist": {"id": "mb-guest", "name": "Guest Person"}}],
    "recording": {"id": "rec-2", "title": "Paranoid Android", "length": 383000}}
 ]}]}`

func TestAlbumReleasesParsesBrowse(t *testing.T) {
	var query string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, browseJSON)
	})
	rels, err := m.AlbumReleases(context.Background(), "rg-okc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "release-group=rg-okc") || !strings.Contains(query, "media") || !strings.Contains(query, "limit=100") {
		t.Errorf("query %q, want a browse of the group with media, 100 a page", query)
	}
	if len(rels) != 2 {
		t.Fatalf("releases = %d, want 2", len(rels))
	}
	gb := rels[0]
	if gb.Label != "Parlophone" || gb.CatalogNumber != "NODATA 02" || gb.Barcode != "724385522925" || gb.Country != "GB" {
		t.Errorf("GB release %+v", gb)
	}
	if gb.TrackCount() != 12 || gb.MediaSummary() != "CD" || len(gb.Credit) != 1 || gb.Credit[0].ArtistMBID != "mb-rh" {
		t.Errorf("GB release counts/credit: %d %q %+v", gb.TrackCount(), gb.MediaSummary(), gb.Credit)
	}
	if got := rels[1].MediaSummary(); got != "2×CD + DVD-Video" {
		t.Errorf("deluxe media summary %q", got)
	}
	if rels[1].TrackCount() != 35 {
		t.Errorf("deluxe tracks %d", rels[1].TrackCount())
	}
}

func TestReleaseTracksParsesRelease(t *testing.T) {
	var path, query string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		_, _ = io.WriteString(w, releaseJSON)
	})
	d, err := m.ReleaseTracks(context.Background(), "rel-gb")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/release/rel-gb") || !strings.Contains(query, "recordings") || !strings.Contains(query, "isrcs") {
		t.Errorf("asked %s?%s", path, query)
	}
	if d.ReleaseGroupMBID != "rg-okc" || len(d.Media) != 1 || len(d.Media[0].Tracks) != 2 {
		t.Fatalf("detail %+v", d)
	}
	a, b := d.Media[0].Tracks[0], d.Media[0].Tracks[1]
	if a.MBID != "rt-1" || a.RecordingMBID != "rec-1" || a.LengthMs != 284000 || len(a.ISRCs) != 1 {
		t.Errorf("track 1 %+v", a)
	}
	if b.Number != "A2" || b.LengthMs != 383000 {
		t.Errorf("track 2 %+v (length falls back to the recording's)", b)
	}
	if len(b.Credit) != 2 || b.Credit[0].JoinPhrase != " feat. " || b.Credit[1].Name != "A Guest" || b.Credit[1].ArtistName != "Guest Person" {
		t.Errorf("track 2 credit %+v", b.Credit)
	}
}
