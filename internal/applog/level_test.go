package applog

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// Raising the level is always temporary: it ends by itself after the chosen time, and a
// second Set replaces the first deadline rather than stacking.
func TestLogLevelSwitchAndRevert(t *testing.T) {
	s := NewLevelSwitch(slog.LevelInfo)
	if st := s.State(); st.Level != slog.LevelInfo || !st.Until.IsZero() {
		t.Fatalf("fresh switch = %+v, want info with no deadline", st)
	}

	until := s.Set(slog.LevelDebug, 40*time.Millisecond)
	if st := s.State(); st.Level != slog.LevelDebug || !st.Until.Equal(until) || until.IsZero() {
		t.Fatalf("after Set = %+v (until %v), want debug until the returned time", st, until)
	}
	waitFor(t, func() bool { return s.State().Level == slog.LevelInfo })
	if st := s.State(); !st.Until.IsZero() {
		t.Errorf("after the revert the deadline should be gone: %+v", st)
	}

	// A re-Set pushes the deadline out: the first timer must not cut the second short.
	s.Set(slog.LevelDebug, 30*time.Millisecond)
	s.Set(slog.LevelDebug, time.Hour)
	time.Sleep(80 * time.Millisecond)
	if lvl := s.State().Level; lvl != slog.LevelDebug {
		t.Errorf("an earlier timer reverted a later Set: level %v", lvl)
	}

	s.Reset()
	if st := s.State(); st.Level != slog.LevelInfo || !st.Until.IsZero() {
		t.Errorf("Reset = %+v, want info with no deadline", st)
	}

	// Asking for the install's level is a reset, and the duration is clamped.
	s.Set(slog.LevelDebug, 10*time.Hour)
	if left := time.Until(s.State().Until); left > MaxDebugFor || left < MaxDebugFor-time.Minute {
		t.Errorf("a 10h request should be clamped to %v, got %v", MaxDebugFor, left)
	}
	s.Set(slog.LevelInfo, time.Hour)
	if st := s.State(); st.Level != slog.LevelInfo || !st.Until.IsZero() {
		t.Errorf("setting the install's level = %+v, want a reset", st)
	}
	if left := time.Until(s.Set(slog.LevelDebug, 0)); left > DefaultDebugFor || left < DefaultDebugFor-time.Minute {
		t.Errorf("no duration should mean %v, got %v", DefaultDebugFor, left)
	}
	s.Reset()
}

// The switch is what the ring's handler asks, so debug lines start reaching the Logs page
// the moment it's raised and stop when it drops back.
func TestDebugLinesCapturedWhenEnabled(t *testing.T) {
	ring := NewRing(100)
	sw := NewLevelSwitch(slog.LevelInfo)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: sw.Leveler()}), ring))

	log.Debug("before")
	sw.Set(slog.LevelDebug, time.Minute)
	log.Debug("during")
	sw.Reset()
	log.Debug("after")
	log.Info("info always")

	var got []string
	for _, e := range ring.Snapshot(Filter{Min: slog.LevelDebug}) {
		got = append(got, e.Message)
	}
	want := []string{"during", "info always"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("captured %q, want %q", got, want)
	}
}

// Set, Reset, the revert timer and every log call's level check run at once in the app;
// the race detector checks they don't trip over each other.
func TestLevelSwitchConcurrent(t *testing.T) {
	sw := NewLevelSwitch(slog.LevelInfo)
	ring := NewRing(1000)
	log := slog.New(NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: sw.Leveler()}), ring))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				switch (i + j) % 4 {
				case 0:
					sw.Set(slog.LevelDebug, time.Millisecond)
				case 1:
					sw.Reset()
				case 2:
					_ = sw.State()
				default:
					log.Debug("tick", "j", j)
				}
			}
		}(i)
	}
	wg.Wait()
	sw.Reset()
	if sw.State().Level != slog.LevelInfo {
		t.Error("Reset after the storm should leave the install's level")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
