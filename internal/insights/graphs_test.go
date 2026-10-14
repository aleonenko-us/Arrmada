package insights

import (
	"context"
	"math"
	"testing"
	"time"
)

// seedGraphRows records plays earlier today and at the same clock time yesterday, so they
// land on known days of the chart's axis whatever the test machine's time zone.
func seedGraphRows(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	base := midnight.Add(now.Sub(midnight) / 2) // today, and not in the future
	at := func(daysAgo int) int64 { return base.AddDate(0, 0, -daysAgo).Unix() }
	rows := []sessionRecord{
		// "Today": a 1h transcoded movie and a 30 min direct-play episode.
		{UserID: "1", UserName: "Amy", MediaType: "movie", Title: "Dune", Decision: "transcode", StartedAt: at(0), StoppedAt: at(0) + 7200, WatchedMS: 3600 * 1000},
		{UserID: "2", UserName: "Ben", MediaType: "episode", Title: "Pilot", GrandparentTitle: "Severance", Decision: "direct_play", StartedAt: at(0) + 60, StoppedAt: at(0) + 1860},
		// "Yesterday": a direct stream, and an import with no decision (counts as direct play).
		{UserID: "1", UserName: "Amy", MediaType: "episode", Title: "Ep 2", GrandparentTitle: "Severance", Decision: "direct_stream", StartedAt: at(1), StoppedAt: at(1) + 2700},
		{UserID: "1", UserName: "Amy", MediaType: "movie", Title: "Arrival", Decision: "", StartedAt: at(1) + 10, StoppedAt: at(1) + 910, WatchedMS: 900 * 1000},
		// A photo isn't charted, so it can't throw the stream-type sums off.
		{UserID: "1", UserName: "Amy", MediaType: "photo", Title: "Beach", Decision: "transcode", StartedAt: at(1) + 20, StoppedAt: at(1) + 30},
	}
	for _, r := range rows {
		if _, err := s.repo.insertSession(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// Hours turns every chart into watch time, from the same per-row figure the People totals
// use (watched_ms when the source reported it, wall time minus pauses otherwise).
func TestGraphsDurationMetric(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	seedGraphRows(t, s)

	g, err := s.Graphs(ctx, 7, MetricDuration, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.Metric != MetricDuration {
		t.Fatalf("metric = %q", g.Metric)
	}
	last := len(g.Days) - 1
	if !near(g.DailyMovies[last], 1) || !near(g.DailyTV[last], 0.5) {
		t.Errorf("today movies/tv = %v / %v hours, want 1 / 0.5", g.DailyMovies[last], g.DailyTV[last])
	}
	if !near(g.DailyTV[last-1], 0.75) || !near(g.DailyMovies[last-1], 0.25) {
		t.Errorf("yesterday tv/movies = %v / %v hours, want 0.75 / 0.25", g.DailyTV[last-1], g.DailyMovies[last-1])
	}
	var hours float64
	for _, v := range g.ByHour {
		hours += v
	}
	// 1 + 0.5 + 0.75 + 0.25 charted, plus the photo's 10 s (hour buckets cover every play).
	if !near(hours, 2.5+10.0/3600) {
		t.Errorf("hour buckets sum to %v hours", hours)
	}
	// Top users rank by watch time and carry it in secs.
	if len(g.TopUsers) != 2 || g.TopUsers[0].Name != "Amy" || g.TopUsers[0].Secs != 3600+2700+900+10 {
		t.Errorf("top users = %+v", g.TopUsers)
	}

	plays, err := s.Graphs(ctx, 7, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plays.Metric != MetricPlays || plays.DailyMovies[last] != 1 || plays.DailyTV[last-1] != 1 {
		t.Errorf("plays metric = %+v", plays)
	}
}

// The stream-type lines add up to the daily play count, day by day, in both metrics.
func TestGraphsDecisionSeriesAligned(t *testing.T) {
	ctx := context.Background()
	s := newDataTestService(t)
	seedGraphRows(t, s)
	for _, metric := range []string{MetricPlays, MetricDuration} {
		g, err := s.Graphs(ctx, 7, metric, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(g.DailyDirectPlay) != len(g.Days) || len(g.DailyDirectStream) != len(g.Days) || len(g.DailyTranscode) != len(g.Days) {
			t.Fatalf("%s: series not aligned to the %d days", metric, len(g.Days))
		}
		for i := range g.Days {
			total := g.DailyTV[i] + g.DailyMovies[i] + g.DailyMusic[i]
			split := g.DailyDirectPlay[i] + g.DailyDirectStream[i] + g.DailyTranscode[i]
			if !near(total, split) {
				t.Errorf("%s %s: decisions sum to %v, plays %v", metric, g.Days[i], split, total)
			}
		}
		last := len(g.Days) - 1
		if metric == MetricPlays && (g.DailyTranscode[last] != 1 || g.DailyDirectPlay[last] != 1 || g.DailyDirectStream[last-1] != 1 || g.DailyDirectPlay[last-1] != 1) {
			t.Errorf("plays by decision: today %v/%v/%v, yesterday %v/%v/%v",
				g.DailyDirectPlay[last], g.DailyDirectStream[last], g.DailyTranscode[last],
				g.DailyDirectPlay[last-1], g.DailyDirectStream[last-1], g.DailyTranscode[last-1])
		}
	}
}
