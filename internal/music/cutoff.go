package music

import "sort"

// The upgrade side of a music profile (MUS-16): which tiers it would replace a held album
// with, and where it stops.
//
// A profile's ladder is its format scores: a tier scored above zero can be grabbed, and the
// highest score wins. An upgrade must win on both counts that matter: the profile prefers
// it (it scores higher than the tier held) and it is really better audio (BetterTier, so a
// lossless file never replaces another lossless one and nothing replaces a file with a
// lower tier, whatever a profile's scores say).

// tierScore is a tier's score on a ladder (0: not on it).
func tierScore(scores map[string]int, q Quality) int {
	if q == QualityUnknown {
		return 0
	}
	return scores[string(q)]
}

// Cutoff is the tier a profile's ladder aims for: its highest-scored tier (the better tier
// on a tie). Unknown for a ladder with nothing on it.
func Cutoff(scores map[string]int) Quality {
	best := QualityUnknown
	for _, q := range AllQualities {
		s := tierScore(scores, q)
		if s <= 0 {
			continue
		}
		if best == QualityUnknown || s > tierScore(scores, best) {
			best = q
		}
	}
	return best
}

// UpgradeTiers are the tiers on a ladder that would replace held, best first: scored above
// zero, scored above held, and BetterTier than it. Empty when the album is at its cutoff;
// also empty when held is unknown, because an upgrade is never grabbed on a guess.
func UpgradeTiers(held Quality, scores map[string]int) []Quality {
	if held == QualityUnknown {
		return nil
	}
	floor := tierScore(scores, held)
	var out []Quality
	for _, q := range AllQualities {
		if s := tierScore(scores, q); s > 0 && s > floor && BetterTier(q, held) {
			out = append(out, q)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return tierScore(scores, out[i]) > tierScore(scores, out[j]) })
	return out
}

// IsUpgrade reports whether a release in tier cand is an upgrade on held under a ladder.
func IsUpgrade(cand, held Quality, scores map[string]int) bool {
	for _, q := range UpgradeTiers(held, scores) {
		if q == cand {
			return true
		}
	}
	return false
}
