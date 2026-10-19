package attention

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
)

// Needs you lists a stalled download by the Activity queue's own rule (automation.QueueStall)
// with its one-hour threshold, so everything it lists is also under the queue's Stalled
// pill: a grab by its stall clock, against the latest sign of life. One resumed a moment
// ago (its clock held while paused), or one whose grab clock says it's stuck though
// qBittorrent calls it downloading, are judged the same way the queue judges them.
func TestStalledFollowsTheQueueRule(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	item := func(hash, raw string, last time.Duration) download.Item {
		return download.Item{Hash: hash, Name: hash, State: "downloading", RawState: raw, Progress: 0.2, RemainingBytes: 9,
			LastActivity: ago(last).Unix()}
	}
	frame := Frame{OK: true, Whole: true, Queue: []download.Item{
		item("dead", "stalledDL", 3*time.Hour),      // grab clock 2h: stalled for 2h
		item("resumed", "stalledDL", 8*time.Hour),   // grab clock held while paused: a minute old
		item("stuck", "downloading", 3*time.Hour),   // the client says downloading, the clock 90m
		item("recent", "stalledDL", 40*time.Minute), // the queue calls it stalled; not an hour yet
	}, Acq: map[string]automation.Acquisition{
		"dead":    {Title: "Dead", Status: "grabbed", ProgressAt: ago(2 * time.Hour)},
		"resumed": {Title: "Resumed", Status: "grabbed", ProgressAt: ago(time.Minute)},
		"stuck":   {Title: "Stuck", Status: "grabbed", ProgressAt: ago(90 * time.Minute)},
		"recent":  {Title: "Recent", Status: "grabbed", ProgressAt: ago(3 * time.Hour)},
	}}
	s := New(staticFrame(frame), nil, quiet, Downloads())
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range s.Current().Items {
		if it.Kind == KindStalled {
			got[it.Hash] = true
		}
	}
	if !got["dead"] || !got["stuck"] || got["resumed"] || got["recent"] || len(got) != 2 {
		t.Fatalf("stalled items = %v, want dead and stuck", got)
	}
}
