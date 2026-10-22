package subtitles

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The GPU lane holds while someone watches and goes again when they stop; switched off,
// it never holds.
func TestAIHoldWhileWatching(t *testing.T) {
	s, _ := changeFixture(t)
	ctx := context.Background()
	var watching atomic.Bool
	s.SetWatching(watching.Load, func() bool { return true })
	if !s.watchingKnown() || !s.aiPauseWatching(ctx) {
		t.Fatal("monitoring on and the hold on by default")
	}
	if err := s.aiHold(ctx); err != nil {
		t.Fatal("nobody watching: no hold", err)
	}

	watching.Store(true)
	done := make(chan error, 1)
	go func() { done <- s.aiHold(ctx) }()
	waitFor(t, func() bool { return s.aiHeld.Load() })
	select {
	case <-done:
		t.Fatal("returned while someone is watching")
	case <-time.After(50 * time.Millisecond):
	}
	// Switching the setting off lets it go at once, without waiting for the next look.
	if err := s.SetAIPauseWatching(ctx, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("still holding after the setting went off")
	}
	if s.aiHeld.Load() {
		t.Error("AIPaused left on")
	}
	if err := s.aiHold(ctx); err != nil {
		t.Error("setting off: never holds")
	}

	// A held lane stops with its context.
	_ = s.SetAIPauseWatching(ctx, true)
	cctx, cancel := context.WithCancel(ctx)
	go func() { done <- s.aiHold(cctx) }()
	waitFor(t, func() bool { return s.aiHeld.Load() })
	cancel()
	if err := <-done; err == nil {
		t.Error("a cancelled hold should return the context's error")
	}
}

// The backlog runs requested movies first, then the newest; anything it can't place
// keeps its place behind them.
func TestRankAILine(t *testing.T) {
	s, st := changeFixture(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path, added_at) VALUES (3, 103, 'Old', 1, 1, '/lib/o.mkv', '2020-01-01T00:00:00Z')`,
		`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path, added_at) VALUES (4, 104, 'New', 1, 1, '/lib/n.mkv', '2026-10-01T00:00:00Z')`,
		`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path, added_at) VALUES (5, 105, 'Asked', 1, 1, '/lib/a.mkv', '2019-01-01T00:00:00Z')`,
	} {
		if _, err := st.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	li := laneIndex(LaneGPU)
	s.pending[li][PrioSweep] = []*Job{
		{ID: 1, Kind: "movie", MovieID: 3, Title: "Old"},
		{ID: 2, Kind: "episode", SeriesID: 9, Title: "Show - S01E01"},
		{ID: 3, Kind: "movie", MovieID: 4, Title: "New"},
		{ID: 4, Kind: "movie", MovieID: 5, Title: "Asked"},
	}
	s.SetRequested(func(context.Context) map[int]bool { return map[int]bool{105: true} })
	s.rankAILine(ctx)
	var got []string
	for _, j := range s.pending[li][PrioSweep] {
		got = append(got, j.Title)
	}
	want := []string{"Asked", "New", "Old", "Show - S01E01"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
