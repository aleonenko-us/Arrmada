package subtitles

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/safego"
	"github.com/tristenlammi/arrmada/internal/series"
)

// JobState is the lifecycle of a subtitle-ensure job.
type JobState string

const (
	StateQueued  JobState = "queued"
	StateRunning JobState = "running"
	StateDone    JobState = "done"
	StateSkipped JobState = "skipped"
	StateFailed  JobState = "failed"
	// StateCancelled: stopped by the user, either before it ran or part-way through.
	StateCancelled JobState = "cancelled"
)

// ErrJobNotActive is returned by Cancel for a job that is already finished (or unknown).
var ErrJobNotActive = errors.New("job is not queued or running")

// Job is one file's "make sure the kept-language subtitles exist" task — the unit the Queue tab
// shows. Extraction and downloads happen inside process().
type Job struct {
	ID       int64    `json:"id"`
	Kind     string   `json:"kind"` // "movie" | "episode"
	MovieID  int64    `json:"movie_id,omitempty"`
	SeriesID int64    `json:"series_id,omitempty"`
	Season   int      `json:"season,omitempty"`
	Episode  int      `json:"episode,omitempty"`
	Title    string   `json:"title"`
	State    JobState `json:"state"`
	Note     string   `json:"note,omitempty"`
	At       int64    `json:"at"` // unix seconds queued
	// Progress is 0-100 while an AI run is in flight (extraction and downloads are too
	// quick to bother). Stays 0 for everything else.
	Progress  int    `json:"progress,omitempty"`
	Stage     string `json:"stage,omitempty"`      // what the running job is doing right now
	StartedAt int64  `json:"started_at,omitempty"` // unix seconds the worker picked it up
	// Redo ignores the sidecars already there and makes every kept language again,
	// replacing them — for when what's there is wrong (the AI read the dub, a bad
	// download) and deleting files by hand shouldn't be the only way back.
	Redo bool `json:"redo,omitempty"`
	// Priority is which line the job waits in: PrioImport, PrioManual or PrioSweep. The
	// worker always takes the lowest non-empty line first, so tonight's import isn't stuck
	// behind a sweep's worth of hour-long AI runs.
	Priority int `json:"priority"`
	// RunID names this run's rows in the attempt ledger (GET /subtitles/history?run_id=).
	RunID string `json:"run_id,omitempty"`
	// Outcomes is what each rung did for each language, in the order tried; the Note is
	// generated from them.
	Outcomes []Outcome `json:"outcomes,omitempty"`
	// Lane is the worker line the job waits in: LaneFast (extract, download) or LaneGPU
	// (whisper).
	Lane string `json:"lane"`
	// Langs limits the job to these languages; empty means every kept language.
	Langs []string `json:"langs,omitempty"`
	// AudioSec is the file's running time once known, for the AI backlog estimate.
	AudioSec   int64 `json:"audio_sec,omitempty"`
	FinishedAt int64 `json:"finished_at,omitempty"` // unix seconds it reached its final state
}

// Worker lanes.
const (
	LaneFast = "fast"
	LaneGPU  = "gpu"
)

// lane is the job's lane, fast when unset.
func (j *Job) lane() string {
	if j.Lane == "" {
		return LaneFast
	}
	return j.Lane
}

// activeKey identifies a live job: one per file and lane, the same rule the table's
// unique index keeps.
func (j *Job) activeKey() string { return j.key() + "|" + j.lane() }

// sortJobsNewestFirst orders jobs by id, newest first.
func sortJobsNewestFirst(list []*Job) {
	sort.SliceStable(list, func(a, b int) bool { return list[a].ID > list[b].ID })
}

// Queue priorities, most urgent first. Each has its own FIFO line in Service.pending.
const (
	PrioImport = 0 // a file that just landed: someone is about to watch it
	PrioManual = 1 // a button press in the UI
	PrioSweep  = 2 // the periodic catch-up sweep and "Ensure all"
	numPrios   = 3
)

// clampPrio keeps an out-of-range priority from indexing past the pending lines.
func clampPrio(p int) int {
	if p < 0 {
		return 0
	}
	if p >= numPrios {
		return numPrios - 1
	}
	return p
}

