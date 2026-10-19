package music

import (
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The review's case (MUS-30): the old listing came from the 1997 CD, and the same day's
// Digital Media release is a four-track deluxe. "original" reads "1997" and "1997-05-21" as
// the same day and prefers Digital Media, so it picks the deluxe; the re-key must still go
// to the CD the listing came from, and the album must stay complete.
func TestRekeyGoesBackToTheListingsOwnRelease(t *testing.T) {
	r, ctx := testRepo(t)
	dir := t.TempDir()
	albumID, files := legacyAlbum(t, r, dir, []EditionTrack{
		{Disc: 1, Position: 1, Title: "Airbag", MBID: "rt-cd-1"},
		{Disc: 1, Position: 2, Title: "Paranoid Android", MBID: "rt-cd-2"},
		{Disc: 1, Position: 3, Title: "Subterranean Homesick Alien", MBID: "rt-cd-3"},
	})
	cd, cdDet := mbRelease("rel-cd", "1997-05-21", "GB", "CD",
		[]string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien"}, "rec", "rt-cd")
	dig, digDet := mbRelease("rel-dig", "1997", "XW", "Digital Media",
		[]string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien", "Lull"}, "rec", "rt-dig")
	fake := &fakeEditions{
		releases: map[string][]metadata.ReleaseSummary{"rg-okc": {dig, cd}},
		details:  map[string]metadata.ReleaseDetail{"rel-cd": cdDet, "rel-dig": digDet},
	}
	eds := []Edition{{ID: 1, MBID: "rel-cd", ReleaseDate: "1997-05-21", MediaSummary: "CD", TrackCount: 3, Status: "Official"},
		{ID: 2, MBID: "rel-dig", ReleaseDate: "1997", MediaSummary: "Digital Media", TrackCount: 4, Status: "Official"}}
	if pick, _ := PickEdition(eds, EditionPolicy{Mode: PolicyOriginal}); pick.MBID != "rel-dig" {
		t.Fatalf("the policy picks %q; the case needs it to pick the same-day digital deluxe", pick.MBID)
	}

	svc := &Service{repo: r, meta: fake, log: slog.Default()}
	al, _ := r.GetAlbum(ctx, albumID)
	if err := svc.EnsureTracks(ctx, al); err != nil {
		t.Fatal(err)
	}
	al, _ = r.GetAlbum(ctx, albumID)
	target, err := r.Edition(ctx, al.TargetEditionID)
	if err != nil {
		t.Fatal(err)
	}
	if target.MBID != "rel-cd" {
		t.Errorf("re-keyed to %q, want the CD the listing was read from", target.MBID)
	}
	if !al.Complete() || al.HaveTracks != 3 || al.TrackCount != 3 {
		t.Errorf("the album reads %d/%d, want still 3/3", al.HaveTracks, al.TrackCount)
	}
	filesUntouched(t, dir, files)
}

// With no release ids on the old listing (rows from long ago), the policy's deluxe may be
// the only edition the files fit. The re-key may go there, but its bonus track isn't newly
// wanted: the album stays complete. When the album isn't monitored every track counts, so
// the deluxe would make it incomplete, and the re-key is refused.
func TestRekeyNeverMakesAnAlbumLessComplete(t *testing.T) {
	cases := []struct {
		name      string
		monitored bool
		wantDig   bool
	}{
		{"monitored: re-keyed, the bonus track not wanted", true, true},
		{"not monitored: every track counts, so refused", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ctx := testRepo(t)
			dir := t.TempDir()
			albumID, files := legacyAlbum(t, r, dir, []EditionTrack{
				{Disc: 1, Position: 1, Title: "Airbag"},
				{Disc: 1, Position: 2, Title: "Paranoid Android"},
				{Disc: 1, Position: 3, Title: "Subterranean Homesick Alien"},
			})
			if !c.monitored {
				if err := r.SetAlbumMonitored(ctx, albumID, false); err != nil {
					t.Fatal(err)
				}
			}
			dig, digDet := mbRelease("rel-dig", "1997", "XW", "Digital Media",
				[]string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien", "Lull"}, "rec", "rt-dig")
			fake := &fakeEditions{
				releases: map[string][]metadata.ReleaseSummary{"rg-okc": {dig}},
				details:  map[string]metadata.ReleaseDetail{"rel-dig": digDet},
			}
			before, _ := r.GetAlbum(ctx, albumID)
			svc := &Service{repo: r, meta: fake, log: slog.Default()}
			if err := svc.EnsureTracks(ctx, before); err != nil {
				t.Fatal(err)
			}
			after, _ := r.GetAlbum(ctx, albumID)
			moved := after.TargetEditionID != before.TargetEditionID
			if moved != c.wantDig {
				t.Fatalf("re-keyed = %v, want %v", moved, c.wantDig)
			}
			if !after.Complete() || after.HaveTracks != 3 {
				t.Errorf("the album reads %d/%d, want still complete with its 3 files", after.HaveTracks, after.TrackCount)
			}
			filesUntouched(t, dir, files)

			// A failed attempt is remembered: looking again doesn't call MusicBrainz.
			calls := fake.calls
			if err := svc.EnsureTracks(ctx, after); err != nil || fake.calls != calls {
				t.Errorf("a second look made %d calls (%v), want none", fake.calls-calls, err)
			}
		})
	}
}

// The album page and the library scan make no MusicBrainz call for an album that has a
// listing: editions are listed on search, import, or when the picker is opened.
func TestPageAndScanDontListEditions(t *testing.T) {
	r, ctx := testRepo(t)
	albumID, _ := legacyAlbum(t, r, t.TempDir(), []EditionTrack{{Disc: 1, Position: 1, Title: "Airbag", MBID: "rt-std-1"}})
	fake := okcEditions()
	svc := &Service{repo: r, meta: fake, log: slog.Default()}
	if _, err := svc.GetAlbum(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TargetCandidates(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("page and scan made %d MusicBrainz calls, want none", fake.calls)
	}
	// Opening the picker lists them.
	if _, _, err := svc.AlbumEditions(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	if fake.calls == 0 {
		t.Error("opening the edition picker didn't list the editions")
	}
}
