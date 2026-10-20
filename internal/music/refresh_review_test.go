package music

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The independent review of the refresh (2026-10-20), B1–B7 and the cheap P items: each
// case is a way a refresh could want something nobody asked for, or get stuck.

// failOnce fails the first album browse (an add whose pull fails), then answers.
type failOnce struct {
	*catalogProvider
	done bool
}

func (p *failOnce) ArtistAlbums(ctx context.Context, mbid string) ([]metadata.AlbumResult, error) {
	if !p.done {
		p.done = true
		return nil, errDown
	}
	return p.catalogProvider.ArtistAlbums(ctx, mbid)
}

// B1: an add whose album pull failed keeps its choice; the first refresh that can applies
// it exactly as the add would have, never as a first look wanting the last 90 days.
func TestAddChoiceSurvivesAFailedPull(t *testing.T) {
	for _, tc := range []struct {
		monitor string
		want    map[string]bool
	}{
		{MonitorFuture, map[string]bool{"rg-old": false, "rg-recent": false}},
		{MonitorLatest, map[string]bool{"rg-old": false, "rg-recent": true}},
		{MonitorNone, map[string]bool{"rg-old": false, "rg-recent": false}},
		{MonitorAll, map[string]bool{"rg-old": true, "rg-recent": true}},
	} {
		t.Run(tc.monitor, func(t *testing.T) {
			f := newRefreshFixture(t)
			f.p.main["mb-x"] = []metadata.AlbumResult{rg("rg-old", "Old", "Album", "2010-01-01"), rg("rg-recent", "Recent", "Album", "2026-09-01")}
			f.svc.meta = &failOnce{catalogProvider: f.p}
			a, err := f.svc.AddArtistWith(f.ctx, "mb-x", "", AddOptions{Monitored: true, Monitor: tc.monitor})
			if err != nil {
				t.Fatal(err)
			}
			if f.hasAlbum("rg-recent") {
				t.Fatal("the add pulled albums; the failure didn't happen")
			}
			res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
			if err != nil {
				t.Fatal(err)
			}
			for id, want := range tc.want {
				if al := f.album(t, id); al.Monitored != want {
					t.Errorf("%s monitored = %v, want %v (found %q)", id, al.Monitored, want, al.FoundAs)
				}
			}
			if len(res.Wanted) != 0 {
				t.Errorf("the add's albums came back as %d new releases to search", len(res.Wanted))
			}
			if got, _ := f.repo.GetArtist(f.ctx, a.ID); got.pendingAdd != "" {
				t.Errorf("pending add choice %q left behind", got.pendingAdd)
			}
			// From then on it's an ordinary refresh.
			f.p.main["mb-x"] = append(f.p.main["mb-x"], rg("rg-next", "Next", "Album", "2026-10-15"))
			if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
				t.Fatal(err)
			}
			wantNext := tc.monitor != MonitorNone
			if al := f.album(t, "rg-next"); al.Monitored != wantNext || al.FoundAs != FoundNew {
				t.Errorf("next release monitored=%v found=%q, want %v and new", al.Monitored, al.FoundAs, wantNext)
			}
		})
	}
}

// B2: after MusicBrainz merges the artist into an entry with a bigger catalogue, nothing
// of that catalogue is a new release just because the artist now reads it.
func TestArtistMergeListsTheTargetsCatalogue(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-dup"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-dup", "", true)
	if err != nil {
		t.Fatal(err)
	}
	f.p.artistID["mb-dup"] = "mb-canon"
	f.p.main["mb-canon"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01"),
		rg("rg-b", "B", "Album", "2026-09-01"), rg("rg-c", "C", "EP", "2026-10-01")}
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rg-b", "rg-c"} {
		if al := f.album(t, id); al.Monitored || al.FoundAs != FoundNotNew {
			t.Errorf("%s after the merge: monitored=%v found=%q, want listed as not new", id, al.Monitored, al.FoundAs)
		}
	}
	// The refresh after is ordinary again.
	f.p.main["mb-canon"] = append(f.p.main["mb-canon"], rg("rg-d", "D", "Album", "2026-10-18"))
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-d"); !al.Monitored {
		t.Error("a real new release after the merge isn't wanted")
	}
}

