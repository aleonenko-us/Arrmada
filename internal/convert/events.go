package convert

import (
	"context"
	"time"
)

// Convert's lifecycle events on the event bus. Until these, a file Convert gave up on or a
// queue stuck for lack of space was visible only to someone who opened the Convert page.
//
// Topics and payloads (the payload is one of the *Event structs below; notify reads it
// through JSON, so the json names are the contract):
//
//	convert.done     DoneEvent     a converted (or tidied) file replaced its original
//	convert.failed   FailedEvent   a job ended in failure (transient ones included)
//	convert.blocked  BlockedEvent  a file reached maxFailures and is left alone from now on
//	convert.stalled  StalledEvent  a file waits for room (scratch, library disk, bin/hold)
//	convert.reverted RevertedEvent a conversion was undone from the originals hold (revert.go)
//
// Who hears them: every topic here is a plain topic, which the realtime policy sends to
// staff only, and they carry library paths for that reason — never republish them on a
// user.<id>.* topic. internal/notify turns them into admin alerts (gave up, stuck, the daily
// summary).
//
// When they fire: after the database has been updated, from the job's own goroutine.
// Publishing never blocks a worker: the bus drops an event for a subscriber that has fallen
// behind, which costs at most an alert.
//
// The hooks (the only places that publish, so a change to the job lifecycle keeps them):
//   - finish (service.go): convert.done on StateDone; convert.failed on StateFailed, plus
//     convert.blocked when that failure was the file's maxFailures-th.
//   - finishAfterEncode (service.go): convert.blocked when its counted failure was the
//     maxFailures-th.
//   - finishSkip (service.go) and finishSkipUntil (holdbudget.go, a full originals hold):
//     convert.stalled on the FIRST skip in a row of a space kind (attempts == 1), so a
//     scratch disk full for a week says so once per file, not on every retry.
//   - finishRevert (revert.go): convert.reverted once the original is back and recorded.
const (
	TopicDone     = "convert.done"
	TopicFailed   = "convert.failed"
	TopicBlocked  = "convert.blocked"
	TopicStalled  = "convert.stalled"
	TopicReverted = "convert.reverted"
)

// Publisher is the event bus as Convert sees it (*eventbus.Bus satisfies it).
type Publisher interface {
	Publish(topic string, data any)
}

// DoneEvent is convert.done.
type DoneEvent struct {
	Title    string `json:"title"`
	Key      string `json:"key"`
	Kind     string `json:"kind"` // "movie" | "episode"
	MovieID  int64  `json:"movie_id,omitempty"`
	SeriesID int64  `json:"series_id,omitempty"`
	Season   int    `json:"season"`
	Episode  int    `json:"episode,omitempty"`
	// VersionID is the movie's extra version track the file belongs to; 0 = its default
	// file. Title already names the track ("Film · Director's Cut").
	VersionID int64  `json:"version_id,omitempty"`
	Path      string `json:"path"`     // the new file
	OldPath   string `json:"old_path"` // the original it replaced (now in the bin or hold)
	SrcBytes  int64  `json:"src_bytes"`
	OutBytes  int64  `json:"out_bytes"`
	// SavedBytes is SrcBytes - OutBytes, never below 0.
	SavedBytes int64  `json:"saved_bytes"`
	Codec      string `json:"codec,omitempty"` // "" = a track tidy-up that copied the video
	HistoryID  int64  `json:"history_id,omitempty"`
	At         int64  `json:"at"` // unix seconds
}

// FailedEvent is convert.failed.
type FailedEvent struct {
	Title string `json:"title"`
	Key   string `json:"key"`
	Note  string `json:"note"`
	// Transient failures (a full disk, a vanished source) clear on their own and never
	// count toward blocking the file.
	Transient bool  `json:"transient,omitempty"`
	At        int64 `json:"at"`
}

// BlockedEvent is convert.blocked: the file failed Count (= maxFailures) times and the
// runner won't pick it again until someone presses Try again.
type BlockedEvent struct {
	Title string `json:"title"`
	Key   string `json:"key"`
	Note  string `json:"note"` // the last failure
	Count int    `json:"count"`
	At    int64  `json:"at"`
}

