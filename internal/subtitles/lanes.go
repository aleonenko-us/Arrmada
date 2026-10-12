package subtitles

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The queue has two lanes (SUB-25). The fast lane extracts embedded tracks and downloads
// from OpenSubtitles — seconds per file. The GPU lane runs whisper — up to hours per file.
// Every job starts in the fast lane; when languages are still missing and whisper would
// actually run for them, the job moves (same id, same run) to the GPU lane with only those
// languages, and the fast lane goes on to the next file. No two jobs work on one file at
// once, whichever lanes they are in (Service.busy).

const numLanes = 2

// laneIndex is a lane's slot in Service.pending.
func laneIndex(lane string) int {
	if lane == LaneGPU {
		return 1
	}
	return 0
}

// Settings keys for the lanes.
const (
	keyFastWorkers = "subs_fast_lane_workers" // 1 (default) or 2; read when the workers start
	keyAIRTF       = "subs_ai_rtf"            // JSON aiRTF: whisper's measured speed per backend
)

// fastWorkers is how many fast-lane workers to run: one by default, two at most — more
// would only pile reads onto the same spinning disks.
func (s *Service) fastWorkers(ctx context.Context) int {
	if s.settings == nil {
		return 1
	}
	n, err := strconv.Atoi(strings.TrimSpace(s.settings.Get(ctx, keyFastWorkers, "1")))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, 2)
}

// laneWakeLocked is the channel that wakes a lane's workers; the mutex must be held.
func (s *Service) laneWakeLocked(li int) chan struct{} {
	if li == laneIndex(LaneGPU) {
		if s.wakeGPU == nil {
			s.wakeGPU = make(chan struct{}, 1)
		}
		return s.wakeGPU
	}
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

// nudge wakes a sleeping worker; a nudge already waiting is enough.
func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// wakeWorkers nudges both lanes.
func (s *Service) wakeWorkers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for li := 0; li < numLanes; li++ {
		nudge(s.laneWakeLocked(li))
	}
}

// lanesOn reports whether a GPU worker is running to take jobs' AI part.
func (s *Service) lanesOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lanes
}

// aiWouldRun reports whether whisper would actually run for any of langs — the work the
// GPU lane is for. When it wouldn't (no model, a language it can't make from this audio),
// the AI rung only records why, and the fast lane does that on the spot instead of
// parking the job behind the AI backlog.
func (s *Service) aiWouldRun(mi *mediaInfo, langs []string) bool {
	ai := s.aiGen()
	if !ai.available() {
		return false
	}
	var audio []string
	if mi != nil {
		audio = mi.AudioLangs
	}
	for _, l := range langs {
		if plan := aiPlan(audio, l); plan != "" && ai.canRun(plan == "translate") {
			return true
		}
	}
	return false
}

// handOffAI moves a fast-lane job's leftover languages to the GPU lane when whisper would
// run for them, recording what the fast rungs did now (the GPU part records the rest
// under the same run). Reports whether it did; the caller then stops.
func (s *Service) handOffAI(ctx context.Context, job *Job, ref fileRef, mi *mediaInfo, runID string, langs []string, outcomes []Outcome) bool {
	if ctx.Err() != nil || len(langs) == 0 || job.lane() == LaneGPU || !s.lanesOn() || !s.aiWouldRun(mi, langs) {
		return false
	}
	var audio int64
	if mi != nil {
		audio = int64(mi.DurationSec)
	}
	if !s.toGPU(ctx, job, langs, outcomes, audio) {
		return false
	}
	s.recordAttempts(ctx, runID, ref, outcomes)
	s.event("info", fmt.Sprintf("%s: waiting for AI (%s)", ref.Title, strings.Join(langs, ", ")))
	return true
}

// toGPU puts a fast-lane job into the GPU lane, keeping its id, priority and outcomes so
// far. A GPU job already waiting for the same file is replaced: this run has just looked
// at the file as it is now, and its languages carry over. It refuses a job Stop has just
// been pressed on (Cancel cancels under the same lock), which then ends as stopped.
func (s *Service) toGPU(ctx context.Context, job *Job, langs []string, outcomes []Outcome, audioSec int64) bool {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	gpuKey := job.key() + "|" + LaneGPU
	var replaced *Job
	if old := s.active[gpuKey]; old != nil && old != job && old.State == StateQueued {
		s.dropPendingLocked(old)
		langs = unionLangs(old.Langs, langs)
		job.Priority = min(job.Priority, old.Priority)
		old.State, old.Note, old.FinishedAt = StateCancelled, fmt.Sprintf("replaced by job %d", job.ID), time.Now().Unix()
		delete(s.active, gpuKey)
		replaced = old
	}
	if k := job.activeKey(); s.active[k] == job {
		delete(s.active, k) // the file may be queued in the fast lane again meanwhile
	}
	job.Lane, job.Langs, job.Outcomes, job.AudioSec = LaneGPU, langs, outcomes, audioSec
	job.State, job.Stage, job.Progress, job.StartedAt = StateQueued, "", 0, 0
	job.Note = "waiting for AI: " + strings.Join(langs, ", ")
	job.At = time.Now().Unix()
	if s.active == nil {
		s.active = map[string]*Job{}
	}
	s.active[gpuKey] = job
	s.pushLocked(job)
	nudge(s.laneWakeLocked(laneIndex(LaneGPU)))
	// The replaced row first: the table allows one live GPU row per file.
	if replaced != nil {
		s.saveLocked("to gpu", replaced, job)
	} else {
		s.saveLocked("to gpu", job)
	}
	return true
}