// key identifies the file a job is for, so the same file can't be queued twice.
func (j *Job) key() string {
	if j.Kind == "episode" {
		return fmt.Sprintf("e:%d:%d:%d", j.SeriesID, j.Season, j.Episode)
	}
	return fmt.Sprintf("m:%d", j.MovieID)
}

// LogLine is one entry in the Subtitles activity console.
type LogLine struct {
	At    int64  `json:"at"`
	Level string `json:"level"` // "info" | "warn" | "error"
	Msg   string `json:"msg"`
}

// Run works the queue until ctx is cancelled (start it in a goroutine): fast-lane workers
// (extract, download; subs_fast_lane_workers, 1 or 2) and one GPU-lane worker (whisper).
// A one-second extraction no longer waits behind an hour of whisper: a job whose leftover
// languages need AI moves to the GPU lane, and the fast lane carries on.
//
// The queue is slices under the mutex rather than channels. A channel send blocks once
// the buffer is full, and a worker can be inside a whisper run for many minutes — so a
// sweep that queued a few hundred files would block the scheduler goroutine, and an
// import hook would block the import, for as long as it took the worker to drain. A
// slice grows; the caller always returns immediately.
func (s *Service) Run(ctx context.Context) {
	fast := s.fastWorkers(ctx)
	s.log.Info("subtitles: workers started", "queued", s.Pending(), "fast_workers", fast)
	s.Rescan(ctx) // first library pass, in the background; the pages read it
	s.bootSweep(ctx)
	s.mu.Lock()
	s.lanes = true // the GPU worker below takes the AI part of every job from here on
	s.mu.Unlock()
	var wg sync.WaitGroup
	start := func(name, lane string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			safego.Loop(ctx, s.log, name, func(ctx context.Context) { s.work(ctx, lane) })
		}()
	}
	for i := 0; i < fast; i++ {
		start(fmt.Sprintf("subtitles: fast worker %d", i+1), LaneFast)
	}
	start("subtitles: GPU worker", LaneGPU)
	wg.Wait()
}

// work is one worker: it takes the next job from its lane, runs it, and sleeps when there
// is nothing it may take.
func (s *Service) work(ctx context.Context, lane string) {
	s.mu.Lock()
	wake := s.laneWakeLocked(laneIndex(lane))
	s.mu.Unlock()
	for {
		job := s.popLane(lane)
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		jctx, done := s.startJob(ctx, job)
		// A panic in one job fails that job and the worker moves on to the next, instead
		// of the whole app going down with it.
		if err := safego.Call(s.log, "subtitles job", func() error { s.process(jctx, job); return nil }); err != nil {
			s.finish(job, StateFailed, "internal error: "+err.Error())
		}
		done()
	}
}

// bootSweepDelay is how long after start the catch-up sweep runs.
const bootSweepDelay = 10 * time.Minute

// bootSweep runs the auto sweep once, bootSweepDelay after the worker starts. The
// scheduled sweep only comes round every six hours and keeps no last-run time, so after
// an update nothing caught up on missed work for hours; waiting ten minutes keeps the
// disks quiet while the app comes up, and backoff keeps the sweep cheap. A worker that
// restarts after a panic doesn't sweep again.
func (s *Service) bootSweep(ctx context.Context) {
	if !s.bootSwept.CompareAndSwap(false, true) {
		return
	}
	safego.Go(s.log, "subtitles: boot sweep", func() {
		t := time.NewTimer(bootSweepDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			s.AutoGrab(ctx)
		}
	})
}

// runHandle is one running job's stop lever.
type runHandle struct{ cancel context.CancelFunc }

// startJob gives a job its own cancellable context and records it as running, so Cancel
// can reach it. The returned func releases both, and the job's file for the other lane;
// call it when process returns.
func (s *Service) startJob(ctx context.Context, job *Job) (context.Context, func()) {
	jctx, cancel := context.WithCancel(ctx)
	h := &runHandle{cancel: cancel}
	s.mu.Lock()
	if s.runs == nil {
		s.runs = map[int64]*runHandle{}
	}
	s.runs[job.ID] = h
	s.mu.Unlock()
	return jctx, func() {
		cancel()
		s.mu.Lock()
		// A job handed to the GPU lane may already be running there under its own handle.
		if s.runs[job.ID] == h {
			delete(s.runs, job.ID)
		}
		s.releaseLocked(job)
		s.mu.Unlock()
		s.wakeWorkers() // a job waiting on this file can go now
	}
}

