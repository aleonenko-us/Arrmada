package automation

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// Every automatic pick records what decided it (grabs.decided_by / decided_mode, migration
// 0274). The search's collector notes each decision's winner and its deciding key as the
// quality engine ranks; the grab recorders read the key back for the release they record.
// A grab no ranking chose — a manual pick, an upload, a book or an album — records none.

// Decided modes besides the ranking modes (quality.RankingV1 / RankingV2).
const decidedUpgrade = "upgrade" // an upgrade over the file on disk; the key is the gain

// pickInfo is what decided one picked release.
type pickInfo struct {
	key  quality.RankKey
	mode string
}

// notePick remembers what decided a decision's winner.
func (n *searchNotes) notePick(d quality.Decision) {
	if d.Winner == nil {
		return
	}
	n.picked(d.Winner.Candidate.Name, d.DecidedBy, d.Ranking)
}

// picked remembers what decided the pick of title.
func (n *searchNotes) picked(title string, key quality.RankKey, mode string) {
	if n == nil || title == "" || key == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.picks == nil {
		n.picks = map[string]pickInfo{}
	}
	n.picks[title] = pickInfo{key: key, mode: mode}
}

// pickOf is what decided the pick of title under ctx's search: ("", "") when no ranking
// chose it.
func pickOf(ctx context.Context, title string) (key, mode string) {
	n := notesFrom(ctx)
	if n == nil {
		return "", ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.picks[title]
	return string(p.key), p.mode
}

// upgradePick is the quality service's UpgradeCandidate that also notes the kind of gain,
// so the upgrade grab records what decided it.
func (c *Coordinator) upgradePick(ctx context.Context, profile string, cur quality.CurrentFile, cands []quality.Candidate) (quality.Candidate, bool) {
	pick, key, ok := c.quality.UpgradePick(ctx, profile, cur, cands)
	if ok {
		notesFrom(ctx).picked(pick.Name, key, decidedUpgrade)
	}
	return pick, ok
}
