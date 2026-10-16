package indexer

import "context"

type interactiveKey struct{}

// WithInteractive marks ctx as a person's own search: a release modal, a Search button,
// a quality-profile test. Such searches still ask an indexer that background work is
// leaving alone after repeated failures — that is how a fixed indexer gets noticed, and
// one success clears its backoff.
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

// IsInteractive reports whether ctx was marked by WithInteractive.
func IsInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveKey{}).(bool)
	return v
}

// WithoutInteractive marks ctx as background work even when a caller up the chain marked
// it a person's search: work that must leave a backing-off indexer alone however it was
// started (a request's release preview).
func WithoutInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, false)
}

type noBackoffKey struct{}

// WithoutBackoff marks ctx's searches as ones whose failures must never pause an indexer:
// a request's release preview. Its errors still show on the indexer's row, as a Test's do,
// but only real searches move the backoff ladder.
func WithoutBackoff(ctx context.Context) context.Context {
	return context.WithValue(ctx, noBackoffKey{}, true)
}

func noBackoff(ctx context.Context) bool {
	v, _ := ctx.Value(noBackoffKey{}).(bool)
	return v
}
