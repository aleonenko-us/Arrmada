package convert

import (
	"context"
	"strings"
	"testing"
	"time"
)

// heldRow puts a converted, held original of size bytes in the ledger, released at until.
func heldRow(t *testing.T, s *Service, key string, size, out, until int64) int64 {
	t.Helper()
	id := s.history.begin(context.Background(), historyRow{key: key, kind: "movie", srcSize: size, outSize: out})
	if _, err := s.db.Exec(`UPDATE convert_history SET outcome = 'done', finished_at = ?, hold_state = 'held', hold_until = ?, hold_path = '/x' WHERE id = ?`,
		time.Now().Unix(), until, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// With 480 GB held under a 500 GB budget a 40 GB original doesn't start: it waits — with the
// earliest release as its retry time — and nothing is touched or deleted.
func TestHoldBudgetPausesTheConversion(t *testing.T) {
	r := newSpaceRig(t)
	src := r.addMovie(t, 1, "Remux", 64)
	soon := time.Now().Add(3 * 24 * time.Hour).Unix()
	heldRow(t, r.Service, movieKey(8), 300<<30, 100<<30, time.Now().Add(9*24*time.Hour).Unix())
	heldRow(t, r.Service, movieKey(9), 180<<30, 60<<30, soon)
	mi := film("h264", 3840, 2160, 85000, aud("truehd", "eng", 8))
	mi.SizeBytes = 40 << 30
	stubProbe(t, mi)
	stubDisks(t, r.scratch, 1<<50, 1<<50)

	job := r.run(t, 1, "Remux")
	kind, retry, _ := r.skipOf(t, movieKey(1))
	if job.State != StateSkipped || kind != SkipHoldFull || retry != soon {
		t.Fatalf("state %s, skip %q retrying at %d (want %d): %s", job.State, kind, retry, soon, job.Note)
	}
	if !strings.Contains(job.Note, "480.0 GB of 500.0 GB") {
		t.Fatalf("the reason should give the figures: %q", job.Note)
	}
	if sizeOf(src) != 64 || strings.Contains(strings.Join(logMsgs(r.Service), "\n"), "Encoding") {
		t.Fatal("work started, or the original was touched")
	}
	if n := r.failures.failureCount(context.Background(), movieKey(1)); n != 0 {
		t.Fatalf("a full hold counted %d time(s) toward the failure limit", n)
	}

	// A bigger budget retries it straight away.
	if _, err := r.UpdateSettings(context.Background(), SettingsPatch{HoldMaxGB: ptr(600)}); err != nil {
		t.Fatal(err)
	}
	if kind, _, _ := r.skipOf(t, movieKey(1)); kind != "" {
		t.Fatalf("raising the budget left the %q skip", kind)
	}
	// With no undo there's nothing to keep, so no budget either.
	if _, err := r.UpdateSettings(context.Background(), SettingsPatch{HoldMaxGB: ptr(1), HoldDays: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	if ok := r.holdRoom(context.Background(), job, src, mi); !ok {
		t.Fatal("with no undo the budget still stopped the file")
	}
}

// The runner passes over files the hold has no room for, and the status line says why.
func TestStatusPausedOnAFullHold(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	set(t, s, map[string]string{keyAuto: "true", keyHoldMaxGB: "100"})
	indexMovie(t, s, 1, "Remux", film("h264", 3840, 2160, 85000, aud("truehd", "eng", 8))) // ~76 GB
	heldRow(t, s, movieKey(9), 90<<30, 30<<30, time.Now().Add(50*time.Hour).Unix())

	if job := s.pickJob(ctx); job != nil {
		t.Fatalf("picked %+v though its original can't be held", job)
	}
	st := s.Status(ctx)
	if st.State != "paused" || !strings.Contains(st.Message, "originals hold is full (90.0 GB of 100.0 GB)") || !strings.Contains(st.Message, "next release in 3 days") {
		t.Fatalf("status %s: %q", st.State, st.Message)
	}

	set(t, s, map[string]string{keyHoldMaxGB: "0"}) // no limit
	if st := s.Status(ctx); st.State != "starting" {
		t.Fatalf("with no limit: %s %q", st.State, st.Message)
	}
	if job := s.pickJob(ctx); job == nil || job.MovieID != 1 {
		t.Fatalf("with no limit it should be picked, got %+v", job)
	}
}

// Release now deletes one held original, moves its bytes from held to freed, and retries
// files that were waiting for room.
func TestReleaseHoldNow(t *testing.T) {
	ctx := context.Background()
	r, src, dst, mi, plan := finalizeRig(t)
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	e := r.ledger(t, job)
	if st := r.HoldStats(ctx); st.HeldBytes != 1000 || st.HeldSavingBytes != 500 || st.HeldCount != 1 || st.NextReleaseAt != e.HoldUntil {
		t.Fatalf("before: %+v", st)
	}
	r.skips.record(ctx, movieKey(5), SkipHoldFull, "full")

	got, err := r.ReleaseHold(ctx, e.ID)
	if err != nil || got.HoldState != HoldReleased {
		t.Fatalf("release: %+v, %v", got.HoldState, err)
	}
	if exists(e.HoldPath) || sizeOf(src) != 500 {
		t.Fatal("the held original should be gone and the converted file untouched")
	}
	if st := r.HoldStats(ctx); st.HeldBytes != 0 || st.HeldCount != 0 || r.Reclaimed(ctx) != 500 {
		t.Fatalf("after: %+v, reclaimed %d", st, r.Reclaimed(ctx))
	}
	if kind, _, _ := r.skipOf(t, movieKey(5)); kind != "" {
		t.Fatal("a release should retry files waiting on the hold")
	}
	if _, err := r.ReleaseHold(ctx, e.ID); err != ErrNotHeld {
		t.Fatalf("a second release: %v, want ErrNotHeld", err)
	}
}

// Bulk release takes only what's old enough, and nothing mid-swap.
func TestReleaseHoldsOlderThan(t *testing.T) {
	ctx := context.Background()
	r, src, dst, mi, plan := finalizeRig(t)
	job := r.convertNow(t, 1, "Remux", src, dst, mi, plan)
	e := r.ledger(t, job)
	if n, _, err := r.ReleaseHoldsOlderThan(ctx, 7); err != nil || n != 0 || !exists(e.HoldPath) {
		t.Fatalf("a day-old conversion released under 'older than 7 days': %d, %v", n, err)
	}
	if _, err := r.db.Exec(`UPDATE convert_history SET finished_at = ? WHERE id = ?`, time.Now().AddDate(0, 0, -8).Unix(), e.ID); err != nil {
		t.Fatal(err)
	}
	if n, freed, err := r.ReleaseHoldsOlderThan(ctx, 7); err != nil || n != 1 || freed != 500 || exists(e.HoldPath) {
		t.Fatalf("released %d (%d bytes), %v", n, freed, err)
	}
}
