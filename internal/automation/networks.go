package automation

import (
	"context"
	"sort"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// netPolicy is what one search pass needs to know about the two download networks: which
// of them has a client (so its releases can be grabbed at all), the owner's preference
// between them, and the tie-breaks after it — indexer priority, then the network whose
// first enabled download client comes first in the client order.
//
// The zero value is an install with no usenet client: torrents are grabbable, usenet
// releases aren't, and nothing is reordered — exactly how grabbing behaved before usenet.
type netPolicy struct {
	usenet  bool // an enabled usenet client exists
	torrent bool // an enabled torrent client exists
	prefer  string
	// priority is each indexer's priority by name (1 is the highest); first is the network
	// of the first enabled download client in the client order ("" when unknown).
	priority map[string]int
	first    indexer.Transport
}

// networks reads the policy for one search pass. Without a usenet client it answers the
// zero policy without reading anything else.
func (c *Coordinator) networks(ctx context.Context) netPolicy {
	var p netPolicy
	if c.downloads == nil {
		return p
	}
	p.usenet = c.downloads.HasTransport(ctx, download.TransportUsenet)
	p.torrent = c.downloads.HasTransport(ctx, download.TransportTorrent)
	if !p.usenet {
		return p
	}
	p.prefer = c.quality.NetworkPreference(ctx)
	if c.indexers != nil {
		if list, err := c.indexers.List(ctx); err == nil {
			p.priority = make(map[string]int, len(list))
			for _, ix := range list {
				p.priority[ix.Name] = ix.Priority
			}
		}
	}
	// The client list comes back in the order new downloads try it (priority, then id).
	if clients, err := c.downloads.List(ctx); err == nil {
		for _, cl := range clients {
			t := cl.Kind.Transport()
			if cl.Enabled && t != "" && c.downloads.HasTransport(ctx, t) {
				p.first = indexer.Transport(t)
				break
			}
		}
	}
	return p
}

// isUsenet reports whether rel came from a usenet (Newznab) indexer.
func isUsenet(rel indexer.Release) bool { return rel.Transport == indexer.TransportUsenet }

// canGrab reports whether some download client can take rel. A torrent is grabbable
// unless the only clients are usenet ones — with no client at all, torrents stay
// grabbable, which is how a grab has always failed in that case: loudly, at the client.
func (p netPolicy) canGrab(rel indexer.Release) bool {
	if isUsenet(rel) {
		return p.usenet
	}
	return p.torrent || !p.usenet
}

// grabbable keeps the releases some download client can take.
func (p netPolicy) grabbable(releases []indexer.Release) []indexer.Release {
	out := make([]indexer.Release, 0, len(releases))
	for _, rel := range releases {
		if p.canGrab(rel) {
			out = append(out, rel)
		}
	}
	return out
}

// dropCode is why a release no client can take was left out of a search's picks.
func (p netPolicy) dropCode(rel indexer.Release) string {
	if isUsenet(rel) {
		return DropNotTorrent
	}
	return DropNotUsenet
}

// noteDropped tells the search attempt which releases grabbable took out, and why.
func (p netPolicy) noteDropped(n *searchNotes, before, after []indexer.Release) {
	if n == nil || len(before) == len(after) {
		return
	}
	kept := make(map[string]bool, len(after))
	for _, rel := range after {
		kept[rel.Title] = true
	}
	for _, rel := range before {
		if !kept[rel.Title] {
			n.mark(rel.Title, p.dropCode(rel))
		}
	}
}

// usable is grabbable followed by bestByTitle, noting what grabbable dropped: the releases
// a search pass picks from, one copy per title.
func (p netPolicy) usable(ctx context.Context, releases []indexer.Release) []indexer.Release {
	kept := p.grabbable(releases)
	p.noteDropped(notesFrom(ctx), releases, kept)
	return p.bestByTitle(kept)
}

// bestByTitle collapses duplicate release titles to one copy each. The same scene release
// routinely appears on several indexers; keeping duplicates meant the quality engine could
// score one copy while a later byName lookup returned another (last write wins), so the
// grab used a different indexer, seed policy and download link than the release that won.
//
// One copy per network first — the healthiest torrent (most seeders), the usenet copy from
// the highest-priority indexer — so a usenet copy, which has no seeders, doesn't lose to
// the torrent just for that. Then one per title (pickCopy). With a usenet client the list
// is put in tie-break order (indexer priority, then client order), which is what decides
// between releases the profile ranks the same; without one it keeps the search's order.
func (p netPolicy) bestByTitle(releases []indexer.Release) []indexer.Release {
	type copies struct{ tor, nzb int } // index into releases, -1 for none
	byTitle := make(map[string]*copies, len(releases))
	var titles []string
	for i, rel := range releases {
		cp := byTitle[rel.Title]
		if cp == nil {
			cp = &copies{-1, -1}
			byTitle[rel.Title] = cp
			titles = append(titles, rel.Title)
		}
		switch {
		case isUsenet(rel):
			if cp.nzb < 0 || p.prio(rel) < p.prio(releases[cp.nzb]) {
				cp.nzb = i
			}
		case cp.tor < 0 || rel.Seeders > releases[cp.tor].Seeders:
			cp.tor = i
		}
	}
	out := make([]indexer.Release, 0, len(titles))
	for _, t := range titles {
		cp := byTitle[t]
		switch {
		case cp.tor < 0:
			out = append(out, releases[cp.nzb])
		case cp.nzb < 0:
			out = append(out, releases[cp.tor])
		default:
			out = append(out, p.pickCopy(releases[cp.tor], releases[cp.nzb], cp.nzb < cp.tor))
		}
	}
	if p.usenet {
		sort.SliceStable(out, func(i, j int) bool { return p.tieBefore(out[i], out[j]) })
	}
	return out
}

// pickCopy chooses between the torrent and the usenet copy of one release (the same name,
// so the same file): a torrent nobody seeds never downloads; then the preference; then
// indexer priority and client order; then whichever the search listed first.
func (p netPolicy) pickCopy(tor, nzb indexer.Release, nzbFirst bool) indexer.Release {
	// A copy no client can take is never the one kept.
	if !p.canGrab(nzb) {
		return tor
	}
	if !p.canGrab(tor) || tor.Seeders <= 0 {
		return nzb
	}
	switch p.prefer {
	case quality.NetworkUsenet:
		return nzb
	case quality.NetworkTorrent:
		return tor
	}
	switch {
	case p.tieBefore(nzb, tor):
		return nzb
	case p.tieBefore(tor, nzb):
		return tor
	case nzbFirst:
		return nzb
	}
	return tor
}

// tieBefore is the tie-break between two releases nothing else separates: the higher
// priority indexer (the lower number), then the network whose first enabled download
// client comes first. False both ways is a tie: the order they came in stands.
func (p netPolicy) tieBefore(a, b indexer.Release) bool {
	if pa, pb := p.prio(a), p.prio(b); pa != pb {
		return pa < pb
	}
	if isUsenet(a) != isUsenet(b) && p.first != "" {
		return a.Transport == p.first
	}
	return false
}

// prio is the priority of the indexer rel came from; one no longer configured ranks last.
func (p netPolicy) prio(rel indexer.Release) int {
	if n, ok := p.priority[rel.Indexer]; ok {
		return n
	}
	return 1 << 30
}

// split is usable without the notes, plus the releases no client can take (deduped the
// same way), for an interactive list that shows both: a title that is grabbable on one
// network is listed only as that copy.
func (p netPolicy) split(releases []indexer.Release) (usable, unusable []indexer.Release) {
	usable = p.bestByTitle(p.grabbable(releases))
	have := make(map[string]bool, len(usable))
	for _, rel := range usable {
		have[rel.Title] = true
	}
	var rest []indexer.Release
	for _, rel := range releases {
		if !p.canGrab(rel) && !have[rel.Title] {
			rest = append(rest, rel)
		}
	}
	return usable, p.bestByTitle(rest)
}

// rejectReason is why an interactive row can't be grabbed ("" when it can).
func (p netPolicy) rejectReason(rel indexer.Release) string {
	switch {
	case p.canGrab(rel):
		return ""
	case isUsenet(rel):
		return usenetReject
	}
	return torrentReject
}

// beats is the book and music pickers' comparison: whether a (scored sa) ranks above b
// (scored sb), where a score is the profile's points × 1,000,000 plus the release's
// seeders. Between two releases on one network that's the score as it always was. Across
// networks seeders don't compare — a usenet release has none to count — so it's the
// points, then a live release over a torrent nobody seeds, then the preference, then the
// tie-breaks (tieBefore). False for a tie: the earlier release keeps its place.
func (p netPolicy) beats(a indexer.Release, sa int, b indexer.Release, sb int) bool {
	if isUsenet(a) == isUsenet(b) {
		return sa > sb
	}
	if pa, pb := sa-a.Seeders, sb-b.Seeders; pa != pb {
		return pa > pb
	}
	if la, lb := isUsenet(a) || a.Seeders > 0, isUsenet(b) || b.Seeders > 0; la != lb {
		return la
	}
	switch p.prefer {
	case quality.NetworkUsenet:
		return isUsenet(a)
	case quality.NetworkTorrent:
		return !isUsenet(a)
	}
	return p.tieBefore(a, b)
}

// releaseCandidate is a release as a scoring candidate: its name, size and seeders, and
// whether it's on usenet (always alive, never compared on seeders with a torrent).
func releaseCandidate(rel indexer.Release) quality.Candidate {
	cand := quality.NewCandidate(rel.Title, rel.SizeGB(), rel.Seeders)
	cand.Usenet = isUsenet(rel)
	return cand
}
