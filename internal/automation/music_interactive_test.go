package automation

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// MUS-17: interactive search judges each release exactly as the sweep would and says why
// it wouldn't take one; the recommended release is the sweep's pick.
func TestRankAlbumReleasesReasons(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer", "Kid A"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	// Track lengths, so the size check has a listing to judge by (8 minutes).
	h.fake.mu.Lock()
	h.fake.tracks["artist-Radiohead-rg-0"] = []metadata.TrackResult{
		{Title: "One", Disc: 1, Number: 1, DurationSec: 240}, {Title: "Two", Disc: 1, Number: 2, DurationSec: 240},
	}
	h.fake.mu.Unlock()
	h.c.addBlockMusic(h.ctx, al.ID, "Radiohead - OK Computer (1997) [ALAC]", "Test", "bad rip")
	video := rel("Radiohead - OK Computer Live 1998 1080p BluRay FLAC 2.0 x264")
	video.SizeBytes = 6 << 30
	tiny := rel("Radiohead - OK Computer (1997) [FLAC] fake")
	tiny.SizeBytes = 5 << 20
	movieCat := rel("Radiohead - OK Computer (1997) [FLAC] web")
	movieCat.Categories = []int{2040}
	h.releases = found(
		rel("Radiohead - OK Computer (1997) [MP3 320]"),
		rel("Radiohead - OK Computer (1997) [FLAC]"),
		rel("Radiohead - Kid A (2000) [FLAC]"),
		rel("Muse - Absolution (2003) [FLAC]"),
		rel("Radiohead - OK Computer (1997) [ALAC]"),
		rel("Radiohead - OK Computer (1997) [MP3 128]"),
		rel("Radiohead - Discography 1993-2016 [FLAC]"),
		video, tiny, movieCat,
	)
	list, err := h.c.RankAlbumReleases(h.ctx, al.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Radiohead - OK Computer (1997) [FLAC]":                        "",
		"Radiohead - OK Computer (1997) [MP3 320]":                     "",
		"Radiohead - Kid A (2000) [FLAC]":                              "doesn't name this album",
		"Muse - Absolution (2003) [FLAC]":                              "doesn't name Radiohead",
		"Radiohead - OK Computer (1997) [ALAC]":                        "blocklisted for this album",
		"Radiohead - OK Computer (1997) [MP3 128]":                     "MP3-128 isn't on the Lossless, or the best MP3 ladder",
		"Radiohead - Discography 1993-2016 [FLAC]":                     "a discography, not this one album",
		"Radiohead - OK Computer Live 1998 1080p BluRay FLAC 2.0 x264": "looks like a video release",
		"Radiohead - OK Computer (1997) [FLAC] fake":                   "5 MB is too small for 8 minutes of FLAC",
		"Radiohead - OK Computer (1997) [FLAC] web":                    "listed as video",
	}
	if len(list.Releases) != len(want) {
		t.Fatalf("%d releases, want %d", len(list.Releases), len(want))
	}
	for _, r := range list.Releases {
		why, ok := want[r.Title]
		if !ok {
			t.Fatalf("unexpected release %q", r.Title)
		}
		if r.Eligible != (why == "") || r.RejectReason != why {
			t.Errorf("%s: eligible=%v reason=%q, want %q", r.Title, r.Eligible, r.RejectReason, why)
		}
		if r.Recommended != (r.Title == "Radiohead - OK Computer (1997) [FLAC]") {
			t.Errorf("%s: recommended=%v", r.Title, r.Recommended)
		}
		if r.Title == "Radiohead - OK Computer (1997) [ALAC]" && !r.Blocklisted {
			t.Errorf("the blocklisted release isn't flagged")
		}
	}
	if list.Releases[0].Title != "Radiohead - OK Computer (1997) [FLAC]" || len(list.Why) == 0 {
		t.Errorf("first %q why %v, want the pick first with its reasons", list.Releases[0].Title, list.Why)
	}
	if len(h.grabs) != 0 {
		t.Fatalf("interactive search grabbed %v", h.grabs)
	}
	// What the sweep then does agrees: it grabs the recommended release.
	out := h.c.grabAlbum(h.ctx, a, al)
	if out.Code != outcomeGrabbed || out.Release != "Radiohead - OK Computer (1997) [FLAC]" {
		t.Errorf("the sweep took %+v, want the recommended FLAC", out)
	}
}

// On a complete album the reasons are an upgrade's: no better than what's held.
func TestRankAlbumReleasesOnAHeldAlbum(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer"}, nil, "1997-05-21")
	al := holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
	h.releases = found(rel("Radiohead - OK Computer (1997) [MP3 V0]"), rel("Radiohead - OK Computer (1997) [FLAC]"))
	list, err := h.c.RankAlbumReleases(h.ctx, al.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list.Releases {
		switch r.Title {
		case "Radiohead - OK Computer (1997) [MP3 V0]":
			if r.Eligible || r.RejectReason != "no better than the MP3-320 you have" {
				t.Errorf("V0: eligible=%v reason=%q", r.Eligible, r.RejectReason)
			}
		case "Radiohead - OK Computer (1997) [FLAC]":
			if !r.Eligible || !r.Recommended {
				t.Errorf("FLAC: %+v, want the recommended upgrade", r)
			}
		}
	}
	if !strings.Contains(strings.Join(list.Why, " "), "Better than the MP3-320") {
		t.Errorf("why = %v", list.Why)
	}
}

// A release a person grabbed by hand whose name doesn't spell the album still imports
// into that album: the matcher checks the files against the album it was grabbed for.
func TestManualAlbumGrabImportsIntoTheAlbum(t *testing.T) {
	r := newMatchRig(t, false, nil)
	files := trackFileNames(okcTitles)
	name := "VA - 1997 Britpop Upload [FLAC]"
	dl := filepath.Join(t.TempDir(), name)
	for _, f := range files {
		blobTrack(t, dl, f, 1)
	}
	r.tagAll(files, okcTitles)
	hash := hashFor(name)
	r.h.c.musicGrabFn = func(context.Context, string, string, string, string) (string, error) { return hash, nil }
	if err := r.h.c.GrabForAlbum(r.h.ctx, r.album.ID, "Fake", "magnet:?xt=x", name); err != nil {
		t.Fatal(err)
	}
	var manual int
	if err := r.h.c.db.QueryRow(`SELECT manual FROM grabs WHERE info_hash = ?`, hash).Scan(&manual); err != nil || manual != 1 {
		t.Fatalf("manual = %d (%v), want the grab marked as a person's choice", manual, err)
	}
	if n := artistEvents(t, r.svc, r.artist.ID, "by hand"); n != 1 {
		t.Errorf("history lines about the grab = %d, want 1", n)
	}
	r.h.qbit.mu.Lock()
	r.h.qbit.torrents = []map[string]any{{
		"hash": hash, "name": name, "state": "uploading", "progress": 1.0,
		"size": 600 << 10, "amount_left": 0, "category": download.CategoryMusic, "content_path": dl,
	}}
	r.h.qbit.mu.Unlock()
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID); al.HaveTracks != len(okcTitles) {
		t.Fatalf("have %d, want all %d placed on the album it was grabbed for", al.HaveTracks, len(okcTitles))
	}
}
