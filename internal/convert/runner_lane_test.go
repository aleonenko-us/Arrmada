package convert

import (
	"context"
	"strings"
	"testing"
	"time"
)

// activeAuto puts an automatic conversion in a normal slot, frozen outside the hours — the
// job a hand request used to wait behind.
func activeAuto(t *testing.T, s *Service, movieID int64, title string) *Job {
	t.Helper()
	j := s.claim(item{Kind: "movie", MovieID: movieID}, title, false)
	if j == nil {
		t.Fatal("claim failed")
	}
	s.update(j, func(x *Job) { x.State, x.Paused = StateEncoding, "outside your encode hours — resumes at 01:00" })
	return j
}

func waitJob(t *testing.T, ch <-chan *Job, within time.Duration) *Job {
	t.Helper()
	select {
	case j := <-ch:
		return j
	case <-time.After(within):
		return nil
	}
}

// With one conversion slot, held by an automatic job frozen outside the hours, a request
// starts on the lane after one wake — and the automatic job waits for it, then may run.
func TestRequestLaneTakesRequestPastFrozenAutoJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestService(t)
	auto := activeAuto(t, s, 1, "Big 4K film")

	got := make(chan *Job, 1)
	go func() { got <- s.nextJob(ctx, requestLane) }()
	if j := waitJob(t, got, 150*time.Millisecond); j != nil {
		t.Fatalf("the lane picked %s with no request waiting", j.Title)
	}
	if err := s.requests.add(ctx, episodeKey(9, 1, 2), "Show - S01E02"); err != nil {
		t.Fatal(err)
	}
	s.wakeUp()
	j := waitJob(t, got, 3*time.Second)
	if j == nil {
		t.Fatal("the request didn't start within seconds of the wake")
	}
	if !j.lane || !j.Requested || j.SeriesID != 9 || j.Episode != 2 {
		t.Fatalf("lane job = %+v", j)
	}

	// A normal slot that frees meanwhile doesn't start an automatic file just to wait.
	set(t, s, map[string]string{keyAuto: "true"})
	indexMovie(t, s, 3, "Remux", film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	if j2 := s.pickJob(ctx); j2 != nil {
		t.Errorf("an automatic pick started while the lane is busy: %s", j2.Title)
	}

	if r := s.pauseReason(auto, prefs{}, false, s.laneBusy()); r != laneWaitReason {
		t.Errorf("the automatic job's pause = %q, want %q", r, laneWaitReason)
	}
	if r := s.pauseReason(j, prefs{start: "00:00", end: "00:01"}, false, true); r != "" {
		t.Errorf("the requested file itself must never wait: %q", r)
	}
	st := s.Status(ctx)
	if st.State != "working" || !strings.Contains(st.Message, "Converting 1 file · 1 paused") {
		t.Errorf("status = %s %q: a requested file running beside a paused automatic one is working", st.State, st.Message)
	}
	s.update(auto, func(x *Job) { x.Paused = "" })

	s.update(j, func(x *Job) { x.State = StateEncoding })
	s.finish(j, StateDone, "")
	if s.laneBusy() {
		t.Fatal("the lane must be free once the request finishes")
	}
	if r := s.pauseReason(auto, prefs{}, false, s.laneBusy()); r != "" {
		t.Errorf("inside the hours the automatic job carries on: %q", r)
	}
}

// The lane opens only when every normal slot is busy with work that includes an automatic
// conversion: a free slot takes requests itself, and requests wait their turn behind
// another request.
func TestRequestLaneOpensOnlyBehindAutoWork(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	if err := s.requests.add(ctx, movieKey(5), "Asked"); err != nil {
		t.Fatal(err)
	}
	if j := s.pickRequest(ctx); j != nil {
		t.Fatal("a free normal slot takes the request; the lane must stay shut")
	}
	other := s.claim(item{Kind: "movie", MovieID: 6}, "Asked earlier", true)
	s.update(other, func(x *Job) { x.State = StateEncoding })
	if j := s.pickRequest(ctx); j != nil {
		t.Fatal("behind another request, a request waits its turn")
	}
	s.finish(other, StateDone, "")

	activeAuto(t, s, 1, "Auto")
	j := s.pickRequest(ctx)
	if j == nil || j.MovieID != 5 {
		t.Fatalf("behind an automatic job the lane takes the request: %+v", j)
	}
	if err := s.requests.add(ctx, movieKey(7), "Another"); err != nil {
		t.Fatal(err)
	}
	if j2 := s.pickRequest(ctx); j2 != nil {
		t.Fatal("one lane, one extra encode: a second request waits")
	}

	s.SetWatching(func() bool { return true }, nil)
	s.finish(j, StateDone, "")
	if j := s.pickRequest(ctx); j != nil {
		t.Fatal("nothing starts while someone is watching Plex")
	}
}

// One wake reaches every idle worker: two requests and two free slots start together.
func TestBroadcastWakeReleasesEveryWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestService(t)
	set(t, s, map[string]string{keyWorkers: "2"})
	got := make(chan *Job, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) { got <- s.nextJob(ctx, idx) }(i)
	}
	time.Sleep(150 * time.Millisecond) // both are waiting for a wake now
	for _, k := range []string{movieKey(1), movieKey(2)} {
		if err := s.requests.add(ctx, k, k); err != nil {
			t.Fatal(err)
		}
	}
	s.wakeUp()
	for i := 0; i < 2; i++ {
		if j := waitJob(t, got, 3*time.Second); j == nil {
			t.Fatalf("worker %d of 2 slept through the wake", i+1)
		}
	}

	// The broadcast channel itself: every waiter is released by one wakeUp.
	chs := []<-chan struct{}{s.wakeCh(), s.wakeCh(), s.wakeCh()}
	s.wakeUp()
	for i, ch := range chs {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d not released", i)
		}
	}
}

// Where a request stands is said truthfully: starting, or its place and what's ahead.
func TestRequestPlace(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	// Requests run oldest first, then by key: movie:1 < movie:2 < movie:3 within a second.
	add := func(key, title string) {
		t.Helper()
		if err := s.requests.add(ctx, key, title); err != nil {
			t.Fatal(err)
		}
	}

	add(movieKey(1), "First")
	if p := s.RequestPlace(ctx, movieKey(1)); !p.Starting {
		t.Errorf("a free slot: %+v, want starting", p)
	}

	// The one slot converts an earlier request: the next one waits behind it.
	running := s.claim(item{Kind: "movie", MovieID: 1}, "First", true)
	s.update(running, func(x *Job) { x.State = StateEncoding })
	add(movieKey(2), "Second")
	add(movieKey(3), "Third")
	if p := s.RequestPlace(ctx, movieKey(2)); p.Starting || p.Position != 1 || p.Ahead != "First" {
		t.Errorf("second: %+v, want #1 in line after First", p)
	}
	if p := s.RequestPlace(ctx, movieKey(3)); p.Starting || p.Position != 2 || p.Ahead != "Second" {
		t.Errorf("third: %+v, want #2 in line after Second", p)
	}
	s.finish(running, StateDone, "")

	// Behind an automatic conversion the lane takes it at once.
	activeAuto(t, s, 9, "Auto")
	if p := s.RequestPlace(ctx, movieKey(2)); !p.Starting {
		t.Errorf("behind an automatic job: %+v, want starting", p)
	}

	s.SetWatching(func() bool { return true }, nil)
	if p := s.RequestPlace(ctx, movieKey(2)); p.Starting || !strings.Contains(p.Reason, "someone is watching") {
		t.Errorf("while watching: %+v", p)
	}
}
