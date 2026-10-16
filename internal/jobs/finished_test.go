package jobs

import (
	"context"
	"errors"
	"testing"
)

// Finished runs once the job is over in every sense: no longer in Active, its final state
// stored — so a page it tells (the queue's "Importing") reads it as finished. It runs for
// a failure and for work abandoned in the queue too.
func TestFinishedRunsAfterTheJobLeavesActive(t *testing.T) {
	r, _, _ := newRunner(t)
	ctx := context.Background()
	type seen struct {
		status string
		active int
		stored string
	}
	got := make(chan seen, 1)
	finished := func(status string) {
		s := seen{status: status, active: len(r.Active())}
		if list, _ := r.List(ctx, Filter{Kind: "t.finished"}); len(list) == 1 {
			s.stored = list[0].Status
		}
		got <- s
	}
	id, _, err := r.Submit(ctx, Spec{Kind: "t.finished", Target: "x", Finished: finished,
		Fn: func(context.Context, *Progress) (any, error) { return nil, errors.New("no") }})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	s := <-got
	if s.status != StatusFailed || s.active != 0 || s.stored != StatusFailed {
		t.Fatalf("Finished saw %+v, want failed, nothing active, failed stored", s)
	}
}
