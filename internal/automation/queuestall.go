package automation

import (
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// QueueStallGrace is how long a download must go without progress before the Activity
// queue and a requester's card call it stalled: the stall fail-over's own evidence grace
// (stallEvidenceGrace), the half hour it gives any one reading of the client before acting
// on it.
const QueueStallGrace = stallEvidenceGrace

// QueueStall is the one "is this download stalled?" rule for the people looking at the
// queue: the Activity queue's Stalled pill and row chip, and a requester's request card.
// It is built on the clock the stall fail-over judges by, so the page never calls a
// download stalled while the fail-over sees it moving.
//
// A download is stalled when all of these hold:
//
//   - It is an unfinished torrent. A usenet job is never replaced for making no progress
//     (usenetlife.go), so it never reads as stalled either.
//   - It is trying to download. Paused (by anyone, the disk guard included), queued for a
//     slot, checking, moving or allocating are the states the fail-over holds its clock
//     through, so they aren't stalls here either; an errored one is its own stage (Failed).
//   - Nothing is arriving right now (down speed 0). Bytes arriving are progress, which
//     restarts the fail-over's clock.
//   - It has gone QueueStallGrace without progress by either of two clocks (below).
//
// The first clock is an Arrmada grab's stall clock (the grab's progress_at): the clock the
// fail-over counts toward its window, and the one the row's "No progress for …" line
// reads. The second is the client's own word: qBittorrent says no peer is sending
// (stalledDL) or the file list never came (metaDL), and nothing has moved for the grace by
// its last activity (or since it was added, if nothing ever did). That covers torrents
// Arrmada didn't grab, and grabs whose clock hasn't started. qBittorrent flips into
// stalledDL whenever no peer sends for a few seconds, which is why its word needs the
// grace too.
//
// clock is the grab's stall clock (zero for a torrent no grab knows, or one never
// observed). since is when it stopped making progress: the earlier clock that holds.
//
// The fail-over itself acts much later — once the grab's stall window (six hours by
// default) has run out on the same clock. Stalled here means "worth a look", not "about
// to be replaced".
func QueueStall(it download.Item, clock time.Time, heldByGuard bool, now time.Time) (since time.Time, stalled bool) {
	if it.Complete() || it.Transport == download.TransportUsenet || heldByGuard || it.DownSpeed > 0 {
		return time.Time{}, false
	}
	if it.State == "paused" || it.State == "checking" || it.State == "error" {
		return time.Time{}, false
	}
	phase := it.Phase()
	switch phase {
	case "downloading", "stalled", "metadata":
		// Trying to download.
	default:
		// paused, queued, checking, moving, allocating, error, unpacking — or a state this
		// doesn't know, which is never called stalled on a guess.
		return time.Time{}, false
	}
	consider := func(at time.Time) {
		if at.IsZero() || at.After(now) || now.Sub(at) < QueueStallGrace {
			return
		}
		if !stalled || at.Before(since) {
			since, stalled = at, true
		}
	}
	consider(clock)
	if phase == "stalled" || phase == "metadata" {
		last := it.LastActivity
		if last <= 0 {
			last = it.AddedOn
		}
		if last > 0 {
			consider(time.Unix(last, 0))
		}
	}
	return since, stalled
}
