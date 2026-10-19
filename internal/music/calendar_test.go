package music

import "testing"

// The Calendar takes a library artist's albums dated to a day inside the window: not a
// year- or month-only date, not one outside it, not one MusicBrainz stopped listing, and
// not an album filed under an artist who is only credited.
func TestCalendarAlbums(t *testing.T) {
	r, ctx := testRepo(t)
	low, err := r.CreateArtist(ctx, Artist{MBID: "a-1", Name: "Low", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := r.CreateArtist(ctx, Artist{MBID: "a-2", Name: "Paused Band"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(artist int64, mbid, title, date string, monitored bool) int64 {
		t.Helper()
		id, err := r.UpsertAlbum(ctx, Album{ArtistID: artist, MBID: mbid, Title: title, ReleaseDate: date, Monitored: monitored})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	upcoming := add(low.ID, "rg-1", "Upcoming", "2026-11-14", true)
	recent := add(paused.ID, "rg-2", "Recent", "2026-10-02", true)
	add(low.ID, "rg-3", "Month only", "2026-11", true)
	add(low.ID, "rg-4", "Year only", "2026", true)
	add(low.ID, "rg-5", "Too late", "2027-03-01", true)
	add(low.ID, "rg-6", "Not a date", "2026-13-45", true)
	stale := add(low.ID, "rg-7", "Gone", "2026-11-01", true)
	if _, err := r.db.ExecContext(ctx, `UPDATE albums SET stale_since = CURRENT_TIMESTAMP WHERE id = ?`, stale); err != nil {
		t.Fatal(err)
	}
	guest := add(low.ID, "rg-8", "Credited only", "2026-11-02", true)
	if _, err := r.db.ExecContext(ctx, `INSERT INTO artists (mbid, name, in_library) VALUES ('a-3', 'Guest', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE albums SET artist_id = (SELECT id FROM artists WHERE mbid = 'a-3') WHERE id = ?`, guest); err != nil {
		t.Fatal(err)
	}

	got, err := r.CalendarAlbums(ctx, "2026-10-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != recent || got[1].ID != upcoming {
		t.Fatalf("albums = %+v, want Recent then Upcoming", got)
	}
	if got[0].ArtistName != "Paused Band" || got[0].ArtistMonitored {
		t.Errorf("recent = %+v, want the paused artist's name and switch", got[0])
	}
	if got[1].ArtistName != "Low" || !got[1].ArtistMonitored || !got[1].Monitored {
		t.Errorf("upcoming = %+v", got[1])
	}
}