// Cancel stops one job. A queued job is dropped from the queue on the spot; a running
// job has its context cancelled, which kills the ffmpeg/whisper child process, and the
// worker then records it as cancelled — whisper writes its SRT to a temp path and only
// moves it next to the video on success, so nothing half-written is left behind. Only
// that job stops: the other lane carries on.
func (s *Service) Cancel(id int64) error {
	s.mu.Lock()
	var job *Job
	for _, j := range s.jobs {
		if j.ID == id {
			job = j
			break
		}
	}
	if job == nil {
		s.mu.Unlock()
		return ErrJobNotActive
	}
	switch job.State {
	case StateQueued:
		s.dropPendingLocked(job)
		job.State = StateCancelled
		job.Note = "removed from the queue"
		job.FinishedAt = time.Now().Unix()
		s.retireLocked(job)
		s.saveLocked("cancel", job)
		s.event("info", "Removed "+job.Title+" from the queue")
		return nil
	case StateRunning:
		if h := s.runs[job.ID]; h != nil {
			h.cancel()
		}
		job.Note = "stopping…"
		s.mu.Unlock()
		s.event("info", "Stopping "+job.Title)
		return nil
	default:
		s.mu.Unlock()
		return ErrJobNotActive
	}
}

// ClearQueue drops every queued job in both lanes (running ones are left alone — use
// Cancel for those). Returns how many were dropped.
func (s *Service) ClearQueue() int { return s.ClearLane("") }

// ClearLane drops the queued jobs of one lane (LaneFast or LaneGPU; "" = both): "Clear AI
// backlog" empties the GPU lane without touching extractions and downloads.
func (s *Service) ClearLane(lane string) int {
	s.mu.Lock()
	var cleared []*Job
	now := time.Now().Unix()
	for li := range s.pending {
		if lane != "" && li != laneIndex(lane) {
			continue
		}
		for p := range s.pending[li] {
			for _, j := range s.pending[li][p] {
				j.State = StateCancelled
				j.Note = "queue cleared"
				if li == laneIndex(LaneGPU) {
					j.Note = "AI backlog cleared"
				}
				j.FinishedAt = now
				s.retireLocked(j)
				cleared = append(cleared, j)
			}
			s.pending[li][p] = nil
		}
	}
	s.saveLocked("clear queue", cleared...)
	n := len(cleared)
	if n > 0 {
		what := "queued job(s)"
		if lane == LaneGPU {
			what = "job(s) from the AI backlog"
		}
		s.event("info", fmt.Sprintf("Cleared %d %s", n, what))
	}
	return n
}

// dropPendingLocked removes a job from whichever pending line holds it; the mutex must be held.
func (s *Service) dropPendingLocked(job *Job) {
	for li := range s.pending {
		for p := range s.pending[li] {
			for i, j := range s.pending[li][p] {
				if j == job {
					s.pending[li][p] = append(s.pending[li][p][:i], s.pending[li][p][i+1:]...)
					return
				}
			}
		}
	}
}

// pendingLocked is the total number of waiting jobs across every line; the mutex must be held.
func (s *Service) pendingLocked() int {
	n := 0
	for li := range s.pending {
		for p := range s.pending[li] {
			n += len(s.pending[li][p])
		}
	}
	return n
}

// pop takes the next fast-lane job (popLane).
func (s *Service) pop() *Job { return s.popLane(LaneFast) }

