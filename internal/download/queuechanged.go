package download

import (
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/eventbus"
)

// TopicQueueChanged says the queue's shape moved: a torrent arrived, left, finished, or
// a finished one was paused or errored. queue.progress only covers unfinished downloads,
// so a seeding torrent being removed or paused never showed on it; pages refetch on this
// instead of polling. Staff-only under the realtime policy, and hashes only — no names.
const TopicQueueChanged = eventbus.TopicQueueChanged

// QueueDelta is the queue.changed payload. Each list is sorted and may be empty.
type QueueDelta struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"` // finished, or a finished one paused / errored / back
}

func (d QueueDelta) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// shapeClass is what queue.changed compares per torrent. Every unfinished phase is one
// class: queue.progress already carries those, and a torrent flapping between
// downloading and stalled (or queued) every few seconds would otherwise make every open
// list refetch on each flap. A finished torrent is seeding, paused or errored — again
// folding qBittorrent's own seed-queue flapping (queuedUP ↔ uploading) into one class.
func shapeClass(it Item) string {
	if !it.Complete() {
		return "unfinished"
	}
	switch p := it.Phase(); p {
	case "paused", "error":
		return p
	}
	return "seeding"
}

// queueShape is each torrent's class, keyed by lower-cased hash.
func queueShape(items []Item) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		if it.Hash == "" {
			continue
		}
		out[strings.ToLower(it.Hash)] = shapeClass(it)
	}
	return out
}

// diffQueue is what moved between two reads. Progress isn't a change here (that's
// queue.progress's job, and it would make this fire every tick).
func diffQueue(prev, next map[string]string) QueueDelta {
	d := QueueDelta{Added: []string{}, Removed: []string{}, Changed: []string{}}
	for h, phase := range next {
		old, ok := prev[h]
		switch {
		case !ok:
			d.Added = append(d.Added, h)
		case old != phase:
			d.Changed = append(d.Changed, h)
		}
	}
	for h := range prev {
		if _, ok := next[h]; !ok {
			d.Removed = append(d.Removed, h)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	return d
}

// shapePublisher remembers the queue's last shape and speaks when it moves.
type shapePublisher struct {
	pub  Publisher
	last map[string]string // nil = no baseline yet
}

// tick compares a read with the last one. The first read after (re)arming only sets the
// baseline: whoever just connected loaded the page moments ago. A partial read (a client
// didn't answer) is skipped entirely — its torrents would look removed, and the next full
// read compares against the last full one instead.
func (p *shapePublisher) tick(snap Snapshot) {
	if snap.Err != nil || !snap.Complete {
		return
	}
	next := queueShape(snap.Items)
	if p.last == nil {
		p.last = next
		return
	}
	d := diffQueue(p.last, next)
	p.last = next
	if !d.empty() {
		p.pub.Publish(TopicQueueChanged, d)
	}
}

// reset forgets the baseline (nobody is listening; the next listener loads fresh).
func (p *shapePublisher) reset() { p.last = nil }
