package indexer

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// INT-21: the per-host Torznab queue serves people's searches first, keeps every two
// requests to one host at least the delay apart, gives sweeps a turn, frees a cancelled
// waiter's slot and shuts its dispatcher down when the host goes quiet.

const testHost = "http://prowlarr:9696/1/api?t=search"

type grant struct {
	interactive bool
	at          time.Time
}

// testThrottle is a throttle with its own short delay that records every grant.
func testThrottle(t *testing.T, delay time.Duration) (*hostThrottle, func() []grant) {
	t.Helper()
	var mu sync.Mutex
	var grants []grant
	th := newHostThrottle(func() *slog.Logger { return nil })
	th.delay, th.idle = delay, time.Minute
	th.onGrant = func(_ string, interactive bool, at time.Time) {
		mu.Lock()
		grants = append(grants, grant{interactive, at})
		mu.Unlock()
	}
	return th, func() []grant {
		mu.Lock()
		defer mu.Unlock()
		return append([]grant(nil), grants...)
	}
}

// queued reports how many waiters each lane of the test host holds.
func (t *hostThrottle) queued(endpoint string) (interactive, background int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if q, ok := t.hosts[hostOf(endpoint)]; ok {
		return len(q.interactive), len(q.background)
	}
	return 0, 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// enqueue starts n waiters in one lane and waits until they're all queued.
func enqueue(t *testing.T, th *hostThrottle, ctx context.Context, interactive bool, n int, wg *sync.WaitGroup) {
	t.Helper()
	if interactive {
		ctx = WithInteractive(ctx)
	}
	fg0, bg0 := th.queued(testHost)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = th.wait(ctx, testHost)
		}()
	}
	waitFor(t, "waiters queued", func() bool {
		fg, bg := th.queued(testHost)
		if interactive {
			return fg >= fg0+n
		}
		return bg >= bg0+n
	})
}

func lanes(gs []grant) string {
	var b strings.Builder
	for _, g := range gs {
		if g.interactive {
			b.WriteByte('I')
		} else {
			b.WriteByte('B')
		}
	}
	return b.String()
}

func TestThrottleInteractiveGoesNext(t *testing.T) {
	th, grants := testThrottle(t, 300*time.Millisecond)
	ctx := context.Background()
	// The first request goes at once and starts the gap.
	if err := th.wait(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	enqueue(t, th, ctx, false, 5, &wg)
	enqueue(t, th, ctx, true, 1, &wg)
	wg.Wait()
	if got := lanes(grants()); got != "BIBBBBB" {
		t.Fatalf("grant order = %s, want the person's request right after the first", got)
	}
}

func TestThrottleSpacing(t *testing.T) {
	const delay = 25 * time.Millisecond
	th, grants := testThrottle(t, delay)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := ctx
			if i%3 == 0 {
				c = WithInteractive(ctx)
			}
			_ = th.wait(c, testHost)
		}(i)
	}
	wg.Wait()
	gs := grants()
	if len(gs) != 10 {
		t.Fatalf("%d grants, want 10", len(gs))
	}
	for i := 1; i < len(gs); i++ {
		if d := gs[i].at.Sub(gs[i-1].at); d < delay {
			t.Fatalf("grants %d and %d only %v apart", i-1, i, d)
		}
	}
}

