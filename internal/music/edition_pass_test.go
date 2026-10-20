package music

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// legacyAlbumRG is legacyAlbum for one of several albums: its own release group, a
// legacy edition with the given titles, and a file row per track.
func legacyAlbumRG(t *testing.T, r *Repo, dir string, artistID int64, rgID string, titles []string) int64 {
	t.Helper()
	ctx := context.Background()
	albumID, err := r.UpsertAlbum(ctx, Album{ArtistID: artistID, MBID: rgID, Title: rgID, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	var tracks []EditionTrack
	for i, title := range titles {
		tracks = append(tracks, EditionTrack{Disc: 1, Position: i + 1, Title: title})
	}
	legacyID, err := r.AddEdition(ctx, albumID, EditionInput{Edition: Edition{Legacy: true, TracksFetched: true}, Tracks: tracks})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetTargetEdition(ctx, albumID, legacyID, false); err != nil {
		t.Fatal(err)
	}
	legacy, err := r.Edition(ctx, legacyID)
	if err != nil {
		t.Fatal(err)
	}
	for i, tk := range legacy.Media[0].Tracks {
		p := filepath.Join(dir, fmt.Sprintf("%s-%02d.flac", rgID, i+1))
		if err := r.RecordTrackFile(ctx, tk.ID, FileRecord{Path: p, SizeBytes: 10, Quality: QualityFLAC}); err != nil {
			t.Fatal(err)
		}
	}
	return albumID
}

// The background edition pass re-keys a few legacy albums per run, never more than its
// cap, nothing at all when the run's budget says stop, and never into an edition that
// would make an album less complete or newly want a track; an album that fits nothing
// isn't tried again until its editions are next due.
func TestEditionPassIsCappedAndNeverLessComplete(t *testing.T) {
	r, ctx := testRepo(t)
	dir := t.TempDir()
	a, err := r.CreateArtist(ctx, Artist{MBID: "mb-rh", Name: "Radiohead", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{"Airbag", "Paranoid Android", "Subterranean Homesick Alien"}
	fake := &fakeEditions{releases: map[string][]metadata.ReleaseSummary{}, details: map[string]metadata.ReleaseDetail{}}
	var ids []int64
	for i := 0; i < 4; i++ {
		rgID := fmt.Sprintf("rg-%d", i)
		ids = append(ids, legacyAlbumRG(t, r, dir, a.ID, rgID, titles))
		relTitles := titles
		if i == 3 {
			// Only a deluxe: its bonus track would make the (unmonitored) album incomplete.
			relTitles = append(append([]string(nil), titles...), "Lull")
		}
		sum, det := mbRelease("rel-"+rgID, "1997-05-21", "GB", "CD", relTitles, "rec-"+rgID, "rt-"+rgID)
		fake.releases[rgID] = []metadata.ReleaseSummary{sum}
		fake.details["rel-"+rgID] = det
	}
	if err := r.SetAlbumMonitored(ctx, ids[3], false); err != nil {
		t.Fatal(err)
	}
	before := map[int64]Album{}
	for _, id := range ids {
		before[id], _ = r.GetAlbum(ctx, id)
	}
	svc := &Service{repo: r, meta: fake, log: slog.Default()}
	always := func() bool { return true }

	if tried, _, err := svc.RekeyLegacyEditions(ctx, 2, func() bool { return false }); err != nil || tried != 0 || fake.calls != 0 {
		t.Fatalf("with no budget tried %d, %d calls (%v), want nothing", tried, fake.calls, err)
	}
	total, rekeyed := 0, 0
	for run := 0; run < 3; run++ {
		tried, moved, err := svc.RekeyLegacyEditions(ctx, 2, always)
		if err != nil {
			t.Fatal(err)
		}
		if tried > 2 {
			t.Fatalf("run %d tried %d albums, the cap is 2", run, tried)
		}
		total += tried
		rekeyed += moved
	}
	if total != 4 || rekeyed != 3 {
		t.Errorf("tried %d and re-keyed %d over three runs, want 4 tried and 3 re-keyed", total, rekeyed)
	}
	for i, id := range ids {
		after, _ := r.GetAlbum(ctx, id)
		b := before[id]
		if after.HaveTracks < b.HaveTracks || (b.Complete() && !after.Complete()) || after.TrackCount > b.TrackCount {
			t.Errorf("album %d: %d/%d → %d/%d: less complete, or newly wanting a track", i, b.HaveTracks, b.TrackCount, after.HaveTracks, after.TrackCount)
		}
		target, _ := r.Edition(ctx, after.TargetEditionID)
		if wantLegacy := i == 3; target.Legacy != wantLegacy {
			t.Errorf("album %d still legacy = %v, want %v", i, target.Legacy, wantLegacy)
		}
	}
	calls := fake.calls
	if tried, _, _ := svc.RekeyLegacyEditions(ctx, 2, always); tried != 0 || fake.calls != calls {
		t.Errorf("a fourth run tried %d albums and made %d calls; the refused one waits until its editions are due", tried, fake.calls-calls)
	}
}
