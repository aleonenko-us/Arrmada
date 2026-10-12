package notify

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// memKV stands in for the settings service; sharing one between two ConvertAlerts is a
// restart.
type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) Get(_ context.Context, key, def string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.m[key]; ok {
		return v
	}
	return def
}

func (k *memKV) Set(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

func deliveriesOf(t *testing.T, s *Service, event string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT body FROM notification_deliveries WHERE event_key = ? ORDER BY id`, event)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// A file Convert gives up on is one convert.failed alert, at once — and a replay of the
// same event doesn't send a second.
func TestConvertBlockedAlertsOnce(t *testing.T) {
	s, _, bus := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mustCreate(t, s, Connection{Name: "problems", URL: "ntfy://p", Events: sub(EventConvertFailed), Enabled: true})
	mustCreate(t, s, Connection{Name: "other", URL: "ntfy://o", Events: sub("release.grabbed"), Enabled: true})
	c := NewConvertAlerts(s, &memKV{m: map[string]string{}}, s.log)
	done := make(chan struct{})
	go func() { c.Run(ctx, bus); close(done) }()

	ev := map[string]any{"title": "Dune (2021)", "key": "movie:12", "note": "ffmpeg exited with status 1", "count": 3, "at": int64(1_800_000_000)}
	waitFor(t, "the gave-up alert", func() bool {
		bus.Publish("convert.blocked", ev)
		return len(deliveriesOf(t, s, EventConvertFailed)) > 0
	})
	bus.Publish("convert.blocked", ev) // the same occurrence again
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	got := deliveriesOf(t, s, EventConvertFailed)
	if len(got) != 1 {
		t.Fatalf("got %d deliveries, want 1: %v", len(got), got)
	}
	if want := "⚠️ Convert gave up on Dune (2021) after 3 tries — ffmpeg exited with status 1"; got[0] != want {
		t.Errorf("body = %q, want %q", got[0], want)
	}

	// Blocked again later (Try again, three more failures): a new occurrence, a new alert.
	ev2 := map[string]any{"title": "Dune (2021)", "key": "movie:12", "note": "x", "count": 3, "at": int64(1_800_100_000)}
	c.Handle(context.Background(), "convert.blocked", ev2)
	if n := len(deliveriesOf(t, s, EventConvertFailed)); n != 2 {
		t.Errorf("a second blocking of the file: %d deliveries, want 2", n)
	}
}

// Three conversions in a day are one summary at 09:00, and a restart neither loses the
// counts nor sends the summary a second time.
func TestConvertDigestDailyAndRestartSafe(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Connection{Name: "daily", URL: "ntfy://d", Events: sub(EventConvertSummary), Enabled: true})
	kv := &memKV{m: map[string]string{}}
	clk := time.Date(2026, 10, 12, 2, 0, 0, 0, time.Local)
	mk := func() *ConvertAlerts {
		c := NewConvertAlerts(s, kv, s.log)
		c.now = func() time.Time { return clk }
		return c
	}
	c := mk()
	for i := 0; i < 3; i++ {
		c.Handle(ctx, "convert.done", map[string]any{"key": "movie:1", "saved_bytes": float64(100 << 30)})
	}
	c.Handle(ctx, "convert.failed", map[string]any{"key": "movie:2", "note": "boom"})
	c.Handle(ctx, "convert.failed", map[string]any{"key": "movie:2", "note": "boom again"}) // same file
	c.Handle(ctx, "convert.failed", map[string]any{"key": "movie:3", "note": "disk", "transient": true})
	c.Handle(ctx, "convert.stalled", map[string]any{"key": "movie:4", "reason": "no_scratch"})

	// Before 09:00: nothing.
	if err := c.SendDigest(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(deliveriesOf(t, s, EventConvertSummary)); n != 0 {
		t.Fatalf("a summary went before 09:00 (%d)", n)
	}

	// A restart at 08:00 keeps the counts.
	clk = clk.Add(6 * time.Hour)
	c = mk()
	clk = time.Date(2026, 10, 12, 9, 5, 0, 0, time.Local)
	if err := c.SendDigest(ctx); err != nil {
		t.Fatal(err)
	}
	got := deliveriesOf(t, s, EventConvertSummary)
	if len(got) != 1 || got[0] != "🎞️ Convert: 3 files, 300 GB freed, 2 problems" {
		t.Fatalf("summary = %v", got)
	}

	// Later the same day, and after another restart: nothing more, even with new work.
	c.Handle(ctx, "convert.done", map[string]any{"key": "movie:5", "saved_bytes": float64(1 << 30)})
	c = mk()
	clk = clk.Add(3 * time.Hour)
	if err := c.SendDigest(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(deliveriesOf(t, s, EventConvertSummary)); n != 1 {
		t.Fatalf("summaries the same day = %d, want 1", n)
	}
	// The next morning the new work goes out.
	clk = time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local)
	if err := c.SendDigest(ctx); err != nil {
		t.Fatal(err)
	}
	got = deliveriesOf(t, s, EventConvertSummary)
	if len(got) != 2 || !strings.Contains(got[1], "1 file, 1 GB freed") {
		t.Fatalf("next day's summary = %v", got)
	}
	// A quiet day sends nothing.
	clk = time.Date(2026, 10, 14, 10, 0, 0, 0, time.Local)
	if err := c.SendDigest(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(deliveriesOf(t, s, EventConvertSummary)); n != 2 {
		t.Errorf("a day with nothing to report sent a summary")
	}
}
