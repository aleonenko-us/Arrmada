package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mbStub(t *testing.T, h http.HandlerFunc) *MusicBrainz {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := NewMusicBrainz(nil, nil)
	m.http = srv.Client()
	m.base = srv.URL
	m.art = "https://art.example"
	// The real 1.1s pacing would make the suite crawl; the pacing itself is not what these
	// tests are checking (mblimit_test.go is). A gate of its own, so no other test's
	// pacing leaks in.
	m.minInterval = 0
	m.gate = newMBLimiter()
	return m
}

// MusicBrainz answers 503 for both rate limiting and ordinary load — often enough that the
// first live run of this client hit one on its second call. It must retry, not fail.
func TestMusicBrainzRetriesServiceUnavailable(t *testing.T) {
	var calls int
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"id":"abc","name":"Radiohead","sort-name":"Radiohead"}`)
	})
	d, err := m.GetArtist(context.Background(), "abc")
	if err != nil {
		t.Fatalf("a 503 should be retried, got %v", err)
	}
	if d.Name != "Radiohead" {
		t.Errorf("name = %q", d.Name)
	}
	if calls != 3 {
		t.Errorf("expected 2 failures then a success, got %d calls", calls)
	}
}

// Every request must carry a descriptive User-Agent — MusicBrainz blocks those that don't.
func TestMusicBrainzSendsUserAgent(t *testing.T) {
	var ua string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, `{"artists":[]}`)
	})
	_, _ = m.SearchArtists(context.Background(), "x")
	if !strings.Contains(ua, "Arrmada") || !strings.Contains(ua, "http") {
		t.Errorf("User-Agent = %q, want an app name and a contact URL", ua)
	}
}

// The album browse asks for albums and EPs exactly as it always has (so cached answers
// stay valid) and returns every one with its MusicBrainz types: the music service picks
// what to list from the artist's release types (MUS-14), so a live album comes back
// labelled Live with its primary type kept.
func TestArtistAlbumsCarriesTypes(t *testing.T) {
	var query string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("type")
		_, _ = io.WriteString(w, `{"release-groups":[
		  {"id":"rg1","title":"OK Computer","primary-type":"Album","first-release-date":"1997-05-21"},
		  {"id":"rg2","title":"Live at the BBC","primary-type":"Album","secondary-types":["Live"],"first-release-date":"1996-01-01"},
		  {"id":"rg3","title":"Airbag EP","primary-type":"EP","first-release-date":"1998-03-01"}
		]}`)
	})
	albums, err := m.ArtistAlbums(context.Background(), "artist")
	if err != nil {
		t.Fatal(err)
	}
	if query != "album|ep" {
		t.Errorf("browse asked for type %q, want album|ep", query)
	}
	if len(albums) != 3 {
		t.Fatalf("want all three release groups, got %d: %+v", len(albums), albums)
	}
	// Newest first.
	if albums[0].Title != "Airbag EP" || albums[0].Year != 1998 || albums[0].PrimaryType != "EP" {
		t.Errorf("expected the 1998 EP first, got %+v", albums[0])
	}
	if albums[1].Year != 1997 {
		t.Errorf("year not parsed from the release date: %+v", albums[1])
	}
	if albums[1].CoverURL != "https://art.example/release-group/rg1/front-500" {
		t.Errorf("cover URL = %q", albums[1].CoverURL)
	}
	live := albums[2]
	if live.Type != "Live" || live.PrimaryType != "Album" || len(live.SecondaryTypes) != 1 || live.SecondaryTypes[0] != "Live" {
		t.Errorf("live album = %+v, want label Live, primary Album, secondary [Live]", live)
	}
}

// Singles, broadcasts and "other" are their own browse, asked only when wanted.
func TestArtistOtherReleaseGroupsQuery(t *testing.T) {
	var query string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("type")
		_, _ = io.WriteString(w, `{"release-groups":[{"id":"s1","title":"Creep","primary-type":"Single","first-release-date":"1992-09-21"}]}`)
	})
	got, err := m.ArtistOtherReleaseGroups(context.Background(), "artist")
	if err != nil || len(got) != 1 || got[0].Type != "Single" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if query != "single|broadcast|other" {
		t.Errorf("browse asked for type %q, want single|broadcast|other", query)
	}
}

// A merged release group answers under its new MBID; a removed one is "not found", which
// is not the same as MusicBrainz not answering.
func TestReleaseGroupLookup(t *testing.T) {
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release-group/old":
			_, _ = io.WriteString(w, `{"id":"new","title":"OK Computer","primary-type":"Album"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"Not Found"}`)
		}
	})
	rg, err := m.ReleaseGroup(context.Background(), "old")
	if err != nil || rg.MBID != "new" {
		t.Fatalf("merged lookup = %+v, %v; want the new MBID", rg, err)
	}
	if _, err := m.ReleaseGroup(context.Background(), "gone"); !IsNotFound(err) {
		t.Errorf("removed lookup err = %v, want not found", err)
	}
}

// A release group holds many releases. Taking the deluxe reissue would mark its bonus
// tracks missing forever on an otherwise complete album, so the EARLIEST official release
// is the one to read a listing from.
func TestAlbumTracksPicksEarliestOfficialRelease(t *testing.T) {
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/release/"):
			if !strings.HasSuffix(r.URL.Path, "/original") {
				t.Errorf("read the listing from %q, want the earliest official release", r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"media":[{"position":1,"tracks":[
			  {"id":"t1","title":"Airbag","number":"1","position":1,"length":284000,"recording":{"id":"rec-airbag"}},
			  {"id":"t2","title":"Paranoid Android","position":2,"length":383000}]}]}`)
		default:
			_, _ = io.WriteString(w, `{"releases":[
			  {"id":"deluxe","date":"2017-06-23","status":"Official"},
			  {"id":"original","date":"1997-05-21","status":"Official"},
			  {"id":"boot","date":"1996-01-01","status":"Bootleg"}
			]}`)
		}
	})
	tracks, err := m.AlbumTracks(context.Background(), "rg1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	if tracks[0].Title != "Airbag" || tracks[0].Disc != 1 || tracks[0].Number != 1 {
		t.Errorf("track 1 = %+v", tracks[0])
	}
	// The release track's id, the recording's and the release's, all from the one answer.
	if tracks[0].MBID != "t1" || tracks[0].RecordingMBID != "rec-airbag" || tracks[0].ReleaseMBID != "original" {
		t.Errorf("track 1 ids = %q / %q / %q", tracks[0].MBID, tracks[0].RecordingMBID, tracks[0].ReleaseMBID)
	}
	if tracks[1].DurationSec != 383 {
		t.Errorf("duration should be seconds, got %d", tracks[1].DurationSec)
	}
}

// An album with no official release yet (an announced-but-unreleased record) is an ordinary
// answer, not a failure.
func TestAlbumTracksWithNoOfficialRelease(t *testing.T) {
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"releases":[{"id":"b","date":"2020-01-01","status":"Bootleg"}]}`)
	})
	tracks, err := m.AlbumTracks(context.Background(), "rg")
	if err != nil || len(tracks) != 0 {
		t.Errorf("want (nil, nil), got %+v / %v", tracks, err)
	}
}
