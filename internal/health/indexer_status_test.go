package health

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A paused indexer among working ones is a warning naming it, its error and its next
// try, linking to its card; one failing for over an hour (not paused) is a warning too,
// one failing for less isn't; more than three collapse into one line; nothing failing, or
// a read error, says nothing. (Every indexer failing is IndexerOutageCheck's one error.)
func TestIndexerStatusCheck(t *testing.T) {
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	until := now.Add(time.Hour)
	tl := TroubledIndexer{ID: 3, Name: "TorrentLeech", Failures: 4, FailingSince: now.Add(-20 * time.Minute), Until: until, LastError: "login failed"}
	check := func(enabled int, failing []TroubledIndexer, err error) []Finding {
		return indexerStatusCheck(func(context.Context) (int, []TroubledIndexer, error) { return enabled, failing, err }, func() time.Time { return now }).Run(context.Background())
	}

	got := check(2, []TroubledIndexer{tl}, nil)
	if len(got) != 1 || got[0].Key != "indexers.paused.3" || got[0].Level != LevelWarning || got[0].Fix.Path != "/settings/connections#indexer-3" {
		t.Fatalf("one of two paused: %+v", got)
	}
	for _, want := range []string{"TorrentLeech", "4 times", "15:00", "login failed"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q is missing %q", got[0].Message, want)
		}
	}

	// Failing, not paused (a person's searches keep it out of backoff, or Retry-After never
	// came): only worth a line after an hour.
	x := TroubledIndexer{ID: 5, Name: "1337x", Failures: 1, FailingSince: now.Add(-61 * time.Minute), LastError: "timeout"}
	got = check(3, []TroubledIndexer{x}, nil)
	if len(got) != 1 || got[0].Key != "indexers.failing.5" || !strings.Contains(got[0].Message, "failing since 12:59") || !strings.Contains(got[0].Message, "timeout") {
		t.Fatalf("failing for an hour: %+v", got)
	}
	x.FailingSince = now.Add(-30 * time.Minute)
	if got := check(3, []TroubledIndexer{x}, nil); len(got) != 0 {
		t.Fatalf("failing for half an hour: %+v", got)
	}

	var many []TroubledIndexer
	for i := 1; i <= 5; i++ {
		many = append(many, TroubledIndexer{ID: int64(i), Name: fmt.Sprintf("Prowlarr %d", i), Until: until})
	}
	got = check(9, many, nil)
	if len(got) != 1 || got[0].Key != "indexers.failing" || !strings.HasPrefix(got[0].Message, "5 indexers are failing") || !strings.Contains(got[0].Message, "and 2 more") {
		t.Fatalf("five failing should collapse: %+v", got)
	}

	if got := check(2, nil, nil); len(got) != 0 {
		t.Fatalf("nothing failing: %+v", got)
	}
	if got := check(0, nil, errors.New("db")); len(got) != 0 {
		t.Fatalf("read error: %+v", got)
	}
}