// popLane takes the oldest job from the lane's most urgent non-empty line, or nil. A job
// whose file another job is working on (in either lane) waits its turn, so a redo can't
// race an AI run on the same file; the next job in line goes instead.
func (s *Service) popLane(lane string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	li := laneIndex(lane)
	for p := range s.pending[li] {
		line := s.pending[li][p]
		for i, job := range line {
			k := job.key()
			if s.busy[k] != nil {
				continue
			}
			if i == 0 {
				s.pending[li][p] = line[1:]
			} else {
				s.pending[li][p] = append(line[:i:i], line[i+1:]...)
			}
			if s.busy == nil {
				s.busy = map[string]*Job{}
			}
			s.busy[k] = job
			// Another worker of this lane may be asleep with work still waiting.
			for _, l := range s.pending[li] {
				if len(l) > 0 {
					nudge(s.laneWakeLocked(li))
					break
				}
			}
			return job
		}
	}
	return nil
}

// releaseLocked frees a job's file for the next job on it; the mutex must be held.
func (s *Service) releaseLocked(job *Job) {
	if k := job.key(); s.busy[k] == job {
		delete(s.busy, k)
	}
}

// enqueue registers a job and wakes the worker. Returns the existing job instead when the
// same file is already queued or running: the 6-hourly sweep re-queues everything still
// missing, and without this every file was in the queue several times over.
func (s *Service) enqueue(job *Job) *Job {
	out, added := s.enqueueJobs("enqueue", []*Job{job})
	if added > 0 {
		s.event("info", "Queued "+job.Title)
	}
	return out[0]
}

// enqueueJobs is enqueue for many jobs at once: each is deduped as enqueue does, and the
// new and bumped rows are written in one transaction — a sweep's thousands of files cost
// one commit. Returns each job's live job (itself, or the one already queued) and how
// many were new.
func (s *Service) enqueueJobs(what string, jobs []*Job) ([]*Job, int) {
	s.mu.Lock()
	// Looked up, not scanned: a sweep queues thousands, and walking the list for each
	// one (with a key rendered per entry) was quadratic — slow enough under the race
	// detector to fail the "never blocks" test.
	if s.active == nil {
		s.active = map[string]*Job{}
	}
	out := make([]*Job, len(jobs))
	var inserts, saves []Job
	var fresh []*Job
	now := time.Now().Unix()
	for i, job := range jobs {
		job.Priority = clampPrio(job.Priority)
		job.Lane = job.lane()
		key := job.activeKey()
		if j, ok := s.active[key]; ok {
			out[i] = j
			if j.State != StateQueued {
				continue
			}
			changed := false
			if job.Redo && !j.Redo {
				j.Redo = true // the waiting job takes on the stronger intent
				changed = true
			}
			// Pressing Ensure on a file the sweep already queued moves it up to the
			// button's line rather than leaving it hours deep behind the sweep.
			if job.Priority < j.Priority {
				s.dropPendingLocked(j)
				j.Priority = job.Priority
				s.pushLocked(j)
				changed = true
			}
			if changed {
				saves = append(saves, *j)
			}
			continue
		}
		s.nextID++
		job.ID = s.nextID
		job.State = StateQueued
		job.At = now
		s.active[key] = job
		s.pushLocked(job)
		inserts = append(inserts, *job)
		fresh = append(fresh, job)
		out[i] = job
	}
	if len(fresh) > 0 {
		// Newest first: the new jobs in reverse, ahead of everything already listed.
		list := make([]*Job, 0, len(fresh)+len(s.jobs))
		for i := len(fresh) - 1; i >= 0; i-- {
			list = append(list, fresh[i])
		}
		s.jobs = append(list, s.jobs...)
	}
	// Keep the visible history bounded, but never drop a job that hasn't run yet. The
	// trim walks the whole list, so it runs once the finished tail has grown by a few
	// hundred rather than on every enqueue. (The table keeps more: JobHistory pages it.)
	if len(s.jobs) > jobMemoryKeep+s.pendingLocked()+256 {
		kept := s.jobs[:0:0]
		for i, j := range s.jobs {
			if i < jobMemoryKeep || j.State == StateQueued || j.State == StateRunning {
				kept = append(kept, j)
			}
		}
		s.jobs = kept
	}
	s.unlockAndPersist(what, func(ctx context.Context, st *jobStore) error {
		skipped, err := st.write(ctx, inserts, saves)
		if skipped > 0 {
			s.log.Warn("subtitles: queued jobs already had a live row; kept in memory only", "count", skipped)
		}
		return err
	})
	if len(inserts) > 0 {
		s.wakeWorkers()
	}
	return out, len(inserts)
}

