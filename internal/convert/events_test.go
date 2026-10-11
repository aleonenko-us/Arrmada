package convert

import (
	"context"
	"sync"
	"testing"
)

// recorder stands in for the event bus.
type recorder struct {
	mu     sync.Mutex
	events []struct {
		topic string
		data  any
	}
}

func (r *recorder) Publish(topic string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, struct {
		topic string
		data  any
	}{topic, data})
}

func (r *recorder) of(topic string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, e := range r.events {
		if e.topic == topic {
			out = append(out, e.data)
		}
	}
	return out
}

func newEventJob(s *Service, key string) *Job {
	job, _ := s.claimPending(key, func(id int64) *Job {
		return &Job{ID: id, Kind: "movie", MovieID: 7, Title: "Film (2020)", State: StateEncoding}
	})
	return job
}

// A finished conversion says so exactly once, with the new file and the one it replaced.
func TestConvertDonePublishedOnce(t *testing.T) {
	s := newTestService(t)
	bus := &recorder{}
	s.SetBus(bus)
	job := newEventJob(s, movieKey(7))
	s.update(job, func(j *Job) { j.SrcBytes, j.OutBytes, j.Codec = 40<<30, 15<<30, "hevc" })
	rec := s.record(job)
	rec.srcPath, rec.outPath = "/movies/Film/Film.m2ts", "/movies/Film/Film.mkv"

	s.finish(job, StateDone, "")

	done := bus.of(TopicDone)
	if len(done) != 1 {
		t.Fatalf("got %d convert.done events, want 1", len(done))
	}
	ev := done[0].(DoneEvent)
	if ev.Path != "/movies/Film/Film.mkv" || ev.OldPath != "/movies/Film/Film.m2ts" {
		t.Errorf("paths = %q ← %q", ev.Path, ev.OldPath)
	}
	if ev.SavedBytes != 25<<30 || ev.Codec != "hevc" || ev.Key != movieKey(7) || ev.MovieID != 7 {
		t.Errorf("payload = %+v", ev)
	}
	if ev.HistoryID == 0 {
		t.Error("the ledger row id is missing")
	}
	if n := len(bus.of(TopicFailed)) + len(bus.of(TopicBlocked)) + len(bus.of(TopicStalled)); n != 0 {
		t.Errorf("a success also published %d problem events", n)
	}
}

// recordFailure counts, and only the failure that reaches maxFailures blocks the file.
func TestConvertBlockedAtMaxFailuresOnly(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for want := 1; want <= 4; want++ {
		if got := s.failures.recordFailure(ctx, "movie:99", "boom"); got != want {
			t.Fatalf("recordFailure #%d returned %d", want, got)
		}
	}

	bus := &recorder{}
	s.SetBus(bus)
	for i := 1; i <= maxFailures+1; i++ {
		job := newEventJob(s, movieKey(7))
		s.finish(job, StateFailed, "ffmpeg exited with status 1")
		if got := len(bus.of(TopicBlocked)); (i < maxFailures && got != 0) || (i >= maxFailures && got != 1) {
			t.Fatalf("after failure %d: %d convert.blocked events", i, got)
		}
	}
	if got := len(bus.of(TopicFailed)); got != maxFailures+1 {
		t.Errorf("convert.failed fired %d times for %d failures", got, maxFailures+1)
	}
	ev := bus.of(TopicBlocked)[0].(BlockedEvent)
	if ev.Count != maxFailures || ev.Key != movieKey(7) || ev.Title != "Film (2020)" {
		t.Errorf("blocked payload = %+v", ev)
	}

	// A transient failure (a full disk) never counts, so never blocks.
	bus2 := &recorder{}
	s.SetBus(bus2)
	for i := 0; i < maxFailures+1; i++ {
		job := newEventJob(s, movieKey(8))
		s.finish(job, StateFailed, "No space left on device")
	}
	if got := len(bus2.of(TopicBlocked)); got != 0 {
		t.Errorf("transient failures blocked the file (%d events)", got)
	}
	if ev := bus2.of(TopicFailed)[0].(FailedEvent); !ev.Transient {
		t.Error("a full-disk failure wasn't marked transient")
	}
}

// A full scratch disk says so once per run of the condition, not on every retry.
func TestConvertStalledOnFirstAttemptOnly(t *testing.T) {
	s := newTestService(t)
	bus := &recorder{}
	s.SetBus(bus)
	for i := 0; i < 3; i++ {
		job := newEventJob(s, movieKey(7))
		s.noteStall(job, 91<<30, 40<<30)
		s.finishSkip(job, SkipNoScratch, "needs ~91 GB of scratch")
	}
	stalled := bus.of(TopicStalled)
	if len(stalled) != 1 {
		t.Fatalf("got %d convert.stalled events for 3 retries, want 1", len(stalled))
	}
	ev := stalled[0].(StalledEvent)
	if ev.Reason != SkipNoScratch || ev.NeedBytes != 91<<30 || ev.FreeBytes != 40<<30 {
		t.Errorf("stalled payload = %+v", ev)
	}

	// A different reason in between starts a new run: the next stall is announced again.
	job := newEventJob(s, movieKey(7))
	s.finishSkip(job, SkipHardlinked, "still seeding")
	job = newEventJob(s, movieKey(7))
	s.finishSkip(job, SkipLibraryFull, "library disk full")
	if got := len(bus.of(TopicStalled)); got != 2 {
		t.Errorf("a new run of a space problem: %d stalled events, want 2", got)
	}
	// Not a space problem: never a stall.
	job = newEventJob(s, movieKey(9))
	s.finishSkip(job, SkipNotSmaller, "only 5% smaller")
	if got := len(bus.of(TopicStalled)); got != 2 {
		t.Errorf("a not-smaller skip published a stall")
	}
}

// No bus wired: nothing panics.
func TestConvertEventsWithoutBus(t *testing.T) {
	s := newTestService(t)
	job := newEventJob(s, movieKey(7))
	s.finish(job, StateDone, "")
	job = newEventJob(s, movieKey(7))
	s.finishSkip(job, SkipNoScratch, "full")
}
