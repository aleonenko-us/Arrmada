package convert

import (
	"context"
	"time"
)

// The request lane. Workers only pick when they're free, and outside your hours an
// automatic conversion is frozen in place (SIGSTOP), so its worker sits waiting for it — a
// file you asked for "now" used to wait behind it, possibly for nights. One extra worker
// takes requests only, and only when every normal slot is held by work that includes an
// automatic conversion. While it runs, the automatic conversions wait ("waiting for your
// requested file to finish") and then carry on where they were, if the hours allow. So at
// most one extra encode exists, and the frozen one keeps its memory meanwhile — two working
// sets in RAM, which is the price of not throwing hours of encoding away.

// requestLane is the request lane's worker index: one past the normal workers.
const requestLane = maxWorkers

// laneWaitReason is what an automatic conversion says while a requested file runs.
const laneWaitReason = "waiting for your requested file to finish"

// wakeCh returns the channel the next wakeUp closes.
func (s *Service) wakeCh() <-chan struct{} {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{})
	}
	return s.wake
}

// wakeUp nudges every idle worker to look for work now (a request arrived, a setting
// changed). A broadcast: with a single-slot channel only one waiting worker heard it, and
// the one that could take the work might sleep on for minutes. Never call it holding mu.
func (s *Service) wakeUp() {
	s.wakeMu.Lock()
	if s.wake != nil {
		close(s.wake)
	}
	s.wake = make(chan struct{})
	s.wakeMu.Unlock()
}

// laneBusyLocked reports whether a requested file is running on the lane. Callers hold mu.
func (s *Service) laneBusyLocked() bool {
	for _, j := range s.jobs {
		if j.lane && activeState(j.State) {
			return true
		}
	}
	return false
}

func (s *Service) laneBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.laneBusyLocked()
}

// slots is how the conversion slots stand right now.
type slots struct {
	normalBusy int  // active jobs on the normal workers
	autoBusy   bool // one of them is an automatic conversion
	laneBusy   bool
}

func (s *Service) slotsLocked() slots {
	var sl slots
	for _, j := range s.jobs {
		if !activeState(j.State) {
			continue
		}
		if j.lane {
			sl.laneBusy = true
			continue
		}
		sl.normalBusy++
		if !j.Requested {
			sl.autoBusy = true
		}
	}
	return sl
}

// laneOpen reports whether the lane may take a request: it's free, and every normal slot is
// busy with work that includes an automatic conversion. A free normal slot takes requests
// itself; slots all busy with other requests mean this one waits its turn behind them.
func (sl slots) laneOpen(workers int) bool {
	return !sl.laneBusy && sl.autoBusy && sl.normalBusy >= workers
}

// pickRequest is the lane's picker: the oldest waiting request, when the lane is open.
func (s *Service) pickRequest(ctx context.Context) *Job {
	p := s.prefs(ctx)
	if p.pauseWatching && s.isWatching() {
		return nil // nothing starts while someone is watching
	}
	workers := s.workerCount(ctx)
	s.mu.Lock()
	open := s.slotsLocked().laneOpen(workers)
	s.mu.Unlock()
	if !open {
		return nil
	}
	for _, r := range s.requests.list(ctx) {
		it, ok := parseKey(r.Key)
		if !ok {
			continue // pickJob drops malformed rows
		}
		job, fresh := s.claimPending(it.key(), func(id int64) *Job {
			return &Job{ID: id, Kind: it.Kind, MovieID: it.MovieID, SeriesID: it.SeriesID, Season: it.Season,
				Episode: it.Episode, Title: r.Title, State: StatePreparing, Requested: true, lane: true, StartedAt: time.Now().Unix()}
		})
		if fresh {
			return job
		}
	}
	return nil
}

// RequestPlace is where a just-requested file stands: starting now, or its place in line
// and what it waits behind.
type RequestPlace struct {
	Starting bool `json:"starting"`
	// Position is its place among the requests still waiting for a slot (1 = next).
	Position int `json:"position,omitempty"`
	// Ahead names what it waits behind: the request before it, or a conversion already
	// running. Reason says why when nothing can start at all (someone is watching Plex).
	Ahead  string `json:"ahead,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// RequestPlace works out where a request stands, by the same rules the workers pick by.
func (s *Service) RequestPlace(ctx context.Context, key string) RequestPlace {
	p := s.prefs(ctx)
	watching := p.pauseWatching && s.isWatching()
	workers := s.workerCount(ctx)
	reqs := s.requests.list(ctx)

	s.mu.Lock()
	if _, claimed := s.pending[key]; claimed {
		s.mu.Unlock()
		return RequestPlace{Starting: true}
	}
	sl := s.slotsLocked()
	var waiting []Request
	for _, r := range reqs {
		if _, running := s.pending[r.Key]; !running {
			waiting = append(waiting, r)
		}
	}
	// What a request with no waiting request before it would wait behind: a running
	// requested file first (requests go in order), else whatever is running.
	blocker := ""
	for _, j := range s.jobs {
		if !activeState(j.State) {
			continue
		}
		if j.Requested {
			blocker = j.Title
			break
		}
		if blocker == "" {
			blocker = j.Title
		}
	}
	s.mu.Unlock()

	idx := -1
	for i, r := range waiting {
		if r.Key == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		return RequestPlace{Starting: true} // claimed in the meantime
	}
	if watching {
		return RequestPlace{Position: idx + 1, Reason: "someone is watching — it starts when they stop"}
	}
	// Free normal slots take the first requests; the lane takes one more when it's open
	// once those are filled (the automatic conversion holding a slot is still there).
	free := max(workers-sl.normalBusy, 0)
	filled := slots{normalBusy: sl.normalBusy + free, autoBusy: sl.autoBusy, laneBusy: sl.laneBusy}
	if filled.laneOpen(workers) {
		free++
	}
	if idx < free {
		return RequestPlace{Starting: true}
	}
	place := RequestPlace{Position: idx - free + 1, Ahead: blocker}
	if idx > 0 {
		place.Ahead = waiting[idx-1].Title
	}
	return place
}
