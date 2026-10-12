package indexer

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"
)

// The Torznab request queue. Every indexer behind one Prowlarr shares its host, and that
// host gets at most one request per torznabRequestDelay (the spacing is what stops the
// 429s). Before, the queue was first come, first served: a person's search waited behind
// whatever a sweep had already queued, inside its own deadline, and the last indexers ran
// out of time while still waiting. Now each busy host has a small dispatcher that hands
// out one slot per delay and serves people first, while still giving sweeps a turn.

// throttleIdle is how long a host's dispatcher waits for more work before it exits.
const throttleIdle = time.Minute

// throttleLogWait is the queue wait worth a log line: real contention, not a normal
// one-second gap.
const throttleLogWait = 2 * time.Second

// backgroundEvery: after this many people's requests in a row while a sweep's request
// waits, the sweep gets the next slot, so background work is slowed, never starved.
const backgroundEvery = 5

// hostThrottle queues requests per host. The zero value is not usable; use newHostThrottle.
type hostThrottle struct {
	log func() *slog.Logger
	// delay is the minimum gap between two requests to one host, idle how long a quiet
	// host's dispatcher lingers. Fields rather than package variables, so a test can
	// shrink its own throttle's without racing other searchers' dispatchers.
	delay, idle time.Duration
	// logWait is the queue wait that gets a log line (throttleLogWait).
	logWait time.Duration
	mu      sync.Mutex
	hosts   map[string]*hostQueue
	// onGrant, when set (tests), sees every slot as it's granted, under the lock.
	onGrant func(host string, interactive bool, at time.Time)
}

func newHostThrottle(log func() *slog.Logger) *hostThrottle {
	return &hostThrottle{log: log, delay: torznabRequestDelay, idle: throttleIdle, logWait: throttleLogWait, hosts: map[string]*hostQueue{}}
}

// hostQueue is one host's two lanes and its dispatcher's state. Guarded by hostThrottle.mu.
type hostQueue struct {
	interactive []*slotWaiter
	background  []*slotWaiter
	last        time.Time // when the last slot was granted
	streak      int       // people's slots granted in a row while a sweep's request waited
	wake        chan struct{}
}

type slotWaiter struct {
	granted     chan struct{} // closed by the dispatcher
	done        bool          // granted (under the lock)
	interactive bool
}

// hostOf is the part of an endpoint the spacing is keyed on: the host, not the URL, so
// every indexer behind one Prowlarr shares one queue.
func hostOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return endpoint
}

// wait blocks until this request may go to its host, or ctx ends. A person's search
// (IsInteractive) is served before queued background ones. A waiter whose ctx ends leaves
// the queue, so the slot it would have had goes to the next one.
func (t *hostThrottle) wait(ctx context.Context, endpoint string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host := hostOf(endpoint)
	lane := "background"
	interactive := IsInteractive(ctx)
	if interactive {
		lane = "interactive"
	}
	w := &slotWaiter{granted: make(chan struct{}), interactive: interactive}
	start := time.Now()

	t.mu.Lock()
	q, ok := t.hosts[host]
	if !ok {
		q = &hostQueue{wake: make(chan struct{}, 1)}
		t.hosts[host] = q
		go t.dispatch(host, q)
	}
	if interactive {
		q.interactive = append(q.interactive, w)
	} else {
		q.background = append(q.background, w)
	}
	t.mu.Unlock()
	q.poke()

	select {
	case <-w.granted:
		if waited := time.Since(start); waited > t.logWait {
			if l := t.log(); l != nil {
				l.Info("torznab queue wait", "host", host, "lane", lane, "queue_wait_ms", waited.Milliseconds())
			}
		}
		return nil
	case <-ctx.Done():
		t.mu.Lock()
		if !w.done {
			q.interactive = remove(q.interactive, w)
			q.background = remove(q.background, w)
		}
		t.mu.Unlock()
		// A slot granted at the same moment is spent: the spacing still holds, it just
		// goes unused.
		return ctx.Err()
	}
}

func (q *hostQueue) poke() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func remove(lane []*slotWaiter, w *slotWaiter) []*slotWaiter {
	for i, x := range lane {
		if x == w {
			return append(lane[:i], lane[i+1:]...)
		}
	}
	return lane
}

// next picks who gets the coming slot: a person's request first, unless sweeps have
// waited through backgroundEvery of them in a row.
func (q *hostQueue) next() *slotWaiter {
	switch {
	case len(q.interactive) > 0 && (len(q.background) == 0 || q.streak < backgroundEvery):
		w := q.interactive[0]
		q.interactive = q.interactive[1:]
		if len(q.background) > 0 {
			q.streak++
		} else {
			q.streak = 0
		}
		return w
	case len(q.background) > 0:
		w := q.background[0]
		q.background = q.background[1:]
		q.streak = 0
		return w
	}
	return nil
}

// dispatch hands out one host's slots, at least t.delay apart, until the host has been
// idle for t.idle. The choice of who goes next is made when the slot opens,
// not when the wait began, so a person's request that arrives mid-gap still goes first.
func (t *hostThrottle) dispatch(host string, q *hostQueue) {
	idle := time.NewTimer(t.idle)
	defer idle.Stop()
	for {
		t.mu.Lock()
		empty := len(q.interactive) == 0 && len(q.background) == 0
		var gap time.Duration
		if !empty && !q.last.IsZero() {
			gap = time.Until(q.last.Add(t.delay))
		}
		if !empty && gap <= 0 {
			w := q.next()
			w.done = true
			q.last = time.Now()
			if t.onGrant != nil {
				t.onGrant(host, w.interactive, q.last)
			}
			close(w.granted)
			t.mu.Unlock()
			continue
		}
		t.mu.Unlock()

		if !empty {
			// Sleep out the gap, then choose again: the queue may have changed.
			time.Sleep(gap)
			continue
		}

		// Nothing queued: wait for work, or leave once the host has gone quiet.
		resetTimer(idle, t.idle)
		select {
		case <-q.wake:
		case <-idle.C:
			t.mu.Lock()
			if len(q.interactive) == 0 && len(q.background) == 0 && time.Since(q.last) >= t.delay {
				delete(t.hosts, host)
				t.mu.Unlock()
				return
			}
			t.mu.Unlock()
		}
	}
}

// resetTimer restarts a timer whether or not it fired and was drained.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// active reports how many hosts have a running dispatcher (for tests).
func (t *hostThrottle) active() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.hosts)
}
