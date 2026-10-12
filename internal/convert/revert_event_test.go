package convert

import (
	"context"
	"sync"
	"testing"
)

type topicRecorder struct {
	mu     sync.Mutex
	events map[string][]any
}

func (r *topicRecorder) Publish(topic string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.events == nil {
		r.events = map[string][]any{}
	}
	r.events[topic] = append(r.events[topic], data)
}

// A revert announces convert.reverted with both paths, once.
func TestRevertPublishesReverted(t *testing.T) {
	r, src, e := convertedFilm(t, "Film.mp4")
	bus := &topicRecorder{}
	r.SetBus(bus)
	if _, err := r.Revert(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	got := bus.events[TopicReverted]
	if len(got) != 1 {
		t.Fatalf("convert.reverted published %d times", len(got))
	}
	ev, ok := got[0].(RevertedEvent)
	if !ok || ev.Key != movieKey(1) || ev.Path != src || ev.OldPath != finalPathFor(src) || ev.HistoryID != e.ID {
		t.Fatalf("event %+v", got[0])
	}
}