func TestThrottleBackgroundNotStarved(t *testing.T) {
	th, grants := testThrottle(t, 100*time.Millisecond)
	ctx := context.Background()
	if err := th.wait(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	enqueue(t, th, ctx, false, 1, &wg)
	enqueue(t, th, ctx, true, 8, &wg)
	wg.Wait()
	// The first grant primed the gap; then five people's requests, then the sweep's.
	if got := lanes(grants())[1:]; got != "IIIIIBIII" {
		t.Fatalf("grant order = %s, want the sweep served after 5 people's requests", got)
	}
}

func TestThrottleCancelledWaiterFreesSlot(t *testing.T) {
	const delay = 150 * time.Millisecond
	th, grants := testThrottle(t, delay)
	ctx := context.Background()
	if err := th.wait(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	gone := make(chan error, 1)
	go func() { gone <- th.wait(cctx, testHost) }()
	waitFor(t, "first waiter queued", func() bool { _, bg := th.queued(testHost); return bg == 1 })
	var wg sync.WaitGroup
	enqueue(t, th, ctx, false, 1, &wg)
	cancel()
	if err := <-gone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %v", err)
	}
	wg.Wait()
	gs := grants()
	if len(gs) != 2 {
		t.Fatalf("%d grants, want 2 (the cancelled one never granted)", len(gs))
	}
	// The second waiter took the cancelled one's slot: one gap after the first grant, not two.
	if d := gs[1].at.Sub(gs[0].at); d >= 2*delay {
		t.Fatalf("second grant came %v after the first; the cancelled slot wasn't reused", d)
	}
}

func TestThrottleCancelledBeforeQueueing(t *testing.T) {
	th, grants := testThrottle(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := th.wait(ctx, testHost); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait = %v", err)
	}
	if len(grants()) != 0 || th.active() != 0 {
		t.Fatal("a cancelled request still took a slot or started a dispatcher")
	}
}

func TestThrottleIdleDispatcherExits(t *testing.T) {
	th, _ := testThrottle(t, 5*time.Millisecond)
	th.idle = 30 * time.Millisecond
	if err := th.wait(context.Background(), testHost); err != nil {
		t.Fatal(err)
	}
	if err := th.wait(context.Background(), "http://other-host/api"); err != nil {
		t.Fatal(err)
	}
	if th.active() != 2 {
		t.Fatalf("active = %d, want a dispatcher per host", th.active())
	}
	waitFor(t, "idle dispatchers to exit", func() bool { return th.active() == 0 })
	// A request after that starts a fresh dispatcher and still goes at once.
	start := time.Now()
	if err := th.wait(context.Background(), testHost); err != nil || time.Since(start) > time.Second {
		t.Fatalf("after idle exit: %v in %v", err, time.Since(start))
	}
}

func TestThrottleKeyedByHost(t *testing.T) {
	th, _ := testThrottle(t, time.Second)
	ctx := context.Background()
	// Two indexers behind one Prowlarr share its queue; another host doesn't wait for it.
	if err := th.wait(ctx, "http://prowlarr:9696/1/api"); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := th.wait(short, "http://prowlarr:9696/2/api"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same host = %v, want it to wait", err)
	}
	if err := th.wait(ctx, "http://jackett:9117/api"); err != nil {
		t.Fatal(err)
	}
}

func TestThrottleLogsLongWaits(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	th := newHostThrottle(func() *slog.Logger { return logger })
	th.delay, th.logWait = 40*time.Millisecond, 10*time.Millisecond
	ctx := context.Background()
	_ = th.wait(ctx, testHost)
	_ = th.wait(WithInteractive(ctx), testHost)
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "queue_wait_ms=") || !strings.Contains(out, "host=prowlarr:9696") || !strings.Contains(out, "lane=interactive") {
		t.Fatalf("log = %q", out)
	}
	if strings.Count(out, "torznab queue wait") != 1 {
		t.Fatalf("the first, unqueued request was logged too: %q", out)
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// The fan-out gives a Torznab searcher only the search's own deadline (its queue wait
// mustn't use up a per-indexer one); every other kind keeps perIndexerTimeout.
func TestIndexerCtxLeavesTheQueueOutsideTheDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pd, _ := parent.Deadline()

	tz, c1 := indexerCtx(parent, NewTorznabSearcher())
	defer c1()
	if d, ok := tz.Deadline(); !ok || !d.Equal(pd) {
		t.Fatalf("torznab deadline = %v, want the search's own %v", d, pd)
	}
	other, c2 := indexerCtx(parent, NewX1337Searcher(nil))
	defer c2()
	if d, _ := other.Deadline(); !d.Before(pd) || time.Until(d) > perIndexerTimeout {
		t.Fatalf("1337x deadline = %v, want perIndexerTimeout", d)
	}
	none, c3 := indexerCtx(parent, nil)
	defer c3()
	if d, _ := none.Deadline(); !d.Before(pd) {
		t.Fatal("no searcher should still get perIndexerTimeout")
	}
}
