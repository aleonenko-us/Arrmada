package automation

import (
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// QueueStallGrace is how long a download must go without progress before the Activity
// queue and a requester's card call it stalled: the stall fail-over's own evidence grace
// (stallEvidenceGrace), the half hour it gives any one reading of the client before acting
// on it.
const QueueStallGrace = stallEvidenceGrace

// StallClocks is what QueueStall knows about a download besides the client's own reading.
// A zero time is "not known".
type StallClocks struct {
	// Grab is an Arrmada grab's stall clock (progress_at): when its download last moved
	// forward, held while it sat paused, queued, checking, moving or allocating
	// (ReconcileAcquisitions). The clock the fail-over counts toward its window.
	Grab time.Time
	// Trying is when this process first saw the download trying to download, after the last
	// time it wasn't (Coordinator.TryingSince). Read only without a Grab clock: it covers
	// torrents no grab knows, which after a restart aren't called stalled until they have
	// been watched trying for the grace again.
	Trying time.Time
}

// trying reports whether a download is trying to download: unfinished, a torrent, and not
// paused (by anyone), queued for a slot, checking, moving, allocating or errored. Only then
// can making no progress mean anything.
func trying(it download.Item) bool {
	if it.Complete() || it.Transport == download.TransportUsenet {
		return false
	}
	if it.State == "paused" || it.State == "checking" || it.State == "error" {
		return false
	}
	switch it.Phase() {
	case "downloading", "stalled", "metadata":
		return true
	}
	// paused, queued, checking, moving, allocating, error, unpacking — or a state this
	// doesn't know, which is never called stalled on a guess.
	return false
}

// QueueStall is the one "is this download stalled?" rule for the people looking at the
// queue: the Activity queue's Stalled pill and row chip, the Dashboard tile and homepage
// widget, Needs you, and a requester's request card. It is built on the clock the stall
// fail-over judges by, so the page never calls a download stalled while the fail-over sees
// it moving.
//
// A download is stalled when all of these hold:
//
//   - It is an unfinished torrent. A usenet job is never replaced for making no progress
//     (usenetlife.go), so it never reads as stalled either.
//   - It is trying to download. Paused (by anyone, the disk guard included), queued for a
//     slot, checking, moving or allocating are the states the fail-over holds its clock
//     through, so they aren't stalls here either; an errored one is its own stage (Failed).
//   - Nothing is arriving right now (down speed 0). Bytes arriving are progress.
//   - Something says it's stuck: it has a grab's stall clock, or qBittorrent itself says no
//     peer is sending (stalledDL) or the file list never came (metaDL). qBittorrent flips
//     into stalledDL whenever no peer sends for a few seconds, which is why its word alone
//     is never enough.
//   - And the latest evidence of life is QueueStallGrace old or older.
//
// The evidence of life is the LATEST of: the grab's stall clock (or, for a torrent no grab
// knows, when this process first saw it trying — StallClocks.Trying), and the client's
// last activity (or when it was added, if nothing ever moved). Any one of them being
// recent means it isn't stalled: a slow pack between whole percents still has a fresh
// last activity; a torrent that sat queued or paused for hours and has just started has a
// held grab clock or a fresh "trying" time. since is that latest time — when it stopped
// showing any sign of progress.
//
// The fail-over itself acts much later — once the grab's stall window (six hours by
// default) has run out on its clock. Stalled here means "worth a look", not "about to be
// replaced".
func QueueStall(it download.Item, clocks StallClocks, heldByGuard bool, now time.Time) (since time.Time, stalled bool) {
	if heldByGuard || it.DownSpeed > 0 || !trying(it) {
		return time.Time{}, false
	}
	phase := it.Phase()
	if clocks.Grab.IsZero() && phase != "stalled" && phase != "metadata" {
		return time.Time{}, false // nothing says it's stuck
	}
	latest := func(at time.Time) {
		// A time in the future (a client clock that's ahead) proves nothing either way.
		if !at.IsZero() && !at.After(now) && at.After(since) {
			since = at
		}
	}
	latest(clocks.Grab)
	if clocks.Grab.IsZero() {
		// A grab's clock is kept in the database, held through queued and paused time, so
		// it survives a restart; only a torrent without one needs this process's watch.
		latest(clocks.Trying)
	}
	last := it.LastActivity
	if last <= 0 {
		last = it.AddedOn
	}
	if last > 0 {
		latest(time.Unix(last, 0))
	}
	if since.IsZero() || now.Sub(since) < QueueStallGrace {
		return time.Time{}, false
	}
	return since, true
}

// noteTrying records, from one read of the clients, when each download started trying to
// download (trying), by lowercased hash. A download that stops trying (paused, queued,
// finished) loses its time and gets a new one when it starts again. On a partial read a
// download that isn't listed may only belong to the client that didn't answer, so its time
// is kept. The times live in memory on purpose: after a restart nothing is called stalled
// until it has been watched for the grace again.
func (c *Coordinator) noteTrying(items []download.Item, whole bool) {
	now := c.clock()
	c.tryingMu.Lock()
	defer c.tryingMu.Unlock()
	if c.trying == nil {
		c.trying = map[string]time.Time{}
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		h := strings.ToLower(it.Hash)
		if h == "" {
			continue
		}
		seen[h] = true
		if !trying(it) {
			delete(c.trying, h)
			continue
		}
		if _, ok := c.trying[h]; !ok {
			c.trying[h] = now
		}
	}
	if !whole {
		return
	}
	for h := range c.trying {
		if !seen[h] {
			delete(c.trying, h)
		}
	}
}

// TryingSince is when this process first saw each download trying to download (since it
// last wasn't), by lowercased hash: StallClocks.Trying. Kept up to date by
// ReconcileAcquisitions. A nil Coordinator knows none.
func (c *Coordinator) TryingSince() map[string]time.Time {
	if c == nil {
		return nil
	}
	c.tryingMu.Lock()
	defer c.tryingMu.Unlock()
	out := make(map[string]time.Time, len(c.trying))
	for h, at := range c.trying {
		out[h] = at
	}
	return out
}
