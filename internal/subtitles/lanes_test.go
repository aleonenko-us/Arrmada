package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateAI is a whisper that doesn't finish until the test opens the gate (or the job is
// stopped), and then writes the sidecar.
type gateAI struct {
	gate    chan struct{}
	started chan string // the video each run starts on
	mu      sync.Mutex
	calls   []string
}

func newGateAI() *gateAI {
	return &gateAI{gate: make(chan struct{}), started: make(chan string, 8)}
}

func (a *gateAI) available() bool  { return true }
func (a *gateAI) canRun(bool) bool { return true }
func (a *gateAI) Backend() string  { return "cpu" }
func (a *gateAI) Device() string   { return "" }
func (a *gateAI) generate(ctx context.Context, _, video, srt, lang, _ string, _ bool, _ int, _ func(int)) error {
	a.mu.Lock()
	a.calls = append(a.calls, lang)
	a.mu.Unlock()
	a.started <- video
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.gate:
	}
	return os.WriteFile(srt, []byte(fakeSRT), 0o644)
}

// laneFixture is a Service with the lanes on and two movies: 1 has only English audio
// (it needs whisper), 2 has an embedded English text track (extract only). The kept
// languages are langs. Nothing real is probed, extracted or transcribed.
func laneFixture(t *testing.T, langs string, ai aiRunner) (*Service, map[int64]string) {
	t.Helper()
	s, _ := ladderFixture(t, langs, nil, nil, ai)
	s.jobsDB = newJobStore(s.ledger.db)
	s.lanes = true
	dir := t.TempDir()
	videos := map[int64]string{}
	for id, name := range map[int64]string{1: "Heat (1995).mkv", 2: "Ran (1985).mkv", 3: "Alien (1979).mkv"} {
		videos[id] = filepath.Join(dir, name)
		if err := os.WriteFile(videos[id], []byte("not really a video"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.resolve = func(_ context.Context, j *Job) (fileRef, bool) {
		return fileRef{Path: videos[j.MovieID], Title: strings.TrimSuffix(filepath.Base(videos[j.MovieID]), ".mkv"), MediaKey: j.key()}, true
	}
	s.probe = func(_ context.Context, path string) (*mediaInfo, error) {
		mi := &mediaInfo{DurationSec: 3600, AudioLangs: []string{"eng"}, Audio: []AudioTrack{{Index: 0, Lang: "eng"}}}
		if path == videos[2] {
			mi.Subs = []SubTrack{{Index: 0, Codec: "subrip", Lang: "eng", Text: true}}
		}
		if path == videos[3] {
			mi.Subs = []SubTrack{{Index: 0, Codec: "subrip", Lang: "fre", Text: true}}
		}
		return mi, nil
	}
	s.extract = func(_ context.Context, _ string, picks []extractPick) error {
		for _, p := range picks {
			if err := os.WriteFile(p.Path, []byte(fakeSRT), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	return s, videos
}

// waitState polls a job until it reaches want.
func waitState(t *testing.T, s *Service, id int64, want JobState, within time.Duration) Job {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		j, ok, err := s.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && j.State == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d is %q (%s), want %q", id, j.State, j.Note, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startWorkers runs one worker per lane until the test ends.
func startWorkers(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, lane := range []string{LaneFast, LaneGPU} {
		wg.Add(1)
		go func() { defer wg.Done(); s.work(ctx, lane) }()
	}
	t.Cleanup(func() { cancel(); wg.Wait() })
}

// While whisper works through a long file, a newly imported file with an embedded English
// track still gets its subtitle straight away.
func TestFastLaneRunsDuringAI(t *testing.T) {
	ai := newGateAI()
	s, videos := laneFixture(t, "en", ai)
	startWorkers(t, s)
	long := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat", Priority: PrioSweep})
	select {
	case <-ai.started:
	case <-time.After(5 * time.Second):
		t.Fatal("whisper never started on the long file")
	}
	quick := s.enqueue(&Job{Kind: "movie", MovieID: 2, Title: "Ran", Priority: PrioImport})
	got := waitState(t, s, quick.ID, StateDone, 5*time.Second)
	if got.Note != "en: extracted" {
		t.Errorf("quick job note = %q", got.Note)
	}
	assertSidecar(t, videos[2], "en")
	if j, _, _ := s.Job(context.Background(), long.ID); j.State != StateRunning || j.Lane != LaneGPU {
		t.Errorf("long job = %s in %s lane, want still running in the GPU lane", j.State, j.Lane)
	}
	close(ai.gate)
	done := waitState(t, s, long.ID, StateDone, 5*time.Second)
	if done.Note != "en: AI-generated" {
		t.Errorf("long job note = %q", done.Note)
	}
	assertSidecar(t, videos[1], "en")
}

// A job whose fast rungs leave a language for whisper moves to the GPU lane with only
// that language, keeping its id and what it did so far; the run's attempts are recorded
// once each, and the job ends with the whole story.
func TestFastJobSpawnsGPUJobForLeftovers(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true}
	s, videos := laneFixture(t, "en,fr", ai)
	job := s.enqueue(&Job{Kind: "movie", MovieID: 3, Title: "Alien", Priority: PrioManual})
	if s.popLane(LaneFast) != job {
		t.Fatal("fast lane didn't hand out the job")
	}
	s.process(context.Background(), job)
	if job.State != StateQueued || job.Lane != LaneGPU || len(job.Langs) != 1 || job.Langs[0] != "en" {
		t.Fatalf("after the fast lane: state=%s lane=%s langs=%v, want queued in the GPU lane for en", job.State, job.Lane, job.Langs)
	}
	if len(ai.calls) != 0 {
		t.Fatalf("whisper ran in the fast lane: %v", ai.calls)
	}
	assertSidecar(t, videos[3], "fr")
	if job.AudioSec != 3600 || len(job.Outcomes) != 1 || job.Outcomes[0].Result != ResultOK {
		t.Errorf("handed-off job: audio=%d outcomes=%+v", job.AudioSec, job.Outcomes)
	}
	// The move is saved: a restart brings it back in the GPU lane.
	b := bootQueue(t, s.ledger.db)
	if j := b.popLane(LaneGPU); j == nil || j.ID != job.ID || j.Langs[0] != "en" || len(j.Outcomes) != 1 {
		t.Errorf("after restart the GPU lane holds %+v", j)
	}

	if got := s.popLane(LaneFast); got != nil {
		t.Errorf("fast lane still holds %+v", got)
	}
	// The fast worker's turn ends (done()); then the GPU worker takes it.
	s.mu.Lock()
	s.releaseLocked(job)
	s.mu.Unlock()
	if s.popLane(LaneGPU) != job {
		t.Fatal("GPU lane didn't hand out the job")
	}
	s.process(context.Background(), job)
	if job.State != StateDone || job.Note != "en: AI-generated · fr: extracted" {
		t.Errorf("final: state=%s note=%q", job.State, job.Note)
	}
	if len(ai.calls) != 1 || ai.calls[0] != "en/transcribe" {
		t.Errorf("whisper calls = %v, want one en transcription", ai.calls)
	}
	atts, err := s.ledger.Attempts(context.Background(), AttemptQuery{RunID: job.RunID, Limit: 10})
	if err != nil || len(atts) != 2 {
		t.Fatalf("attempts for the run = %+v (%v), want fr extract + en AI once each", atts, err)
	}
}

// Without a model whisper wouldn't run, so there's nothing to wait for: the fast lane
// records why and the job ends there.
func TestNoHandOffWhenAICantRun(t *testing.T) {
	ai := &fakeAI{avail: false}
	s, _ := laneFixture(t, "en", ai)
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat"})
	s.popLane(LaneFast)
	s.process(context.Background(), job)
	if job.Lane != LaneFast || job.State == StateQueued || !strings.Contains(job.Note, "no whisper model") {
		t.Errorf("lane=%s state=%s note=%q, want it finished in the fast lane", job.Lane, job.State, job.Note)
	}
}

// No two jobs work on one file at once, whichever lane they're in; other files go ahead.
func TestNoConcurrentJobsSameFile(t *testing.T) {
	s, _ := laneFixture(t, "en", &fakeAI{avail: true, transcribe: true})
	a := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat"})
	s.popLane(LaneFast)
	if !s.toGPU(context.Background(), a, []string{"en"}, nil, 3600) {
		t.Fatal("toGPU refused")
	}
	s.mu.Lock()
	s.releaseLocked(a) // the fast worker's turn is over
	s.mu.Unlock()
	// A redo of the same file queues in the fast lane while the AI part waits.
	redo := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat", Redo: true, Priority: PrioImport})
	if redo == a {
		t.Fatal("the redo was folded into the GPU job")
	}
	other := s.enqueue(&Job{Kind: "movie", MovieID: 2, Title: "Ran", Priority: PrioSweep})
	if got := s.popLane(LaneGPU); got != a {
		t.Fatalf("GPU pop = %+v, want the AI job", got)
	}
	// Heat is busy in the GPU lane: the fast lane skips its redo and takes Ran.
	if got := s.popLane(LaneFast); got != other {
		t.Fatalf("fast pop while Heat is busy = %+v, want Ran", got)
	}
	if got := s.popLane(LaneFast); got != nil {
		t.Fatalf("fast pop = %+v, want nothing it may take", got)
	}
	_, done := s.startJob(context.Background(), a)
	s.finish(a, StateDone, "en: AI-generated")
	done()
	if got := s.popLane(LaneFast); got != redo {
		t.Errorf("fast pop after the AI job = %+v, want the redo", got)
	}
}

// Stopping the GPU job leaves the fast lane's job running, and clearing the AI backlog
// leaves the fast lane's queue alone.
func TestStopAndClearArePerLane(t *testing.T) {
	s, _ := laneFixture(t, "en", &fakeAI{avail: true, transcribe: true})
	g := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat"})
	s.popLane(LaneFast)
	s.toGPU(context.Background(), g, []string{"en"}, nil, 3600)
	s.mu.Lock()
	s.releaseLocked(g)
	s.mu.Unlock()
	f := s.enqueue(&Job{Kind: "movie", MovieID: 2, Title: "Ran"})
	s.popLane(LaneGPU)
	s.popLane(LaneFast)
	gctx, gdone := s.startJob(context.Background(), g)
	fctx, fdone := s.startJob(context.Background(), f)
	defer gdone()
	defer fdone()
	s.markRunning(g)
	s.markRunning(f)
	if err := s.Cancel(g.ID); err != nil {
		t.Fatal(err)
	}
	if gctx.Err() == nil || fctx.Err() != nil {
		t.Errorf("after stopping the GPU job: gpu ctx err=%v, fast ctx err=%v", gctx.Err(), fctx.Err())
	}

	queuedGPU := s.enqueue(&Job{Kind: "movie", MovieID: 3, Title: "Alien"})
	s.mu.Lock()
	s.dropPendingLocked(queuedGPU)
	delete(s.active, queuedGPU.activeKey())
	queuedGPU.Lane = LaneGPU
	s.active[queuedGPU.activeKey()] = queuedGPU
	s.pushLocked(queuedGPU)
	s.mu.Unlock()
	queuedFast := s.enqueue(&Job{Kind: "episode", SeriesID: 4, Season: 1, Episode: 1, Title: "Show"})
	if n := s.ClearLane(LaneGPU); n != 1 {
		t.Errorf("cleared %d from the AI backlog, want 1", n)
	}
	if queuedGPU.State != StateCancelled || queuedFast.State != StateQueued {
		t.Errorf("gpu=%s fast=%s, want only the GPU job cleared", queuedGPU.State, queuedFast.State)
	}
}

// The AI backlog's ETA is its audio (a pass per language) over whisper's measured speed,
// which is smoothed across runs.
func TestBacklogETAFromRTF(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true}
	s, _ := laneFixture(t, "en,fr", ai)
	ctx := context.Background()
	s.noteRTF(ctx, "cpu", 4)
	s.noteRTF(ctx, "cpu", 2) // 4 + 0.3·(2−4) = 3.4
	a := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Heat"})
	b := s.enqueue(&Job{Kind: "movie", MovieID: 2, Title: "Ran"})
	s.popLane(LaneFast)
	s.popLane(LaneFast)
	s.toGPU(ctx, a, []string{"en"}, nil, 3600)
	s.toGPU(ctx, b, []string{"en", "fr"}, nil, 1800)
	bl := s.GPUBacklog(ctx)
	if bl.Files != 2 || bl.AudioSec != 7200 {
		t.Fatalf("backlog = %+v, want 2 files and 7200 s of audio", bl)
	}
	if bl.RTF < 3.39 || bl.RTF > 3.41 || bl.ETASec != int64(7200/bl.RTF) {
		t.Errorf("rtf=%.3f eta=%d, want 3.4 and about 2118", bl.RTF, bl.ETASec)
	}
	// Half way through the first, the backlog shrinks.
	s.mu.Lock()
	s.releaseLocked(a)
	s.mu.Unlock()
	s.popLane(LaneGPU)
	s.markRunning(a)
	s.update(a, func(j *Job) { j.Progress = 50 })
	if bl := s.GPUBacklog(ctx); bl.AudioSec != 5400 {
		t.Errorf("backlog with the first half done = %d s, want 5400", bl.AudioSec)
	}
	// Nothing measured yet: no ETA rather than a made-up one.
	s2, _ := laneFixture(t, "en", ai)
	if bl := s2.GPUBacklog(ctx); bl.ETASec != 0 || bl.RTF != 0 {
		t.Errorf("unmeasured backlog = %+v, want no ETA", bl)
	}
}
