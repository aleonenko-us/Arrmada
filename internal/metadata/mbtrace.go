package metadata

import (
	"context"
	"errors"
	"sync/atomic"
)

// MBTrace counts what the MusicBrainz questions asked with one context cost: answers that
// came over the network, answers served from the cache, stale answers served because
// MusicBrainz couldn't be asked again, and questions nobody could answer. The scheduled
// refresh paces itself on Fetched (its call budget) and reads StaleFallback/Failed as
// "MusicBrainz didn't answer": a reason to change nothing, never to remove anything.
type MBTrace struct {
	Fetched       atomic.Int64
	FromCache     atomic.Int64
	StaleFallback atomic.Int64
	Failed        atomic.Int64
}

// Unanswered is how many questions MusicBrainz didn't answer (a stale answer or none).
func (t *MBTrace) Unanswered() int64 {
	if t == nil {
		return 0
	}
	return t.StaleFallback.Load() + t.Failed.Load()
}

type mbTraceKey struct{}

// WithMBTrace returns a context whose MusicBrainz questions are counted in the returned
// trace. A context that already carries a trace keeps counting into that one too: the
// nearest trace wins, so a caller can count one artist inside a run it also counts.
func WithMBTrace(ctx context.Context) (context.Context, *MBTrace) {
	t := &MBTrace{}
	return context.WithValue(ctx, mbTraceKey{}, &chainTrace{own: t, parent: mbTraceOf(ctx)}), t
}

// chainTrace counts into its own trace and every enclosing one.
type chainTrace struct {
	own    *MBTrace
	parent *chainTrace
}

func (c *chainTrace) add(pick func(*MBTrace) *atomic.Int64) {
	for ; c != nil; c = c.parent {
		pick(c.own).Add(1)
	}
}

func mbTraceOf(ctx context.Context) *chainTrace {
	c, _ := ctx.Value(mbTraceKey{}).(*chainTrace)
	return c
}

// The lookup-side counters: one per outcome, each counted up the chain.
var (
	traceFetched       = func(t *MBTrace) *atomic.Int64 { return &t.Fetched }
	traceFromCache     = func(t *MBTrace) *atomic.Int64 { return &t.FromCache }
	traceStaleFallback = func(t *MBTrace) *atomic.Int64 { return &t.StaleFallback }
	traceFailed        = func(t *MBTrace) *atomic.Int64 { return &t.Failed }
)

type mbRevalidateKey struct{}

// WithMBRevalidate marks ctx's MusicBrainz questions as wanting the newest answer: a cached
// answer past its freshness is asked again right away on the caller's lane (not left to a
// background refresh), and still served if MusicBrainz can't answer. Fresh answers cost
// nothing either way, so the per-kind freshness rules still decide how often anything is
// asked.
func WithMBRevalidate(ctx context.Context) context.Context {
	return context.WithValue(ctx, mbRevalidateKey{}, true)
}

func mbRevalidating(ctx context.Context) bool {
	v, _ := ctx.Value(mbRevalidateKey{}).(bool)
	return v
}

type mbFreshNotFoundKey struct{}

// WithMBFreshNotFound marks ctx's MusicBrainz questions as asking "does this still exist?":
// when a stale cached answer is asked again (WithMBRevalidate) and MusicBrainz answers
// 404 now, the lookup reports not found, though the cache still keeps its old answer for
// every other caller. A cached answer MusicBrainz couldn't be asked about is still served:
// that is "unknown", never "gone".
func WithMBFreshNotFound(ctx context.Context) context.Context {
	return context.WithValue(ctx, mbFreshNotFoundKey{}, true)
}

func mbFreshNotFound(ctx context.Context) bool {
	v, _ := ctx.Value(mbFreshNotFoundKey{}).(bool)
	return v
}

// IsNotFound reports whether a metadata error means the provider answered "no such thing"
// (a 404), as opposed to not answering at all.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }
