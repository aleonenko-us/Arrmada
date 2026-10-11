package series

import (
	"errors"
	"testing"
)

// Bulk monitor (SER-16): one transaction for the picked episodes, a foreign id refuses the
// whole change, and every touched season's flag follows its episodes.
func TestSetEpisodesMonitoredBulk(t *testing.T) {
	repo, db, ctx := monitorFixture(t, 1)
	if _, err := db.ExecContext(ctx, `INSERT INTO episodes (series_id,season_number,episode_number,air_date,monitored) VALUES (1,2,2,'2024-10-11',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO series (id,tmdb_id,title,monitored) VALUES (2,8,'Other',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO episodes (series_id,season_number,episode_number,monitored) VALUES (2,1,1,0)`); err != nil {
		t.Fatal(err)
	}
	ids := func(where string) []int64 {
		rows, err := db.QueryContext(ctx, `SELECT id FROM episodes WHERE `+where+` ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			out = append(out, id)
		}
		return out
	}
	season2 := ids(`series_id = 1 AND season_number = 2`)
	foreign := ids(`series_id = 2`)
	if len(season2) != 2 || len(foreign) != 1 {
		t.Fatalf("fixture: season 2 %v, foreign %v", season2, foreign)
	}

	// A foreign id refuses everything: nothing in season 2 changes either.
	if _, err := repo.SetEpisodesMonitored(ctx, 1, append(append([]int64{}, season2...), foreign...), true); !errors.Is(err, ErrForeignEpisodes) {
		t.Fatalf("foreign id: err = %v, want ErrForeignEpisodes", err)
	}
	if got := countEps(t, db, `season_number = 2 AND monitored = 1`); got != 0 {
		t.Fatalf("a refused change still monitored %d episodes", got)
	}
	if _, err := repo.SetEpisodesMonitored(ctx, 1, []int64{999999}, true); !errors.Is(err, ErrForeignEpisodes) {
		t.Fatalf("unknown id: err = %v", err)
	}

	// Monitoring episodes in an unmonitored season monitors the season; duplicates count once.
	n, err := repo.SetEpisodesMonitored(ctx, 1, []int64{season2[0], season2[1], season2[0]}, true)
	if err != nil || n != 2 {
		t.Fatalf("set = %d, %v; want 2", n, err)
	}
	flags, _ := repo.SeasonMonitorFlags(ctx, 1)
	if !flags[2] || flags[1] {
		t.Errorf("season flags = %v, want season 2 monitored and season 1 untouched", flags)
	}

	// Unmonitoring every episode of the season unmonitors it again.
	if _, err := repo.SetEpisodesMonitored(ctx, 1, season2, false); err != nil {
		t.Fatal(err)
	}
	flags, _ = repo.SeasonMonitorFlags(ctx, 1)
	if flags[2] {
		t.Error("season 2 still monitored with none of its episodes monitored")
	}
	if n, err := repo.SetEpisodesMonitored(ctx, 1, nil, true); n != 0 || err != nil {
		t.Errorf("empty list: %d, %v", n, err)
	}
}