// StalledEvent is convert.stalled. NeedBytes/FreeBytes are 0 when the check that stopped
// the file couldn't say (a disk that filled mid-step).
type StalledEvent struct {
	Reason    string `json:"reason"` // a Skip* kind: no_scratch | library_full | bin_full | hold_full
	Title     string `json:"title"`
	Key       string `json:"key"`
	Note      string `json:"note"`
	NeedBytes int64  `json:"need_bytes"`
	FreeBytes int64  `json:"free_bytes"`
	At        int64  `json:"at"`
}

// SetBus tells Convert where to publish its lifecycle events. nil = nowhere. Call it at
// startup, next to SetWatching.
func (s *Service) SetBus(p Publisher) {
	if p == nil {
		s.bus.Store(nil)
		return
	}
	s.bus.Store(&p)
}

func (s *Service) publish(topic string, data any) {
	if p := s.bus.Load(); p != nil {
		(*p).Publish(topic, data)
	}
}

// stalledKind reports whether a skip kind means "waiting for room" — the condition worth
// telling the owner about, because nothing converts until someone frees space. hold_full
// is the originals hold's budget (CONV-09), named here by value so either lane can land
// first.
func stalledKind(kind string) bool {
	switch kind {
	case SkipNoScratch, SkipLibraryFull, SkipBinFull, "hold_full":
		return true
	}
	return false
}

// noteStall remembers the space figures behind a skip that's about to be recorded, for
// the convert.stalled event. Called right before finishSkip by the checks that know them.
func (s *Service) noteStall(job *Job, need, free int64) {
	rec := s.record(job)
	rec.stallNeed, rec.stallFree = need, free
}

// publishDone announces a finished conversion. Called from finish after the ledger row is
// written.
func (s *Service) publishDone(job *Job) {
	if s.bus.Load() == nil {
		return
	}
	s.mu.Lock()
	j := *job
	s.mu.Unlock()
	rec := s.record(job)
	saved := j.SrcBytes - j.OutBytes
	if saved < 0 {
		saved = 0
	}
	s.publish(TopicDone, DoneEvent{
		Title: j.Title, Key: j.Key, Kind: j.Kind, MovieID: j.MovieID, VersionID: j.VersionID, SeriesID: j.SeriesID,
		Season: j.Season, Episode: j.Episode, Path: rec.outPath, OldPath: rec.srcPath,
		SrcBytes: j.SrcBytes, OutBytes: j.OutBytes, SavedBytes: saved, Codec: j.Codec,
		HistoryID: rec.historyID, At: time.Now().Unix(),
	})
}

// failureCounted records one counted failure of the job's file and announces
// convert.blocked when it was the one that blocks the file. Exactly once: only the
// failure that takes the count to maxFailures publishes (a file is converted by one job
// at a time, so two can't race to the same count).
func (s *Service) failureCounted(ctx context.Context, job *Job, note string) {
	n := s.failures.recordFailure(ctx, job.Key, note)
	if n != maxFailures {
		return
	}
	s.publish(TopicBlocked, BlockedEvent{Title: job.Title, Key: job.Key, Note: note, Count: n, At: time.Now().Unix()})
}

// publishFailed announces a failed job.
func (s *Service) publishFailed(job *Job, note string, transient bool) {
	s.publish(TopicFailed, FailedEvent{Title: job.Title, Key: job.Key, Note: note, Transient: transient, At: time.Now().Unix()})
}

// skipRecorded is told how many times in a row a file has now hit a skip kind, and
// announces convert.stalled on the first of a space kind.
func (s *Service) skipRecorded(job *Job, kind, note string, attempts int) {
	if attempts != 1 || !stalledKind(kind) {
		return
	}
	rec := s.record(job)
	s.publish(TopicStalled, StalledEvent{Reason: kind, Title: job.Title, Key: job.Key, Note: note,
		NeedBytes: rec.stallNeed, FreeBytes: rec.stallFree, At: time.Now().Unix()})
}
