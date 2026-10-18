package download

import (
	"context"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

const gib = 1 << 30

// limits are the guard's lines: the pause and resume percentages, and the optional
// free-space floor (KeyDiskGuardPauseFree, in bytes; 0 = off).
type limits struct {
	pause, resume int
	floor         uint64
}

func (g *DiskGuard) limits(ctx context.Context) limits {
	pause, resume := g.thresholds(ctx)
	return limits{pause: pause, resume: resume, floor: FreeFloorBytes(g.settings.Get(ctx, KeyDiskGuardPauseFree, ""))}
}

// FreeFloorBytes reads a stored free-space limit (whole GB) as bytes: 0 for empty,
// nonsense or zero, which all mean off.
func FreeFloorBytes(stored string) uint64 {
	n, err := strconv.Atoi(strings.TrimSpace(stored))
	if err != nil || n <= 0 {
		return 0
	}
	return uint64(n) * gib
}

// full is over either line: the percentage at or above the pause point, or less free
// space than the floor.
func (l limits) full(u diskspace.Usage) bool {
	return u.UsedPct >= float64(l.pause) || (l.floor > 0 && u.FreeBytes < l.floor)
}

// drained is back under both lines, so what was held can go: the percentage at or below
// the resume point, and the free space back above the floor by a margin (resumeFree).
// Never true while full, so the two can't flap.
func (l limits) drained(u diskspace.Usage) bool {
	return u.UsedPct <= float64(l.resume) && (l.floor == 0 || u.FreeBytes >= l.resumeFree())
}

// resumeFree is the free space a drive needs before held downloads restart: the floor
// plus 10%, and at least 1 GB more. Resuming the moment free space crept over the floor
// would pause again a few minutes of downloading later, as the percentage's own gap
// between pause and resume avoids.
func (l limits) resumeFree() uint64 {
	if l.floor == 0 {
		return 0
	}
	margin := l.floor / 10
	if margin < gib {
		margin = gib
	}
	return l.floor + margin
}