// pushLocked puts a job at the back of its lane's line for its priority; the mutex must
// be held.
func (s *Service) pushLocked(j *Job) {
	li := laneIndex(j.lane())
	s.pending[li][j.Priority] = append(s.pending[li][j.Priority], j)
}

// QueueMovie enqueues a subtitle-ensure job for one movie. redo replaces the sidecars
// already there rather than filling in what's missing; prio is the line it waits in
// (PrioImport, PrioManual or PrioSweep).
func (s *Service) QueueMovie(ctx context.Context, movieID int64, redo bool, prio int) (*Job, error) {
	m, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return nil, err
	}
	if !m.HasFile || m.MovieFilePath == "" {
		return nil, fmt.Errorf("movie has no file")
	}
	return s.enqueue(&Job{Kind: "movie", MovieID: movieID, Title: m.Title, Redo: redo, Priority: prio}), nil
}

// QueueEpisode enqueues a subtitle-ensure job for one TV episode (redo and prio as for movies).
func (s *Service) QueueEpisode(ctx context.Context, seriesID int64, season, episode int, redo bool, prio int) (*Job, error) {
	path, _ := s.series.EpisodeFilePath(ctx, seriesID, season, episode)
	if path == "" {
		return nil, fmt.Errorf("episode has no file")
	}
	title := fmt.Sprintf("S%02dE%02d", season, episode)
	if sm, err := s.series.Get(ctx, seriesID); err == nil {
		title = fmt.Sprintf("%s - S%02dE%02d", sm.Title, season, episode)
	}
	return s.enqueue(&Job{Kind: "episode", SeriesID: seriesID, Season: season, Episode: episode, Title: title, Redo: redo, Priority: prio}), nil
}

