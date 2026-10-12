package subtitles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAttentionBuckets(t *testing.T) {
	s, _ := ladderFixture(t, "en,es", englishAudio(), nil, &fakeAI{})
	ctx := context.Background()
	dir := t.TempDir()
	ep := filepath.Join(dir, "Show - S01E02.mkv")
	_ = os.WriteFile(ep, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Show - S01E02.es.srt"), []byte(fakeSRT), 0o644) // made since

	s.snap.movies = []FileSubs{
		{Kind: "movie", MovieID: 1, Title: "Alpha", Year: 2020, Path: "/m/Alpha.mkv", Languages: []LangStatus{{Lang: "en"}, {Lang: "es", Have: true}}},
		{Kind: "movie", MovieID: 2, Title: "Beta", Path: "/m/Beta.mkv", Languages: []LangStatus{{Lang: "en"}}},
		{Kind: "movie", MovieID: 3, Title: "Gamma", Path: "/m/Gamma.mkv", Languages: []LangStatus{{Lang: "en", Orphan: true}},
			Orphans: []Orphan{{Name: "Gamma.old.en.srt", Lang: "en"}}},
	}
	s.snap.groups = []SeriesGroup{{SeriesID: 7, Title: "Show"}}
	s.snap.at = time.Now()
	for _, b := range []Backoff{
		{MediaKey: "m:1", Lang: "en", VideoPath: "/m/Alpha.mkv", PrimaryReason: ResultAuthFailed, Detail: "login rejected", ClearOn: []string{EventCreds}},
		{MediaKey: "m:1", Lang: "es", VideoPath: "/m/Alpha.mkv", PrimaryReason: ResultNoMatch},    // present now
		{MediaKey: "m:2", Lang: "en", VideoPath: "/m/Beta-old.mkv", PrimaryReason: ResultNoMatch}, // the file was upgraded
		{MediaKey: "e:7:1:2", Lang: "en", VideoPath: ep, PrimaryReason: ResultModelMissing},       // still missing
		{MediaKey: "e:7:1:2", Lang: "es", VideoPath: ep, PrimaryReason: ResultNoMatch},            // made since
	} {
		b.Count = 1
		if err := s.ledger.SaveBackoff(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	board, err := s.Attention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]AttentionBucket{}
	for _, b := range board.Buckets {
		got[b.Reason] = b
	}
	if len(board.Buckets) != 3 || board.Total != 3 {
		t.Fatalf("buckets %+v", board.Buckets)
	}
	if b := got[string(ResultAuthFailed)]; b.Count != 1 || b.Fix != FixCreds || b.Items[0].Title != "Alpha (2020)" || b.Items[0].MovieID != 1 {
		t.Errorf("auth bucket %+v", b)
	}
	if b := got[string(ResultModelMissing)]; b.Count != 1 || b.Fix != FixDownloadLarge || b.Items[0].Title != "Show - S01E02" || b.Items[0].SeriesID != 7 {
		t.Errorf("model bucket %+v", b)
	}
	if b := got[ReasonOrphaned]; b.Count != 1 || b.Fix != FixAdopt || b.Items[0].Orphans[0] != "Gamma.old.en.srt" {
		t.Errorf("orphan bucket %+v", b)
	}
	if board.Buckets[0].Reason != string(ResultAuthFailed) || board.Buckets[2].Reason != ReasonOrphaned {
		t.Errorf("order %v %v", board.Buckets[0].Reason, board.Buckets[2].Reason)
	}
	// The Needs-you feed counts only what needs a person.
	if n, err := s.AttentionCount(ctx); err != nil || n != 2 {
		t.Errorf("attention count %d %v", n, err)
	}
}

func TestRetryClearsBackoffAndQueues(t *testing.T) {
	s, _ := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{})
	ctx := context.Background()
	for _, b := range []Backoff{
		{MediaKey: "e:7:1:2", Lang: "en", VideoPath: "/x/a.mkv", PrimaryReason: ResultNoMatch, Count: 1, NextRetryAt: time.Now().Add(7 * 24 * time.Hour).Unix()},
		{MediaKey: "e:7:1:3", Lang: "en", VideoPath: "/x/b.mkv", PrimaryReason: ResultNoMatch, Count: 1, NextRetryAt: time.Now().Add(7 * 24 * time.Hour).Unix()},
		{MediaKey: "e:7:1:4", Lang: "en", VideoPath: "/x/c.mkv", PrimaryReason: ResultAIFailed, Count: 1},
	} {
		if err := s.ledger.SaveBackoff(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	// No series catalog in the fixture: queueing fails, but the backoff still goes, so the
	// sweep tries at once too.
	if _, err := s.Retry(ctx, nil, string(ResultNoMatch), ""); err != nil {
		t.Logf("retry: %v", err)
	}
	left, _ := s.ledger.LoadBackoffs(ctx)
	if len(left) != 1 {
		t.Errorf("backoff left %v, want only the ai_failed row", left)
	}
	if _, err := s.Retry(ctx, nil, ReasonOrphaned, ""); !errors.Is(err, ErrBadRetry) {
		t.Errorf("orphans retried: %v", err)
	}
	if _, err := s.Retry(ctx, []RetryItem{{MediaKey: "../etc", Lang: "en"}}, "", ""); !errors.Is(err, ErrBadRetry) {
		t.Errorf("bad key: %v", err)
	}
	if _, err := s.Retry(ctx, nil, "", ""); !errors.Is(err, ErrBadRetry) {
		t.Errorf("empty: %v", err)
	}
	if _, err := s.Retry(ctx, nil, string(ResultAIFailed), "burn"); !errors.Is(err, ErrBadRetry) {
		t.Errorf("bad mode: %v", err)
	}
}

func TestParseMediaKey(t *testing.T) {
	for k, want := range map[string]MediaRef{
		"m:12":    {Kind: "movie", MovieID: 12},
		"e:3:0:5": {Kind: "episode", SeriesID: 3, Season: 0, Episode: 5},
	} {
		if got, ok := parseMediaKey(k); !ok || got != want {
			t.Errorf("%s → %+v %v", k, got, ok)
		}
	}
	for _, k := range []string{"", "m:", "m:0", "m:-1", "e:1:2", "x:1", "m:1:2"} {
		if _, ok := parseMediaKey(k); ok {
			t.Errorf("%q parsed", k)
		}
	}
}
