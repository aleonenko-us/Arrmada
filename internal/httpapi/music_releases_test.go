package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
)

// MUS-17: an album's interactive search shows every release with whether it would be
// grabbed and why not, carries tokens and never a download link, and grabs by token into
// the album, marked as a person's choice.
func TestAlbumInteractiveSearchAndGrab(t *testing.T) {
	s, mt, _, _, _, al := musicSearchServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, other := s.user(t, "mgr2@example.com", auth.RoleManager)

	path := fmt.Sprintf("/api/v1/music/albums/%d/releases", al.ID)
	rec := s.do("GET", path, mgr)
	rels := releasesOf(t, rec, true)
	noSecrets(t, path, rec.Body.String())
	byTitle := map[string]automation.RankedRelease{}
	for _, r := range rels {
		byTitle[r.Title] = r
	}
	want := map[string]string{ // title -> the reason it's turned down ("" = eligible)
		"Radiohead - OK Computer (1997) [FLAC]":                        "",
		"Radiohead - OK Computer (1997) [MP3 320]":                     "",
		"Radiohead - OK Computer Live 1998 1080p BluRay FLAC 2.0 x264": "looks like a video release",
		"Radiohead - OK Computer (1997) [FLAC] tiny":                   "20 MB is too small for 48 minutes of FLAC",
		"Muse - Absolution (2003) [FLAC]":                              "doesn't name Radiohead",
		"Radiohead - OK Computer (1997) [MP3 128]":                     "MP3-128 isn't on the Lossless or 320 ladder",
	}
	for title, why := range want {
		r, ok := byTitle[title]
		if !ok {
			t.Fatalf("no release %q in %+v", title, rels)
		}
		if r.Eligible != (why == "") || r.RejectReason != why {
			t.Errorf("%s: eligible=%v reason=%q, want reason %q", title, r.Eligible, r.RejectReason, why)
		}
	}
	if !byTitle["Radiohead - OK Computer (1997) [FLAC]"].Recommended || byTitle["Radiohead - OK Computer (1997) [MP3 320]"].Recommended {
		t.Errorf("the FLAC should be the recommended pick: %+v", rels)
	}
	if rels[0].Title != "Radiohead - OK Computer (1997) [FLAC]" {
		t.Errorf("first row = %q, want the pick first", rels[0].Title)
	}
	if f := mt.fetches(); len(f) != 0 {
		t.Fatalf("searching fetched downloads: %v", f)
	}

	// A token is only good for this album and this person.
	tok := tokenFor(t, rels, "Radiohead - OK Computer Live 1998 1080p BluRay FLAC 2.0 x264")
	grabPath := fmt.Sprintf("/api/v1/music/albums/%d/grab", al.ID)
	if rec := s.doJSON("POST", grabPath, other, `{"token":"`+tok+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("another person's token: HTTP %d, want 400", rec.Code)
	}
	if rec := s.doJSON("POST", fmt.Sprintf("/api/v1/music/albums/%d/grab", al.ID+100), mgr, `{"token":"`+tok+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("another album: HTTP %d, want 400", rec.Code)
	}
	// A person may grab what the automatic search wouldn't: their choice.
	rec = s.doJSON("POST", grabPath, mgr, `{"token":"`+tok+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grab: HTTP %d: %s", rec.Code, rec.Body)
	}
	noSecrets(t, "grab", rec.Body.String())
	// The same release again while it downloads: refused, nothing fetched.
	if rec := s.doJSON("POST", grabPath, mgr, `{"token":"`+tok+`"}`); rec.Code != http.StatusConflict {
		t.Errorf("a repeat grab: HTTP %d, want 409: %s", rec.Code, rec.Body)
	}
	if f := mt.fetches(); len(f) != 1 || !strings.HasPrefix(f[0], "/dl/3?apikey=") {
		t.Fatalf("fetched %v, want the stored link of release 3", f)
	}
	var mediaType string
	var id int64
	var manual int
	if err := s.st.DB().QueryRow(`SELECT media_type, movie_id, manual FROM grabs ORDER BY id DESC LIMIT 1`).Scan(&mediaType, &id, &manual); err != nil {
		t.Fatal(err)
	}
	if mediaType != "music" || id != al.ID || manual != 1 {
		t.Errorf("grab row = %s/%d manual=%d, want music/%d manual=1", mediaType, id, manual, al.ID)
	}
}