// QueueSeries enqueues an ensure job for every episode of one show that has a file but
// lacks a kept language — the middle ground between one episode and the whole library.
// Returns how many were queued (already-queued episodes are deduped by enqueue).
func (s *Service) QueueSeries(ctx context.Context, seriesID int64) (int, error) {
	sm, err := s.series.Get(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	var batch []*Job
	for _, e := range s.missingEpisodes(ctx, seriesID, nil, time.Time{}) {
		batch = append(batch, &Job{Kind: "episode", SeriesID: seriesID, Season: e.season, Episode: e.episode,
			Title: fmt.Sprintf("%s - S%02dE%02d", sm.Title, e.season, e.episode), Priority: PrioManual})
	}
	if len(batch) > 0 {
		s.enqueueJobs("queue series", batch)
		s.event("info", fmt.Sprintf("Queued %d episode(s) of %s", len(batch), sm.Title))
	}
	return len(batch), nil
}

// OnMovieImported is the import hook: a movie that just landed gets its subtitles ensured
// now, rather than whenever the next 6-hourly sweep happens to run.
func (s *Service) OnMovieImported(ctx context.Context, movieID int64) {
	if !s.settings.GetBool(ctx, keyMoviesAuto, defaultMoviesAuto) {
		return
	}
	if _, err := s.QueueMovie(ctx, movieID, false, PrioImport); err != nil {
		s.log.Debug("subtitles: import hook skipped movie", "movie_id", movieID, "err", err)
	}
}

// OnMovieChanged keeps Subtitles in step with a movie whose files changed without an
// import — renamed, a file deleted, the movie removed. A movie that still has a file gets
// its Library entry recomputed at the new path (a rename carries its sidecars along, so
// nothing is queued; a missing language is the 6-hourly sweep's job, as before); one left
// with no file is forgotten (OnMovieRemoved). Safe to run again for the same state. An
// error means the movie couldn't be read, and the caller should try again.
func (s *Service) OnMovieChanged(ctx context.Context, movieID int64) error {
	m, err := s.movies.Get(ctx, movieID)
	if err != nil && !errors.Is(err, movies.ErrNotFound) {
		return err
	}
	if err != nil || !m.HasFile || m.MovieFilePath == "" {
		s.OnMovieRemoved(ctx, movieID)
		return nil
	}
	s.snap.mu.Lock()
	seen := !s.snap.at.IsZero()
	s.snap.mu.Unlock()
	if seen { // before the first pass there's nothing to patch; the pass will see it
		s.patchMovie(ctx, m, s.languages(ctx))
	}
	return nil
}

// OnMovieRemoved forgets a movie whose file (or the movie itself) is gone: its Library
// entry goes now rather than at the next pass, and an ensure job still waiting for it is
// dropped from the queue — there's no file left to subtitle. A job already running is
// left to finish (it fails on the missing file on its own).
func (s *Service) OnMovieRemoved(_ context.Context, movieID int64) {
	s.dropMovie(movieID)
	s.mu.Lock()
	var dropped []*Job
	for _, j := range s.jobs {
		if j.Kind == "movie" && j.MovieID == movieID && j.State == StateQueued {
			s.dropPendingLocked(j)
			j.State = StateCancelled
			j.Note = "the movie's file was removed"
			j.FinishedAt = time.Now().Unix()
			s.retireLocked(j)
			dropped = append(dropped, j)
		}
	}
	s.saveLocked("movie removed", dropped...)
	if n := len(dropped); n > 0 {
		s.log.Info("subtitles: dropped queued jobs for a removed movie file", "movie_id", movieID, "jobs", n)
	}
}

// OnSeriesImported is the import hook for TV: the episodes the import just placed get
// their subtitles ensured now. Only those — it used to be told just the series and queued
// every episode of the show still missing a language, so one new episode of a long-running
// show put the whole back-catalogue in the queue in front of whatever came next. Anything
// older that is missing is the 6-hourly sweep's job.
func (s *Service) OnSeriesImported(ctx context.Context, seriesID int64, episodes []series.EpisodeRef) {
	if !s.settings.GetBool(ctx, keySeriesAuto, defaultSeriesAuto) {
		return
	}
	n := 0
	for _, e := range episodes {
		if _, err := s.QueueEpisode(ctx, seriesID, e.Season, e.Episode, false, PrioImport); err == nil {
			n++
		} else {
			s.log.Debug("subtitles: import hook skipped episode", "series_id", seriesID, "season", e.Season, "episode", e.Episode, "err", err)
		}
	}
	if n > 0 {
		s.log.Info("subtitles: import hook queued episodes", "series_id", seriesID, "count", n)
	}
}

// SweepMissing enqueues an ensure job for every downloaded file still missing a kept-language
// subtitle (media = "movies" | "tv"). Returns how many jobs were queued.
//
// Decides "missing" from the sidecars on disk alone. It used to go through Library(),
// which probes every video file for embedded tracks — a full ffprobe pass over the whole
// catalogue, every six hours, to answer a question a directory listing answers.
//
// A language still backing off for that file (backoff.go) doesn't count as missing: the
// whole sweep reads every backoff row in one query up front.
func (s *Service) SweepMissing(ctx context.Context, media string) (int, error) {
	n := 0
	backoffs, err := s.ledger.LoadBackoffs(ctx)
	if err != nil {
		// Backoff only saves API calls and GPU time; without it the sweep just asks again.
		s.log.Warn("subtitles: could not read backoff, sweeping without it", "err", err)
		backoffs = nil
	}
	now := time.Now()
	if media == "tv" {
		list, err := s.series.List(ctx)
		if err != nil {
			return 0, err
		}
		for _, sm := range list {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			// One write per show: its episodes start queuing while the rest are scanned.
			var batch []*Job
			for _, e := range s.missingEpisodes(ctx, sm.ID, backoffs, now) {
				batch = append(batch, &Job{Kind: "episode", SeriesID: sm.ID, Season: e.season, Episode: e.episode,
					Title: fmt.Sprintf("%s - S%02dE%02d", sm.Title, e.season, e.episode), Priority: PrioSweep})
			}
			if len(batch) > 0 {
				s.enqueueJobs("sweep", batch)
				n += len(batch)
			}
		}
		if n > 0 {
			s.event("info", fmt.Sprintf("Sweep queued %d episode(s) missing subtitles", n))
		}
		return n, nil
	}
	list, err := s.movies.List(ctx)
	if err != nil {
		return 0, err
	}
	langs := s.languages(ctx)
	var batch []*Job
	for _, m := range list {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !m.HasFile || m.MovieFilePath == "" {
			continue
		}
		key := fmt.Sprintf("m:%d", m.ID)
		due := 0
		for _, l := range movieSweepLangs(m.MovieFilePath, langs) {
			if !backoffs.Blocked(key, l, m.MovieFilePath, now) {
				due++
			}
		}
		if due == 0 {
			continue
		}
		batch = append(batch, &Job{Kind: "movie", MovieID: m.ID, Title: m.Title, Priority: PrioSweep})
	}
	// One transaction for the whole sweep, not one commit per film.
	if len(batch) > 0 {
		s.enqueueJobs("sweep", batch)
		s.event("info", fmt.Sprintf("Sweep queued %d movie(s) missing subtitles", len(batch)))
	}
	return len(batch), nil
}

// movieNeedsSweep reports whether the sweep should queue a movie: some kept language has
// no paired sidecar. A language covered only by an orphaned subtitle (one named for no
// video — the owner's file under an old name, most likely) doesn't count: regenerating
// those every six hours would bury what the owner chose. Ensure still replaces it.
func movieNeedsSweep(path string, langs []string) bool {
	return len(movieSweepLangs(path, langs)) > 0
}

// movieSweepLangs is the kept languages the sweep would make for a movie: missing, and
// not covered by an orphaned subtitle.
func movieSweepLangs(path string, langs []string) []string {
	sc := scanSidecars(path, langs, "movie")
	missing := missingOf(langs, sc.Present)
	return without(missing, orphanCovered(langs, sc.Present, sc.Orphans))
}

type epRef struct{ season, episode int }

// missingEpisodes lists one show's episodes that have a file but lack a kept language.
// With backoffs (the sweep), a language still backing off for that file isn't lacking;
// nil (a button press) counts every missing language.
func (s *Service) missingEpisodes(ctx context.Context, seriesID int64, backoffs Backoffs, now time.Time) []epRef {
	full, err := s.series.Get(ctx, seriesID)
	if err != nil {
		return nil
	}
	langs := s.languages(ctx)
	var out []epRef
	for _, sn := range full.Seasons {
		for _, e := range sn.Episodes {
			if !e.HasFile || e.FilePath == "" {
				continue
			}
			key := fmt.Sprintf("e:%d:%d:%d", seriesID, e.SeasonNumber, e.EpisodeNumber)
			due := 0
			for _, l := range missingOf(langs, scanSidecars(e.FilePath, langs, "episode").Present) {
				if !backoffs.Blocked(key, l, e.FilePath, now) {
					due++
				}
			}
			if due == 0 {
				continue
			}
			out = append(out, epRef{e.SeasonNumber, e.EpisodeNumber})
		}
	}
	return out
}

// Jobs returns a snapshot of every queued and running job and the most recent finished
// ones (newest first). Older history is JobHistory's.
func (s *Service) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	for i, j := range s.jobs {
		out[i] = *j
	}
	return out
}

