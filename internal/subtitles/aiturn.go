package subtitles

import (
	"context"
	"sort"
	"time"
)

// The GPU lane shares the box with Plex and Jellyfin. Two things keep it out of the way:
// it holds while someone is watching (between whisper chunks, so a running file stops
// within a few minutes and picks up where it left off), and the catch-up backlog runs the
// titles people are most likely to watch first — the ones someone asked for, then the
// newest — instead of the order a sweep happened to list them in.

// keyAIPauseWatching is the setting that holds AI subtitles while someone is watching; on
// by default.
const keyAIPauseWatching = "subs_ai_pause_watching"

// aiHoldPoll is how often a held lane looks again.
const aiHoldPoll = 15 * time.Second

// aiRankEvery is how often the GPU lane's sweep line is put back in order: new sweep jobs
// join at the back until the next pass.
const aiRankEvery = 30 * time.Minute

// mediaWatch is how the service asks whether someone is watching, and whether anyone is
// monitoring the servers at all (so the settings page can say when the hold can't work).
type mediaWatch struct {
	watching func() bool
	known    func() bool
}

// SetWatching tells Subtitles how to ask whether someone is watching Plex or Jellyfin.
// A nil watching clears it: the lane never holds.
func (s *Service) SetWatching(watching func() bool, known func() bool) {
	if watching == nil {
		s.watch.Store(nil)
		return
	}
	s.watch.Store(&mediaWatch{watching: watching, known: known})
}

// SetRequested tells the GPU lane which movies someone has asked for (TMDB ids): they go
// first in the backlog. nil = nobody's requests count.
func (s *Service) SetRequested(fn func(ctx context.Context) map[int]bool) {
	if fn == nil {
		s.requested.Store(nil)
		return
	}
	s.requested.Store(&fn)
}

func (s *Service) watchingKnown() bool {
	if w := s.watch.Load(); w != nil && w.known != nil {
		return w.known()
	}
	return false
}

// aiPauseWatching is the setting.
func (s *Service) aiPauseWatching(ctx context.Context) bool {
	if s.settings == nil {
		return true
	}
	return s.settings.GetBool(ctx, keyAIPauseWatching, true)
}

// SetAIPauseWatching saves the setting; switching it off lets a held lane go at once.
func (s *Service) SetAIPauseWatching(ctx context.Context, on bool) error {
	if err := s.settings.SetBool(ctx, keyAIPauseWatching, on); err != nil {
		return err
	}
	if !on {
		nudge(s.aiResume())
	}
	return nil
}

// aiShouldHold reports whether AI work should wait right now.
func (s *Service) aiShouldHold(ctx context.Context) bool {
	w := s.watch.Load()
	return w != nil && w.watching() && s.aiPauseWatching(ctx)
}

// aiResume is the channel that cuts a hold's wait short (a setting switched off).
func (s *Service) aiResume() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.aiWake == nil {
		s.aiWake = make(chan struct{}, 1)
	}
	return s.aiWake
}

// aiHold waits while someone is watching, saying so in the log once per hold. It returns
// at once when nobody is, and ctx's error when it's stopped while waiting.
func (s *Service) aiHold(ctx context.Context) error {
	if !s.aiShouldHold(ctx) {
		return nil
	}
	s.aiHeld.Store(true)
	s.event("info", "AI subtitles paused — someone is watching")
	defer func() {
		s.aiHeld.Store(false)
		if ctx.Err() == nil {
			s.event("info", "AI subtitles resumed — nobody is watching")
		}
	}()
	wake := s.aiResume()
	t := time.NewTicker(aiHoldPoll)
	defer t.Stop()
	for s.aiShouldHold(ctx) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		case <-wake:
		}
	}
	return nil
}

// rankAILine puts the GPU lane's sweep line in the order to run it: movies someone asked
// for first, then the newest additions. Jobs this pass can't place keep their order behind
// those. The lookups run outside the lock; jobs that joined or left meanwhile are fine.
func (s *Service) rankAILine(ctx context.Context) {
	li, p := laneIndex(LaneGPU), PrioSweep
	s.mu.Lock()
	line := append([]*Job(nil), s.pending[li][p]...)
	s.mu.Unlock()
	if len(line) < 2 || s.movies == nil {
		return
	}
	var asked map[int]bool
	if fn := s.requested.Load(); fn != nil {
		asked = (*fn)(ctx)
	}
	type rank struct {
		asked bool
		added string // RFC 3339-ish, so newer sorts greater as text
	}
	ranks := make(map[*Job]rank, len(line))
	for _, j := range line {
		if ctx.Err() != nil {
			return
		}
		if j.Kind != "movie" || j.MovieID == 0 {
			continue
		}
		m, err := s.movies.Get(ctx, j.MovieID)
		if err != nil {
			continue
		}
		ranks[j] = rank{asked: asked[m.TMDBID], added: m.AddedAt}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.pending[li][p]
	sort.SliceStable(cur, func(a, b int) bool {
		ra, oka := ranks[cur[a]]
		rb, okb := ranks[cur[b]]
		switch {
		case oka != okb:
			return oka
		case ra.asked != rb.asked:
			return ra.asked
		}
		return ra.added > rb.added
	})
}
