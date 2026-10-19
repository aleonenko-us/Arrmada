package metadata

import (
	"context"
	"sync"
	"time"
)

// MusicBrainz refuses EVERY request from an address that goes over its rate limit, not
// just the extra ones, so one feature running ahead (a scan, a refresh) would take the add
// screen and the album pages down with it. The limit is therefore one gate for the whole
// process, not per client value: mbGate below, shared by every MusicBrainz request,
// whichever code path or client value makes it.
//
// It has two lanes over the one rate. Interactive calls (someone is waiting: searching to
// add an artist, opening an album, an import that is running) go first; background calls
// (refreshing the cache, backfills) take the slots nobody waiting needs. A 503 or 429 on a
// background call also pauses the background lane for a while, doubling up to an hour, so
// a refresh can't keep hammering a service that has started refusing us.

// MBLane says who a MusicBrainz call is for.
type MBLane int

const (
	// MBInteractive: a person is waiting on the answer. The default.
	MBInteractive MBLane = iota
	// MBBackground: nobody is waiting; it yields to interactive calls.
	MBBackground
)

type mbLaneKey struct{}

// WithMBLane marks the MusicBrainz calls made with ctx as interactive or background.
func WithMBLane(ctx context.Context, lane MBLane) context.Context {
	return context.WithValue(ctx, mbLaneKey{}, lane)
}

func mbLaneOf(ctx context.Context) MBLane {
	if l, ok := ctx.Value(mbLaneKey{}).(MBLane); ok {
		return l
	}
	return MBInteractive
}

const (
	// mbPublicInterval is the gap between requests to musicbrainz.org. The documented limit
	// is one a second; the tenth of a second is headroom for clock and network jitter. It
	// can't be set lower: only a self-hosted mirror has a rate of its own.
	mbPublicInterval = 1100 * time.Millisecond
	// mbPauseFirst and mbPauseMax bound how long the background lane rests after a refusal.
	mbPauseFirst = 5 * time.Minute
	mbPauseMax   = time.Hour
)

// mbLimiter spaces requests out by at least an interval, interactive callers first.
type mbLimiter struct {
	mu   sync.Mutex
	next time.Time // the earliest the next request may start
	// waiting counts interactive callers queued; background callers stand aside for them.
	waiting int
	// pausedUntil holds the background lane after a refusal; pause is the current length.
	pausedUntil time.Time
	pause       time.Duration

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// mbGate is the one limiter every MusicBrainz request goes through.
var mbGate = newMBLimiter()

func newMBLimiter() *mbLimiter {
	return &mbLimiter{now: time.Now, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errMBPaused is a background call refused while the background lane rests.
var errMBPaused = errMusicBrainzUnavailable("MusicBrainz refused recent requests; background lookups are paused")

// wait blocks until the caller may send one request, interval after the one before. A
// background caller waits while any interactive caller is queued, and is refused outright
// while the background lane is paused.
func (l *mbLimiter) wait(ctx context.Context, lane MBLane, interval time.Duration) error {
	if lane == MBInteractive {
		l.mu.Lock()
		l.waiting++
		l.mu.Unlock()
		defer func() {
			l.mu.Lock()
			l.waiting--
			l.mu.Unlock()
		}()
	}
	for {
		l.mu.Lock()
		now := l.now()
		if lane == MBBackground && now.Before(l.pausedUntil) {
			l.mu.Unlock()
			return errMBPaused
		}
		if !now.Before(l.next) && (lane == MBInteractive || l.waiting == 0) {
			l.next = now.Add(interval)
			l.mu.Unlock()
			return nil
		}
		d := l.next.Sub(now)
		if d <= 0 {
			// Only an interactive caller holds this one up: look again shortly.
			d = interval / 4
			if d < 10*time.Millisecond {
				d = 10 * time.Millisecond
			}
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, d); err != nil {
			return err
		}
	}
}

// refused records a 503/429 answer to a background call: the background lane rests,
// for longer each time in a row, up to an hour.
func (l *mbLimiter) refused() {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.pause == 0:
		l.pause = mbPauseFirst
	case l.pause < mbPauseMax:
		l.pause *= 2
		if l.pause > mbPauseMax {
			l.pause = mbPauseMax
		}
	}
	l.pausedUntil = l.now().Add(l.pause)
}

// answered records a good answer: the refusal streak is over.
func (l *mbLimiter) answered() {
	l.mu.Lock()
	l.pause = 0
	l.mu.Unlock()
}

// Paused reports whether background MusicBrainz lookups are resting after a refusal, and
// until when (for the health check).
func MusicBrainzPaused() (bool, time.Time) {
	mbGate.mu.Lock()
	defer mbGate.mu.Unlock()
	until := mbGate.pausedUntil
	return mbGate.now().Before(until), until
}