// unionLangs is a then the languages of b not already in a (case-insensitive).
func unionLangs(a, b []string) []string {
	out := append([]string(nil), a...)
	seen := map[string]bool{}
	for _, l := range a {
		seen[strings.ToLower(l)] = true
	}
	for _, l := range b {
		if !seen[strings.ToLower(l)] {
			seen[strings.ToLower(l)] = true
			out = append(out, l)
		}
	}
	return out
}

// outcomeLangs is the languages outcomes were recorded for, first seen first.
func outcomeLangs(outs []Outcome) []string {
	var out []string
	for _, o := range outs {
		out = unionLangs(out, []string{o.Lang})
	}
	return out
}

// inLangs reports whether l is one of the job's languages; a job without a list takes
// every kept language.
func inLangs(only []string, l string) bool {
	if len(only) == 0 {
		return true
	}
	for _, o := range only {
		if strings.EqualFold(o, l) {
			return true
		}
	}
	return false
}

// aiRTF is whisper's measured speed: seconds of audio per second of work, per backend
// (sycl, vulkan, cpu), smoothed over runs.
type aiRTF struct {
	Last    string             `json:"last"` // the backend measured most recently
	Backend map[string]float64 `json:"backend"`
}

// rtfWeight is how much one run moves the average: enough to follow a driver or model
// change within a few files, not so much that one odd file swings the estimate.
const rtfWeight = 0.3

func (s *Service) loadRTF(ctx context.Context) aiRTF {
	var r aiRTF
	if s.settings != nil {
		_ = json.Unmarshal([]byte(s.settings.Get(ctx, keyAIRTF, "")), &r)
	}
	if r.Backend == nil {
		r.Backend = map[string]float64{}
	}
	return r
}

// noteRTF folds one AI run's realtime factor into the backend's average.
func (s *Service) noteRTF(ctx context.Context, backend string, rtf float64) {
	if s.settings == nil || backend == "" || rtf <= 0 {
		return
	}
	s.rtfMu.Lock()
	defer s.rtfMu.Unlock()
	r := s.loadRTF(ctx)
	if prev, ok := r.Backend[backend]; ok && prev > 0 {
		rtf = prev + rtfWeight*(rtf-prev)
	}
	r.Backend[backend], r.Last = rtf, backend
	b, _ := json.Marshal(r)
	if err := s.settings.Set(context.WithoutCancel(ctx), keyAIRTF, string(b)); err != nil {
		s.log.Warn("subtitles: could not save the AI speed", "err", err)
	}
}

// Backlog is the GPU lane's waiting work, for the Overview's "AI backlog" line.
type Backlog struct {
	Files    int     `json:"gpu_backlog"`           // GPU-lane jobs queued or running
	AudioSec int64   `json:"gpu_backlog_audio_sec"` // audio still to go through whisper (each language is a pass)
	ETASec   int64   `json:"gpu_eta_sec"`           // 0 = unknown: no speed measured yet
	RTF      float64 `json:"rtf"`                   // the backend's measured realtime factor
}

// GPUBacklog sums the GPU lane's audio from what the jobs carry (the running time read
// when they moved lanes — never a fresh probe) and divides by whisper's measured speed on
// the backend in use, or the one measured last.
func (s *Service) GPUBacklog(ctx context.Context) Backlog {
	var b Backlog
	s.mu.Lock()
	for _, j := range s.jobs {
		if j.lane() != LaneGPU || (j.State != StateQueued && j.State != StateRunning) {
			continue
		}
		b.Files++
		audio := j.AudioSec * int64(max(1, len(j.Langs)))
		if j.State == StateRunning && j.Progress > 0 {
			audio = audio * int64(100-min(j.Progress, 100)) / 100
		}
		b.AudioSec += audio
	}
	s.mu.Unlock()
	r := s.loadRTF(ctx)
	backend := s.aiGen().Backend()
	if _, ok := r.Backend[backend]; !ok {
		backend = r.Last
	}
	b.RTF = r.Backend[backend]
	if b.RTF > 0 {
		b.ETASec = int64(float64(b.AudioSec) / b.RTF)
	}
	return b
}
