package jobs

import (
	"context"
	"testing"
	"time"
)

// Release previews wait for their per-minute budget in a lane of their own, one at a time:
// a preview waiting there never holds a slot other work needs.
func TestRequestPreviewClassIsItsOwnLane(t *testing.T) {
	r, _, _ := newRunner(t)
	release := make(chan struct{})
	started := make(chan string, 3)
	mk := func(kind, target, class string) Spec {
		return Spec{Kind: kind, Target: target, Class: class, Fn: func(ctx context.Context, p *Progress) (any, error) {
			started <- kind + ":" + target
			<-release
			return nil, nil
		}}
	}
	p1, _, _ := r.Submit(context.Background(), mk("request.preview", "request:1", ClassRequestPreview))
	if got := <-started; got != "request.preview:request:1" {
		t.Fatalf("started %s", got)
	}
	p2, _, _ := r.Submit(context.Background(), mk("request.preview", "request:2", ClassRequestPreview))
	other, _, _ := r.Submit(context.Background(), mk("other.work", "x", ClassOther))
	if got := <-started; got != "other.work:x" {
		t.Fatalf("started %s; other work must not wait behind a preview", got)
	}
	select {
	case s := <-started:
		t.Fatalf("%s started while a preview held the lane", s)
	case <-time.After(50 * time.Millisecond):
	}
	if j, _ := r.Get(context.Background(), p2); j.Status != StatusQueued {
		t.Fatalf("second preview = %s, want queued", j.Status)
	}
	close(release)
	for _, id := range []int64{p1, p2, other} {
		if j := waitJob(t, r, id); j.Status != StatusSucceeded {
			t.Fatalf("job = %+v", j)
		}
	}
}