// Job finds one job by id: the live queue first (it has the running job's stage and
// progress), then the saved history. Ids are row ids, so they don't repeat after a restart.
func (s *Service) Job(ctx context.Context, id int64) (Job, bool, error) {
	s.mu.Lock()
	for _, j := range s.jobs {
		if j.ID == id {
			out := *j
			s.mu.Unlock()
			return out, true, nil
		}
	}
	s.mu.Unlock()
	if !s.jobsDB.ok() {
		return Job{}, false, nil
	}
	return s.jobsDB.get(ctx, id)
}

// JobHistory pages every job the table keeps (the last 500 finished, plus the queue),
// newest first. A job still in the live queue reads from memory, with its stage and
// progress. Without a database it pages the in-memory list.
func (s *Service) JobHistory(ctx context.Context, q JobQuery) ([]Job, error) {
	var rows []Job
	if s.jobsDB.ok() {
		var err error
		if rows, err = s.jobsDB.page(ctx, q); err != nil {
			return nil, err
		}
	} else {
		for _, j := range s.Jobs() {
			if (q.Before == 0 || j.ID < q.Before) && (q.State == "" || j.State == q.State) {
				rows = append(rows, j)
				if len(rows) == q.limit() {
					break
				}
			}
		}
		return rows, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[int64]*Job, len(s.jobs))
	for _, j := range s.jobs {
		if j.State == StateQueued || j.State == StateRunning {
			live[j.ID] = j
		}
	}
	for i := range rows {
		if j, ok := live[rows[i].ID]; ok {
			rows[i] = *j
		}
	}
	return rows, nil
}

