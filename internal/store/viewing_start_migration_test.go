package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0308 marks the plays that start a viewing on the history already there: a person's
// first sitting of an item, or one more than three hours after the sitting before it
// stopped. Plays without a rating key are told apart by type, title, show and episode.
func TestViewingStartBackfill(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0308" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0307: %v", err)
	}
	const h = 3600
	type play struct {
		id               int
		user, key, title string
		episode          int
		start, stop      int
		wantStart        int
	}
	plays := []play{
		{1, "1", "100", "Dune", 0, 0, h, 1},             // first sitting
		{2, "1", "100", "Dune", 0, h + 20*60, 2 * h, 0}, // 20 minutes later: the same viewing
		{3, "1", "100", "Dune", 0, 5*h + 1, 6 * h, 1},   // three hours and a second: a new one
		{4, "1", "100", "Dune", 0, 9 * h, 10 * h, 0},    // exactly three hours: still the same
		{5, "2", "100", "Dune", 0, h + 30*60, 2 * h, 1}, // someone else's
		{6, "1", "", "Arrival", 0, 2 * h, 3 * h, 1},     // no key: its title is the item
		{7, "1", "", "Heat", 0, 3*h + 60, 4 * h, 1},     // ...so a different title is another
		{8, "1", "", "Pilot", 1, 0, h, 1},               // keyless episodes apart by number
		{9, "1", "", "Pilot", 2, h + 60, 2 * h, 1},      //
		{10, "1", "", "Pilot", 1, h + 120, 2 * h, 0},    //
		{11, "3", "200", "Ronin", 0, 100, 200, 1},       // a start-second tie goes by id
		{12, "3", "200", "Ronin", 0, 100, 300, 0},       //
	}
	for _, p := range plays {
		mt, show := "movie", ""
		if p.episode > 0 {
			mt, show = "episode", "Show"
		}
		if _, err := db.Exec(`INSERT INTO stream_sessions (id, session_key, user_id, rating_key, media_type, title, grandparent_title,
			parent_index, media_index, started_at, stopped_at) VALUES (?, '', ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			p.id, p.user, p.key, mt, p.title, show, p.episode, 1_760_000_000+p.start, 1_760_000_000+p.stop); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	for _, p := range plays {
		var got int
		if err := db.QueryRow(`SELECT viewing_start FROM stream_sessions WHERE id = ?`, p.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != p.wantStart {
			t.Errorf("play %d (%s): viewing_start %d, want %d", p.id, p.title, got, p.wantStart)
		}
	}

	// From here the triggers keep it: a play slotted in between two sittings starts
	// nothing, and the one after it now follows it.
	if _, err := db.Exec(`INSERT INTO stream_sessions (id, session_key, user_id, rating_key, media_type, title, started_at, stopped_at)
		VALUES (13, '', '1', '100', 'movie', 'Dune', ?, ?)`, 1_760_000_000+3*h, 1_760_000_000+4*h); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]int{13: 0, 3: 0} {
		var got int
		if err := db.QueryRow(`SELECT viewing_start FROM stream_sessions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("after the insert, play %d: viewing_start %d, want %d", id, got, want)
		}
	}
}