// B3: a person's choice beats the automatic singles check, and the check only acts while
// what asked for it still holds.
func TestSinglesCheckRespectsTheOwner(t *testing.T) {
	setup := func(t *testing.T) (*refreshFixture, Artist) {
		f := newRefreshFixture(t)
		singles := ReleaseTypes{Primary: []string{"Album", "Single"}}
		f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21")}
		f.p.tracks["rg-okc"] = []metadata.TrackResult{{Title: "Airbag", Disc: 1, Number: 1, RecordingMBID: "rec-airbag"}}
		a, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, ReleaseTypes: &singles})
		if err != nil {
			t.Fatal(err)
		}
		f.p.other["mb-rh"] = []metadata.AlbumResult{rg("rg-s", "Airbag", "Single", "2026-10-01"), rg("rg-s2", "Lift", "Single", "2026-10-02")}
		f.p.tracks["rg-s"] = []metadata.TrackResult{{Title: "Airbag", Disc: 1, Number: 1, RecordingMBID: "rec-airbag"}}
		f.p.tracks["rg-s2"] = []metadata.TrackResult{{Title: "Lift", Disc: 1, Number: 1, RecordingMBID: "rec-lift"}}
		if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
			t.Fatal(err)
		}
		if s := f.album(t, "rg-s"); s.FoundAs != FoundSinglePending {
			t.Fatalf("found=%q, want pending", s.FoundAs)
		}
		return f, a
	}
	always := func() bool { return true }

	t.Run("wanted by hand stays wanted", func(t *testing.T) {
		f, _ := setup(t)
		s := f.album(t, "rg-s")
		if err := f.svc.SetAlbumMonitored(f.ctx, s.ID, true); err != nil {
			t.Fatal(err)
		}
		if got := f.album(t, "rg-s"); got.FoundAs == FoundSinglePending {
			t.Error("wanting by hand left it pending")
		}
		if _, _, err := f.svc.CheckPendingSingles(f.ctx, 10, always); err != nil {
			t.Fatal(err)
		}
		if !f.album(t, "rg-s").Monitored {
			t.Errorf("the single the owner wanted by hand was unwanted by the check (found=%q)", f.album(t, "rg-s").FoundAs)
		}
	})
	t.Run("new releases switched to listed only", func(t *testing.T) {
		f, a := setup(t)
		if _, err := f.svc.SetOptions(f.ctx, a.ID, MonitorNewNone, a.ReleaseTypes); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.svc.CheckPendingSingles(f.ctx, 10, always); err != nil {
			t.Fatal(err)
		}
		if al := f.album(t, "rg-s2"); al.Monitored || al.FoundAs != FoundNew {
			t.Errorf("a pending single after new releases went list-only: monitored=%v found=%q", al.Monitored, al.FoundAs)
		}
	})
	t.Run("singles unticked", func(t *testing.T) {
		f, a := setup(t)
		albums := ReleaseTypes{Primary: []string{"Album"}}
		if _, err := f.svc.SetOptions(f.ctx, a.ID, "", &albums); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.svc.CheckPendingSingles(f.ctx, 10, always); err != nil {
			t.Fatal(err)
		}
		if al := f.album(t, "rg-s2"); al.Monitored || al.FoundAs != FoundNotNew {
			t.Errorf("a pending single after Single was unticked: monitored=%v found=%q", al.Monitored, al.FoundAs)
		}
	})
	t.Run("a person's apply is honoured with new releases list-only", func(t *testing.T) {
		f, a := setup(t)
		if _, err := f.svc.SetOptions(f.ctx, a.ID, MonitorNewNone, a.ReleaseTypes); err != nil {
			t.Fatal(err)
		}
		// The check settles the queued ones (listed only now), then the person asks.
		if _, _, err := f.svc.CheckPendingSingles(f.ctx, 10, always); err != nil {
			t.Fatal(err)
		}
		plan, err := f.svc.ApplyPreview(f.ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Singles) != 2 {
			t.Fatalf("plan singles = %+v, want both", plan.Singles)
		}
		if _, err := f.svc.ApplyOptions(f.ctx, a.ID, ApplyIDs{Want: briefIDs(plan.Want), Singles: briefIDs(plan.Singles)}); err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(time.Hour)
		if _, wanted, err := f.svc.CheckPendingSingles(f.ctx, 10, always); err != nil || wanted != 1 {
			t.Fatalf("wanted %d (%v), want the single the person asked for", wanted, err)
		}
		if !f.album(t, "rg-s2").Monitored {
			t.Error("the asked-for single isn't wanted")
		}
		if al := f.album(t, "rg-s"); al.Monitored || al.FoundAs != FoundSingleOnAlbum {
			t.Errorf("the asked-for single already on the album: monitored=%v found=%q", al.Monitored, al.FoundAs)
		}
	})
}

