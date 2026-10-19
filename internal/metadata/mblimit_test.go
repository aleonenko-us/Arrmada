package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"
)

// Twenty callers at once, on both lanes, never get requests closer together than the
// interval.
func TestMBLimiterSpacesConcurrentCallers(t *testing.T) {
	l := newMBLimiter()
	const interval = 15 * time.Millisecond
	var mu sync.Mutex
	var grants []time.Time
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		lane := MBInteractive
		if i%2 == 1 {
			lane = MBBackground
		}
		go func() {
			defer wg.Done()
			if err := l.wait(context.Background(), lane, interval); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			grants = append(grants, time.Now())
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(grants, func(i, j int) bool { return grants[i].Before(grants[j]) })
	for i := 1; i < len(grants); i++ {
		// A millisecond of slack for timer and scheduler jitter on the measuring side.
		if gap := grants[i].Sub(grants[i-1]); gap < interval-time.Millisecond {
			t.Errorf("grants %d and %d only %v apart, want at least %v", i-1, i, gap, interval)
		}
	}
	if span := grants[len(grants)-1].Sub(grants[0]); span < 19*interval-5*time.Millisecond {
		t.Errorf("20 grants took %v, want at least %v", span, 19*interval)
	}
}

// Interactive callers go before background ones that are already waiting.
func TestMBLimiterInteractiveFirst(t *testing.T) {
	l := newMBLimiter()
	const interval = 20 * time.Millisecond
	// Take the first slot so everyone below has to wait.
	if err := l.wait(context.Background(), MBInteractive, interval); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []MBLane
	var wg sync.WaitGroup
	start := func(lane MBLane) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.wait(context.Background(), lane, interval)
			mu.Lock()
			order = append(order, lane)
			mu.Unlock()
		}()
	}
	for i := 0; i < 4; i++ {
		start(MBBackground)
	}
	time.Sleep(5 * time.Millisecond) // the background callers are queued first
	for i := 0; i < 4; i++ {
		start(MBInteractive)
	}
	wg.Wait()
	// At most the slot already being handed out when the interactive callers arrived goes
	// to a background caller; after that every interactive caller precedes the rest.
	lastInteractive := 0
	for i, lane := range order {
		if lane == MBInteractive {
			lastInteractive = i
		}
	}
	if lastInteractive > 4 {
		t.Errorf("order = %v: interactive callers waited behind background ones", order)
	}
}

// A refusal rests the background lane (and only it), longer each time, up to an hour.
func TestMBLimiterPausesBackground(t *testing.T) {
	l := newMBLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	l.refused()
	if err := l.wait(context.Background(), MBBackground, 0); !errors.Is(err, ErrMusicBrainzUnavailable) {
		t.Fatalf("background while paused = %v, want unavailable", err)
	}
	if err := l.wait(context.Background(), MBInteractive, 0); err != nil {
		t.Fatalf("interactive while paused = %v, want through", err)
	}
	if l.pause != mbPauseFirst {
		t.Errorf("first pause = %v", l.pause)
	}
	for i := 0; i < 10; i++ {
		l.refused()
	}
	if l.pause != mbPauseMax {
		t.Errorf("pause after many refusals = %v, want capped at %v", l.pause, mbPauseMax)
	}
	now = now.Add(2 * mbPauseMax)
	if err := l.wait(context.Background(), MBBackground, 0); err != nil {
		t.Errorf("background after the pause = %v", err)
	}
	l.answered()
	if l.pause != 0 {
		t.Error("a good answer ends the streak")
	}
}

// The limiter is the process's, not the client's: two clients asking at once still keep
// to one gap between requests.
func TestMBLimiterSharedAcrossClients(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		_, _ = io.WriteString(w, `{"artists":[]}`)
	}))
	defer srv.Close()
	// A mirror at 20 a second: 50 ms apart.
	cfg := func() MBConfig { return MBConfig{BaseURL: srv.URL, MirrorRate: 20} }
	a, b := NewMusicBrainz(nil, cfg), NewMusicBrainz(nil, cfg)
	if a.gate != b.gate || a.gate != mbGate {
		t.Fatal("every client must share the one gate")
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		c := a
		if i%2 == 1 {
			c = b
		}
		go func(q string) {
			defer wg.Done()
			if _, err := c.SearchArtists(context.Background(), q); err != nil {
				t.Error(err)
			}
		}(string(rune('a' + i)))
	}
	wg.Wait()
	sort.Slice(at, func(i, j int) bool { return at[i].Before(at[j]) })
	if len(at) != 6 {
		t.Fatalf("requests = %d", len(at))
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < 45*time.Millisecond {
			t.Errorf("requests %d and %d %v apart, want about 50ms", i-1, i, gap)
		}
	}
}
