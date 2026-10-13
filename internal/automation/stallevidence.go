package automation

import (
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// A torrent missing from the client, or in a hard error state, is a stall verdict on
// sight (stalledInQueue), and a missing one is blocklisted and replaced at once. One read
// is too little to act on. A qBittorrent that is still loading its session after a reboot
// answers with an empty or partial list that reads as complete; a disk that isn't mounted
// yet puts every torrent on it into "missingFiles", which reads as an error. Acted on in
// the first two-minute tick, that blocklists healthy releases and grabs replacements for
// them, three at a time.
//
// It bit hardest on an upgrade: grabs made by a build without fail-over carry a stall
// window of 0, which now resolves to the global default, and they are long past the age
// gate, so every one of them was judged at the first tick after the new build started.
//
// So the evidence has to persist: the torrent must stay gone (or errored) for
// stallEvidenceGrace, measured by this process from the first check that saw it so. The
// sightings live in memory on purpose: after a restart nothing is condemned until it has
// been watched for the full grace again, which costs a dead torrent half an hour and
// protects a live one through whatever the restart was.

// stallEvidenceGrace is how long a torrent must stay gone from complete reads of the
// client, or stay in a hard error, before a fail-over acts on it. The same half hour the
// sweeps give a missing torrent before its title counts as no longer in flight.
const stallEvidenceGrace = missingGrace

// Kinds of evidence a sighting records.
const (
	evidenceGone  = "gone"  // not in a complete read of the client
	evidenceError = "error" // in the client, incomplete, in a hard error state
)

// stallSighting is when this process first saw a grab's current evidence.
type stallSighting struct {
	kind  string
	since time.Time
}

// stallEvidenceOf is what a stall check's read of the client says against a grab: gone,
// errored, or nothing ("").
func stallEvidenceOf(item download.Item, found bool) string {
	switch {
	case !found:
		return evidenceGone
	case item.State == "error" && !item.Complete():
		return evidenceError
	}
	return ""
}

// stallEvidenceHeld records this check's evidence against grab id and reports whether it
// has now held for the full grace. No evidence clears the sighting; a different kind of
// evidence starts its own clock.
func (c *Coordinator) stallEvidenceHeld(id int64, kind string) bool {
	c.stallSeenMu.Lock()
	defer c.stallSeenMu.Unlock()
	if kind == "" {
		delete(c.stallSeen, id)
		return false
	}
	now := c.clock()
	s, ok := c.stallSeen[id]
	if !ok || s.kind != kind {
		if c.stallSeen == nil {
			c.stallSeen = map[int64]stallSighting{}
		}
		c.stallSeen[id] = stallSighting{kind: kind, since: now}
		return false
	}
	return now.Sub(s.since) >= stallEvidenceGrace
}

// pruneStallSightings forgets grabs that are no longer pending.
func (c *Coordinator) pruneStallSightings(pending []grab) {
	keep := make(map[int64]bool, len(pending))
	for _, g := range pending {
		keep[g.ID] = true
	}
	c.stallSeenMu.Lock()
	defer c.stallSeenMu.Unlock()
	for id := range c.stallSeen {
		if !keep[id] {
			delete(c.stallSeen, id)
		}
	}
}
