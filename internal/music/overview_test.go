package music

import (
	"sort"
	"testing"
	"time"
)

// IncompleteWanted is the Dashboard's grouped form of WantedAlbums filtered by
// !Complete: over albums that are complete, half there, unlisted, missing a file, with an
// unwanted track, with an open migration note, stale, unmonitored or of a paused artist,
// both must name the same albums.
func TestIncompleteWantedMatchesWantedAlbums(t *testing.T) {
	r, ctx := testRepo(t)
	low, err := r.CreateArtist(ctx, Artist{MBID: "a-1", Name: "Low", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := r.CreateArtist(ctx, Artist{MBID: "a-2", Name: "Paused"})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	add := func(artist int64, monitored bool, tracks, have int) (int64, []Track) {
		t.Helper()
		n++
		id, err := r.UpsertAlbum(ctx, Album{ArtistID: artist, MBID: "rg-" + string(rune('a'+n)), Title: "Album", Year: 2020, Monitored: monitored})
		if err != nil {
			t.Fatal(err)
		}
		if tracks == 0 {
			return id, nil
		}
		list := make([]Track, tracks)
		for i := range list {
			list[i] = Track{DiscNumber: 1, TrackNumber: i + 1, Title: "T"}
		}
		if err := r.UpsertTracks(ctx, id, list); err != nil {
			t.Fatal(err)
		}
		listed, err := r.TracksFor(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < have; i++ {
			if err := r.SetTrackFile(ctx, listed[i].ID, "/m/"+string(rune('a'+n))+"/"+string(rune('a'+i)), "FLAC", 900, 10, ""); err != nil {
				t.Fatal(err)
			}
		}
		return id, listed
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	add(low.ID, true, 2, 2) // complete
	add(low.ID, true, 3, 1) // half there
	add(low.ID, true, 0, 0) // no listing: never complete
	gone, _ := add(low.ID, true, 2, 2)
	exec(`UPDATE music_track_files SET missing_since = CURRENT_TIMESTAMP WHERE album_id = ?`, gone) // files gone missing
	_, skip := add(low.ID, true, 3, 2)
	exec(`UPDATE music_release_tracks SET monitored = 0 WHERE id = ?`, skip[2].ID) // the missing track isn't wanted: complete
	noted, nt := add(low.ID, true, 2, 1)
	exec(`INSERT INTO music_migration_notes (legacy_track_id, album_id, kind, note) VALUES (?, ?, 'shared_path', 'x')`, nt[1].ID, noted)
	stale, _ := add(low.ID, true, 2, 0)
	exec(`UPDATE albums SET stale_since = CURRENT_TIMESTAMP WHERE id = ?`, stale)
	add(low.ID, false, 2, 0)   // not wanted
	add(paused.ID, true, 2, 0) // its artist is paused

	all, err := r.WantedAlbums(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	for _, al := range all {
		if !al.Complete() {
			want = append(want, al.ID)
		}
	}
	got, err := r.IncompleteWanted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, al := range got {
		ids = append(ids, al.ID)
		if al.Year != 2020 {
			t.Errorf("album %d came back without its year: %+v", al.ID, al)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(want) != 3 || len(ids) != len(want) {
		t.Fatalf("grouped = %v, WantedAlbums' incomplete = %v (want the half, unlisted and missing-file albums)", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("grouped = %v, WantedAlbums' incomplete = %v", ids, want)
		}
	}
}

// The Dashboard's New releases: the newest few, with their artist, and the exact total
// however long the list is.
func TestRecentReleases(t *testing.T) {
	r, ctx := testRepo(t)
	a, err := r.CreateArtist(ctx, Artist{MBID: "a-1", Name: "Low", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 7; i++ {
		found := dbTime(now.Add(-time.Duration(i) * time.Hour))
		if _, err := r.UpsertAlbum(ctx, Album{ArtistID: a.ID, MBID: "rg-" + string(rune('a'+i)), Title: "New " + string(rune('A'+i)),
			FoundAt: found, FoundAs: FoundNew, Monitored: i%2 == 0}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.UpsertAlbum(ctx, Album{ArtistID: a.ID, MBID: "rg-old", Title: "Old", FoundAt: dbTime(now.AddDate(0, 0, -40))}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertAlbum(ctx, Album{ArtistID: a.ID, MBID: "rg-with", Title: "Added with the artist"}); err != nil {
		t.Fatal(err)
	}
	list, total, err := r.RecentReleases(ctx, now.AddDate(0, 0, -30), 5)
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 || len(list) != 5 {
		t.Fatalf("total %d, rows %d: want 7 and 5", total, len(list))
	}
	if list[0].Title != "New A" || list[0].ArtistName != "Low" || !list[0].Monitored || list[0].FoundAs != FoundNew || list[1].Monitored {
		t.Errorf("newest = %+v, next %+v", list[0], list[1])
	}
}
