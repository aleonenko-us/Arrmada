package download

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiffQueue(t *testing.T) {
	prev := map[string]string{"a": "downloading", "b": "seeding", "c": "paused"}
	next := map[string]string{"a": "downloading", "b": "paused", "d": "queued"}
	got := diffQueue(prev, next)
	want := QueueDelta{Added: []string{"d"}, Removed: []string{"c"}, Changed: []string{"b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffQueue = %+v, want %+v", got, want)
	}
	if !diffQueue(prev, prev).empty() {
		t.Fatal("an unchanged queue should give no delta")
	}
}

type topicPub struct {
	mu  sync.Mutex
	got map[string][]any
}

func (p *topicPub) Publish(topic string, data any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.got == nil {
		p.got = map[string][]any{}
	}
	p.got[topic] = append(p.got[topic], data)
}

func (p *topicPub) n(topic string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.got[topic])
}

func (p *topicPub) first(topic string) any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.got[topic][0]
}

func seeding(hash string) Item {
	return Item{Hash: hash, Progress: 1, State: "seeding", RawState: "uploading"}
}

func TestShapePublisher(t *testing.T) {
	pub := &topicPub{}
	p := &shapePublisher{pub: pub}
	full := func(items ...Item) Snapshot { return Snapshot{Items: items, Complete: true} }

	p.tick(full(seeding("a"), dl("b", 0.2)))
	if pub.n(TopicQueueChanged) != 0 {
		t.Fatal("the first read only sets the baseline")
	}
	// Progress alone isn't a shape change.
	p.tick(full(seeding("a"), dl("b", 0.6)))
	if pub.n(TopicQueueChanged) != 0 {
		t.Fatal("a progress move published queue.changed")
	}
	// A seeding torrent removed: queue.progress never saw it; this must.
	p.tick(full(dl("b", 0.6)))
	if pub.n(TopicQueueChanged) != 1 {
		t.Fatalf("removal: %d publishes, want 1", pub.n(TopicQueueChanged))
	}
	if d := pub.first(TopicQueueChanged).(QueueDelta); !reflect.DeepEqual(d.Removed, []string{"a"}) {
		t.Errorf("removed = %v", d.Removed)
	}
	// A partial read (one client down) must not report its torrents as gone.
	p.tick(Snapshot{Items: nil, Complete: false})
	p.tick(Snapshot{Err: errors.New("down")})
	if pub.n(TopicQueueChanged) != 1 {
		t.Fatal("a partial or failed read published")
	}
	// Finishing (downloading -> seeding) is a phase change.
	p.tick(full(seeding("b")))
	if pub.n(TopicQueueChanged) != 2 {
		t.Fatalf("phase change: %d publishes, want 2", pub.n(TopicQueueChanged))
	}
	// After a reset the next read is a baseline again.
	p.reset()
	p.tick(full(seeding("b"), seeding("c")))
	if pub.n(TopicQueueChanged) != 2 {
		t.Fatal("published right after a reset")
	}
}

// With nobody connected the publisher must not read the queue at all — no client calls.
func TestWatcherSkipsWhenInactive(t *testing.T) {
	var reads atomic.Int32
	read := func(context.Context) (Snapshot, error) {
		reads.Add(1)
		return Snapshot{Complete: true}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	runProgress(ctx, read, &topicPub{}, func() bool { return false }, 5*time.Millisecond)
	if n := reads.Load(); n != 0 {
		t.Fatalf("inactive publisher read the queue %d times", n)
	}
}

func TestWatcherPublishes(t *testing.T) {
	var tick atomic.Int32
	read := func(context.Context) (Snapshot, error) {
		// One torrent downloading, then it finishes and a second arrives.
		if tick.Add(1) == 1 {
			return Snapshot{Items: []Item{dl("a", 0.1)}, Complete: true}, nil
		}
		return Snapshot{Items: []Item{seeding("a"), dl("b", 0)}, Complete: true}, nil
	}
	pub := &topicPub{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runProgress(ctx, read, pub, func() bool { return true }, 5*time.Millisecond); close(done) }()
	deadline := time.After(2 * time.Second)
	for pub.n(TopicQueueChanged) == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("no queue.changed published")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	d := pub.first(TopicQueueChanged).(QueueDelta)
	if !reflect.DeepEqual(d.Added, []string{"b"}) || !reflect.DeepEqual(d.Changed, []string{"a"}) {
		t.Fatalf("delta = %+v", d)
	}
	if pub.n(TopicQueueProgress) == 0 {
		t.Fatal("queue.progress wasn't published from the same read")
	}
}
