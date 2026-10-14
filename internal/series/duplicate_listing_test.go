package series

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// placeholders is what TVDB lists for a season it has announced but not numbered:
// several "TBA" episodes that all share one placeholder number (Paradise (2025) has four
// S03E99 entries). The rebuild used to INSERT each of them and die on the episodes
// UNIQUE(series_id, season_number, episode_number) index, every time.
func placeholders(season int, titles ...string) metadata.SeasonDetails {
	sd := metadata.SeasonDetails{SeasonNumber: season}
	for _, t := range titles {
		sd.Episodes = append(sd.Episodes, metadata.EpisodeDetails{EpisodeNumber: 99, Title: t})
	}
	return sd
}

// An applied renumber whose fresh listing names one (season, episode) more than once
// must rebuild — keeping one row for it — instead of failing the episodes UNIQUE index
// and leaving the owner with a proposal no Apply can ever complete.
func TestApplyNumberingWithDuplicateEpisodeNumbers(t *testing.T) {
	a := animeDetails()
	a.NumberingSource, a.Seasons = "tmdb", listing(4) // TMDB: one long season
	svc, fm, ctx := refreshTestService(t, a)
	sr, err := svc.Add(ctx, a.TMDBID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	const file = "/tv/Anime Show/Season 1/Anime Show - S01E04.mkv"
	if err := svc.repo.SetEpisodeFile(ctx, sr.ID, 1, 4, file, 1); err != nil {
		t.Fatal(err)
	}

	fm.d.NumberingSource = "tvdb"
	fm.d.Seasons = []metadata.SeasonDetails{
		{SeasonNumber: 1, Episodes: []metadata.EpisodeDetails{{EpisodeNumber: 1, AbsoluteNumber: 1}, {EpisodeNumber: 2, AbsoluteNumber: 2}}},
		{SeasonNumber: 2, Episodes: []metadata.EpisodeDetails{{EpisodeNumber: 1, AbsoluteNumber: 3}, {EpisodeNumber: 2, AbsoluteNumber: 4}}},
		placeholders(3, "The Last Astronauts", "Wonderwall", "Dr. Georgia"),
	}
	if _, res, err := svc.Refresh(ctx, sr.ID, RefreshOptions{}); err != nil || !res.Proposed {
		t.Fatalf("the refresh should propose the renumber: %+v, %v", res, err)
	}
	res, err := svc.ApplyNumbering(ctx, sr.ID, pendingHash(t, svc, sr.ID))
	if err != nil {
		t.Fatalf("apply: %v (a listing with a repeated episode number must still rebuild)", err)
	}
	if !res.Rebuilt || len(res.Remaps) != 1 {
		t.Fatalf("result = %+v, want a rebuild with one remap", res)
	}
	if mustFile(t, svc, ctx, sr.ID, 2, 2) != file {
		t.Error("the file should follow its absolute number to S02E02")
	}
	got, _ := svc.Get(ctx, sr.ID)
	if got.NumberingSource != "tvdb" {
		t.Errorf("numbering_source = %q, want tvdb once applied", got.NumberingSource)
	}
	var n int
	var title string
	if err := svc.repo.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(title) FROM episodes WHERE series_id = ? AND season_number = 3`, sr.ID).Scan(&n, &title); err != nil {
		t.Fatal(err)
	}
	if n != 1 || title != "The Last Astronauts" {
		t.Errorf("season 3 = %d rows (title %q), want the first placeholder only", n, title)
	}
}

// The repo-level rebuild takes a listing from anywhere; a repeated (season, episode) —
// or a season listed twice — collapses to its first entry, and what belongs to the owner
// (the file and its hold, monitoring, the season flag) survives.
func TestRebuildEpisodesToleratesRepeatedSlots(t *testing.T) {
	repo, ctx := testRepo(t)
	sr, err := repo.Create(ctx, Series{TMDBID: 5, Title: "Show", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSeasons(ctx, sr.ID, []Season{
		{SeasonNumber: 1, Monitored: true, Episodes: []Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1, Monitored: true},
			{SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2, Monitored: true},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetEpisodeFile(ctx, sr.ID, 1, 2, "/tv/Show/S01E02.mkv", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx,
		`UPDATE episodes SET upgrade_hold = 1 WHERE series_id = ? AND season_number = 1 AND episode_number = 2`, sr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx,
		`UPDATE episodes SET monitored = 0 WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, sr.ID); err != nil {
		t.Fatal(err)
	}
	// The season flag alone (SetSeasonMonitored would cascade to the episodes).
	if _, err := repo.db.ExecContext(ctx,
		`UPDATE seasons SET monitored = 0 WHERE series_id = ? AND season_number = 1`, sr.ID); err != nil {
		t.Fatal(err)
	}

	listing := []Season{
		{SeasonNumber: 1, Monitored: true, Episodes: []Episode{
			{SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 1, Title: "One", Monitored: true},
			{SeasonNumber: 1, EpisodeNumber: 1, AbsoluteNumber: 9, Title: "One again", Monitored: true},
			{SeasonNumber: 1, EpisodeNumber: 2, AbsoluteNumber: 2, Title: "Two", Monitored: true},
		}},
		{SeasonNumber: 2, Monitored: true, Episodes: []Episode{{SeasonNumber: 2, EpisodeNumber: 1, AbsoluteNumber: 3, Title: "Three", Monitored: true}}},
		{SeasonNumber: 2, Monitored: true, Episodes: []Episode{{SeasonNumber: 2, EpisodeNumber: 1, AbsoluteNumber: 3, Title: "Three dup", Monitored: true}}},
	}
	plan, err := repo.PlanRebuild(ctx, sr.ID, listing)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := repo.RebuildEpisodes(ctx, sr.ID, listing)
	if err != nil {
		t.Fatalf("rebuild with repeated slots: %v", err)
	}
	if len(moved) != 0 || len(plan) != 0 {
		t.Errorf("nothing should move: plan %+v, moved %+v", plan, moved)
	}
	f := repo.CurrentEpisodeFile(ctx, sr.ID, 1, 2)
	if f.Path != "/tv/Show/S01E02.mkv" || f.SizeBytes != 42 {
		t.Errorf("the file must stay on S01E02, got %+v", f)
	}
	rows, err := snapshotRows(ctx, repo.db, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 episode rows (S01E01, S01E02, S02E01), got %+v", rows)
	}
	for _, r := range rows {
		switch [2]int{r.Season, r.Episode} {
		case [2]int{1, 1}:
			if r.Monitored || r.Absolute != 1 {
				t.Errorf("S01E01 = %+v, want unmonitored with absolute 1 (the first entry)", r)
			}
		case [2]int{1, 2}:
			if !r.Hold || !r.Monitored {
				t.Errorf("S01E02 = %+v, want its hold and monitoring kept", r)
			}
		}
	}
	seasons, err := repo.SeasonsFor(ctx, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 2 || seasons[0].Monitored {
		t.Errorf("seasons = %+v, want S1 (still unmonitored) and one S2", seasons)
	}
}

// The listing a refresh builds keeps one entry per (season, episode), and the absolutes
// it counts don't skip a number for the entries it dropped.
func TestSeasonsFromDetailsDropsRepeatedEpisodes(t *testing.T) {
	d := &metadata.SeriesDetails{Seasons: []metadata.SeasonDetails{
		{SeasonNumber: 1, Episodes: []metadata.EpisodeDetails{{EpisodeNumber: 1, Title: "a"}, {EpisodeNumber: 1, Title: "b"}, {EpisodeNumber: 2, Title: "c"}}},
		placeholders(2, "x", "y"),
	}}
	got := seasonsFromDetails(d, func(int) bool { return true })
	type row struct {
		s, e, abs int
		title     string
	}
	var rows []row
	for _, sn := range got {
		for _, ep := range sn.Episodes {
			rows = append(rows, row{ep.SeasonNumber, ep.EpisodeNumber, ep.AbsoluteNumber, ep.Title})
		}
	}
	want := []row{{1, 1, 1, "a"}, {1, 2, 2, "c"}, {2, 99, 3, "x"}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}