// B4: albums that left the artist's browse but are still on MusicBrainz are looked up
// once, remembered, and never hold new releases up; lookups rotate, oldest first.
func TestDeparturesStillOnMusicBrainzDontBlockNewReleases(t *testing.T) {
	f := newRefreshFixture(t)
	var all []metadata.AlbumResult
	for i := 0; i < 8; i++ {
		all = append(all, rg(fmt.Sprintf("rg-%d", i), fmt.Sprintf("A%d", i), "Album", fmt.Sprintf("%d-01-01", 2000+i)))
	}
	f.p.main["mb-x"] = all
	a, err := f.svc.AddArtist(f.ctx, "mb-x", "", true)
	if err != nil {
		t.Fatal(err)
	}
	f.p.main["mb-x"] = []metadata.AlbumResult{all[0], all[1], rg("rg-new", "Brand New", "Album", "2026-10-10")}
	for run := 0; run < 2; run++ {
		f.now = f.now.Add(24 * time.Hour)
		if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if al := f.album(t, "rg-new"); !al.Monitored {
		t.Fatalf("the new release isn't in (found=%q); departures still on MusicBrainz held it up", al.FoundAs)
	}
	if n := f.p.calls["rg"]; n != 6 {
		t.Errorf("%d lookups, want each of the 6 departures looked up once", n)
	}
	for i := 2; i < 8; i++ {
		if al := f.album(t, fmt.Sprintf("rg-%d", i)); al.StaleSince != "" || !al.Monitored {
			t.Errorf("rg-%d: stale=%q monitored=%v, want kept as it was", i, al.StaleSince, al.Monitored)
		}
	}
	// Remembered: the next refreshes don't ask again until the recheck is due.
	f.now = f.now.Add(24 * time.Hour)
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if n := f.p.calls["rg"]; n != 6 {
		t.Errorf("%d lookups after another refresh, want none new", n)
	}
	f.now = f.now.Add(departureRecheck + time.Hour)
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if n := f.p.calls["rg"]; n != 6+rgLookupsPerRefresh {
		t.Errorf("%d lookups once the recheck is due, want %d more (the cap)", n, rgLookupsPerRefresh)
	}
}

// B4: a browse that came back at the cap may be cut short; an album missing from it isn't
// a departure, isn't looked up, and new releases still come in.
func TestBrowseAtTheCapHasNoDepartures(t *testing.T) {
	f := newRefreshFixture(t)
	var all []metadata.AlbumResult
	for i := 0; i < metadata.MBReleaseGroupBrowseCap; i++ {
		all = append(all, rg(fmt.Sprintf("rg-%03d", i), fmt.Sprintf("A%03d", i), "Album", "1990-01-01"))
	}
	f.p.main["mb-big"] = all
	a, err := f.svc.AddArtistWith(f.ctx, "mb-big", "", AddOptions{Monitored: true, Monitor: MonitorFuture})
	if err != nil {
		t.Fatal(err)
	}
	next := append(append([]metadata.AlbumResult(nil), all[1:]...), rg("rg-new", "Brand New", "Album", "2026-10-10"))
	f.p.main["mb-big"] = next
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if f.p.calls["rg"] != 0 {
		t.Errorf("%d lookups on a capped browse, want none", f.p.calls["rg"])
	}
	if f.album(t, "rg-000").StaleSince != "" {
		t.Error("an album past the cap was flagged")
	}
	if !f.album(t, "rg-new").Monitored {
		t.Error("the new release didn't come in")
	}
}

// B5: an artist the library holds only singles of can have an empty album list; that is
// an answer, not MusicBrainz failing.
func TestSinglesOnlyArtistIsAnswered(t *testing.T) {
	f := newRefreshFixture(t)
	singles := ReleaseTypes{Primary: []string{"Album", "EP", "Single"}}
	f.p.other["mb-s"] = []metadata.AlbumResult{rg("rg-s1", "S1", "Single", "2025-01-01")}
	a, err := f.svc.AddArtistWith(f.ctx, "mb-s", "", AddOptions{Monitored: true, ReleaseTypes: &singles})
	if err != nil {
		t.Fatal(err)
	}
	f.p.other["mb-s"] = append(f.p.other["mb-s"], rg("rg-s2", "S2", "Single", "2026-10-10"))
	res, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unanswered || !f.hasAlbum("rg-s2") {
		t.Errorf("singles-only artist: unanswered=%v, new single added=%v", res.Unanswered, f.hasAlbum("rg-s2"))
	}
	if got, _ := f.repo.GetArtist(f.ctx, a.ID); got.StaleSince != "" {
		t.Error("the artist was marked as MusicBrainz not answering")
	}
}

// B6: a single whose song is on an album the owner has, wanted or not, isn't wanted.
func TestSingleOnAnUnwantedAlbumIsNotWanted(t *testing.T) {
	f := newRefreshFixture(t)
	singles := ReleaseTypes{Primary: []string{"Album", "Single"}}
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21")}
	f.p.tracks["rg-okc"] = []metadata.TrackResult{{Title: "Airbag", Disc: 1, Number: 1, RecordingMBID: "rec-airbag"}}
	a, err := f.svc.AddArtistWith(f.ctx, "mb-rh", "", AddOptions{Monitored: true, ReleaseTypes: &singles})
	if err != nil {
		t.Fatal(err)
	}
	okc := f.album(t, "rg-okc")
	if err := f.svc.EnsureTracks(f.ctx, okc); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetAlbumMonitored(f.ctx, okc.ID, false); err != nil { // owned, nothing more wanted
		t.Fatal(err)
	}
	f.p.other["mb-rh"] = []metadata.AlbumResult{rg("rg-s", "Airbag", "Single", "2026-10-01")}
	f.p.tracks["rg-s"] = []metadata.TrackResult{{Title: "Airbag", Disc: 1, Number: 1, RecordingMBID: "rec-airbag"}}
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.CheckPendingSingles(f.ctx, 10, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "rg-s"); al.Monitored || al.FoundAs != FoundSingleOnAlbum {
		t.Errorf("single already on the unwanted album: monitored=%v found=%q", al.Monitored, al.FoundAs)
	}
}

// Albums MusicBrainz no longer lists are kept but never searched by themselves: out of
// the sweep's and Wanted's list, out of the listing fill, and not counted as wanted.
func TestStaleAlbumsAreNotSearchedByThemselves(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-okc", "OK Computer", "Album", "1997-05-21"), rg("rg-gone", "Gone", "Album", "1999-01-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	gone := f.album(t, "rg-gone")
	if err := f.repo.SetAlbumStale(f.ctx, gone.ID, f.now); err != nil {
		t.Fatal(err)
	}
	wanted, err := f.svc.WantedAlbums(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, al := range wanted {
		if al.ID == gone.ID {
			t.Error("a stale album is in the wanted list")
		}
	}
	fill, err := f.svc.AlbumsWithoutListing(f.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, al := range fill {
		if al.ID == gone.ID {
			t.Error("a stale album is in the listing fill")
		}
	}
	art, err := f.svc.GetArtist(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if art.Stats.AlbumsWanted != 1 || art.Stats.AlbumsMonitored != 1 {
		t.Errorf("stats wanted=%d monitored=%d, want the stale album counted as neither", art.Stats.AlbumsWanted, art.Stats.AlbumsMonitored)
	}
	// The listing fill retries weekly, not daily.
	if err := f.svc.FillListing(f.ctx, f.album(t, "rg-okc")); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * 24 * time.Hour)
	if fill, _ := f.svc.AlbumsWithoutListing(f.ctx, 10); len(fill) != 0 {
		t.Errorf("listing fill offers %d albums two days after a try, want a week's wait", len(fill))
	}
}

// P2: the flood guard counts new releases over 30 days, not one refresh: two refreshes of
// six each is a catalogue change too.
func TestFloodGuardIsCumulative(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-x"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-x", "", true)
	if err != nil {
		t.Fatal(err)
	}
	add := func(prefix string) {
		for i := 0; i < 6; i++ {
			f.p.main["mb-x"] = append(f.p.main["mb-x"], rg(fmt.Sprintf("%s-%d", prefix, i), fmt.Sprintf("%s %d", prefix, i), "Album", "2026-09-15"))
		}
	}
	add("first")
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if !f.album(t, "first-0").Monitored {
		t.Fatal("six new releases in one refresh aren't wanted")
	}
	f.now = f.now.Add(24 * time.Hour)
	add("second")
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	if al := f.album(t, "second-0"); al.Monitored || al.FoundAs != FoundTooMany {
		t.Errorf("the second six: monitored=%v found=%q, want listed as too many", al.Monitored, al.FoundAs)
	}
}

// P4: "Want matching albums" never offers to unwant an album holding a file the scan
// can't see right now (an unmounted share).
func TestApplyNeverUnwantsAlbumsWithMissingFiles(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-ep", "Airbag EP", "EP", "1998-03-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	ep := f.album(t, "rg-ep")
	if err := f.repo.UpsertTracks(f.ctx, ep.ID, []Track{{DiscNumber: 1, TrackNumber: 1, Title: "Airbag"}}); err != nil {
		t.Fatal(err)
	}
	tracks, _ := f.repo.TracksFor(f.ctx, ep.ID)
	if err := f.repo.SetTrackFile(f.ctx, tracks[0].ID, "/nas/airbag.flac", "FLAC", 900, 10, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.db.Exec(`UPDATE music_track_files SET missing_since = CURRENT_TIMESTAMP WHERE album_id = ?`, ep.ID); err != nil {
		t.Fatal(err)
	}
	albumsOnly := ReleaseTypes{Primary: []string{"Album"}}
	if _, err := f.svc.SetOptions(f.ctx, a.ID, "", &albumsOnly); err != nil {
		t.Fatal(err)
	}
	plan, err := f.svc.ApplyPreview(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unwant) != 0 {
		t.Errorf("unwant = %+v, want nothing: its file is only missing", plan.Unwant)
	}
}

// P5: an album MusicBrainz merged into a release group under another artist isn't
// re-pointed here (that would file another artist's album under this one): flagged, kept.
func TestMergeIntoAnotherArtistsAlbumIsFlagged(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01"), rg("rg-b", "B", "Album", "2002-01-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-rh", "", true)
	if err != nil {
		t.Fatal(err)
	}
	f.p.main["mb-rh"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01")}
	f.p.merged["rg-b"] = "rg-someone-elses"
	if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	al := f.album(t, "rg-b")
	if al.StaleSince == "" || !al.Monitored {
		t.Errorf("stale=%q monitored=%v, want flagged and otherwise untouched", al.StaleSince, al.Monitored)
	}
	if f.hasAlbum("rg-someone-elses") {
		t.Error("re-pointed to another artist's release group")
	}
}

// P6: when both artists of a merge are in the library, it's written down once.
func TestBothArtistsOfAMergeSaidOnce(t *testing.T) {
	f := newRefreshFixture(t)
	f.p.main["mb-a"] = []metadata.AlbumResult{rg("rg-a", "A", "Album", "2001-01-01")}
	f.p.main["mb-b"] = []metadata.AlbumResult{rg("rg-b", "B", "Album", "2002-01-01")}
	a, err := f.svc.AddArtist(f.ctx, "mb-a", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddArtist(f.ctx, "mb-b", "", true); err != nil {
		t.Fatal(err)
	}
	f.p.artistID["mb-a"] = "mb-b"
	for i := 0; i < 3; i++ {
		if _, err := f.svc.RefreshArtist(f.ctx, a.ID, RefreshOptions{Scheduled: true}); err != nil {
			t.Fatal(err)
		}
	}
	evs, _ := f.repo.Events(f.ctx, a.ID, 50)
	n := 0
	for _, e := range evs {
		if strings.Contains(e.Detail, "merged this artist into another one") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the merge was written down %d times, want once", n)
	}
}
