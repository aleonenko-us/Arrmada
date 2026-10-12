package convert

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A paused job's timeouts stand still: a quality check frozen through a film must not run
// out of time and cost the encode. Once the job runs again the budget counts down.
func TestPausableTimeoutOnlyCountsRunningTime(t *testing.T) {
	old := pausedTick
	pausedTick = 2 * time.Millisecond
	t.Cleanup(func() { pausedTick = old })

	s := newTestService(t)
	job := &Job{ID: 1, Paused: "someone is watching Plex"}
	ctx, cancel := s.pausableTimeout(withJob(context.Background(), job), nil, 40*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("the budget ran down while the job was paused")
	case <-time.After(200 * time.Millisecond):
	}
	s.update(job, func(j *Job) { j.Paused = "" })
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the budget never ran out once the job was running")
	}
	if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatalf("cause %v, want a deadline", context.Cause(ctx))
	}
}

// Cancelling the parent (or the returned cancel) ends it at once, with no deadline cause.
func TestPausableTimeoutCancel(t *testing.T) {
	s := newTestService(t)
	parent, stop := context.WithCancel(context.Background())
	ctx, cancel := s.pausableTimeout(parent, &Job{}, time.Hour)
	defer cancel()
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancel didn't reach the timeout context")
	}
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatal("a cancel reported as a timeout")
	}
}

// Steps below runJob find their job through the context.
func TestToolFindsTheJobInContext(t *testing.T) {
	s := newTestService(t)
	job := &Job{ID: 7}
	if jobFrom(context.Background()) != nil {
		t.Fatal("no job carried")
	}
	cmd := s.tool(withJob(context.Background(), job), nil, 0, "true")
	if cmd.OnStart == nil || cmd.OnExit == nil {
		t.Fatal("a step with a job in its context isn't registered for pausing")
	}
	if cmd := s.tool(context.Background(), nil, 0, "true"); cmd.OnStart != nil {
		t.Fatal("no job: nothing to register with")
	}
}