// PruneJobs trims the saved history to the newest 500 finished jobs.
func (s *Service) PruneJobs(ctx context.Context) {
	if n, err := s.jobsDB.prune(ctx); err != nil {
		s.log.Warn("subtitles: could not prune the job history", "err", err)
	} else if n > 0 {
		s.log.Info("subtitles: pruned old jobs", "count", n)
	}
}

// markRunning records that the worker has started a job.
func (s *Service) markRunning(job *Job) {
	s.mu.Lock()
	job.State, job.StartedAt = StateRunning, time.Now().Unix()
	s.saveLocked("start", job)
}

// Pending reports how many jobs are waiting in every line, for the status line.
func (s *Service) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingLocked()
}

// update mutates a job under lock.
func (s *Service) update(job *Job, fn func(*Job)) {
	s.mu.Lock()
	fn(job)
	s.mu.Unlock()
}

// finish sets a job's terminal state + note, and clears the in-flight stage/progress.
func (s *Service) finish(job *Job, state JobState, note string) {
	s.finishOutcomes(job, state, note, nil)
}

// finishOutcomes is finish for a run of the ladder, keeping what each rung did.
func (s *Service) finishOutcomes(job *Job, state JobState, note string, outcomes []Outcome) {
	s.mu.Lock()
	job.State, job.Note, job.Stage, job.Progress = state, note, "", 0
	job.Outcomes = outcomes
	job.FinishedAt = time.Now().Unix()
	s.retireLocked(job)
	done, snapshot := s.onJobDone, *job
	s.saveLocked("finish", job)
	if done != nil {
		done(snapshot)
	}
}

// SetJobDoneHook registers fn to hear about every job the worker finishes (done, skipped,
// failed or stopped), with its final state, note and outcomes — so another module (a
// reported problem's subtitles fix) settles when the job does instead of polling Jobs().
// fn runs on the worker; keep it short. Jobs dropped from the queue before they ran don't
// call it.
func (s *Service) SetJobDoneHook(fn func(Job)) {
	s.mu.Lock()
	s.onJobDone = fn
	s.mu.Unlock()
}

// retireLocked forgets a job that is no longer queued or running, so its file can be
// queued again. The mutex must be held.
func (s *Service) retireLocked(job *Job) {
	if k := job.activeKey(); s.active[k] == job {
		delete(s.active, k)
	}
	s.releaseLocked(job)
}

// event appends a line to the activity console (kept to the last 500) and mirrors it to the log.
func (s *Service) event(level, msg string) {
	s.logMu.Lock()
	s.logBuf = append(s.logBuf, LogLine{At: time.Now().Unix(), Level: level, Msg: msg})
	if len(s.logBuf) > 500 {
		s.logBuf = s.logBuf[len(s.logBuf)-500:]
	}
	s.logMu.Unlock()
	if level == "warn" || level == "error" {
		s.log.Warn("subtitles: " + msg)
	} else {
		s.log.Info("subtitles: " + msg)
	}
}

// Logs returns the recent activity console lines (oldest first).
func (s *Service) Logs() []LogLine {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	out := make([]LogLine, len(s.logBuf))
	copy(out, s.logBuf)
	return out
}

// missingOf returns the wanted languages that aren't present (case-insensitive).
func missingOf(wanted, present []string) []string {
	have := make(map[string]bool, len(present))
	for _, p := range present {
		have[strings.ToLower(p)] = true
	}
	var out []string
	for _, w := range wanted {
		if !have[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}
