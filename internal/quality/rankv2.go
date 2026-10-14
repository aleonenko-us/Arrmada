package quality

import (
	"sort"
	"strings"
)

// Ranking v2: releases compared key by key, in order, instead of by one summed score. The
// first key that tells two releases apart decides, and says so — so the goal resolution
// can't be outweighed by stacked preferences, a custom format scored −5 is a score rather
// than a cliff, and every pick names what decided it.
//
// It ships behind the "quality_ranking" setting, default "v1" (classic). While v1 decides,
// the service runs v2 alongside every decision and records where the two pick differently
// (shadow.go), so the owner can review the disagreements before v2 becomes the default.

// Ranking modes ("quality_ranking").
const (
	RankingV1 = "v1" // classic: the summed score, then magnitude, source and seeders
	RankingV2 = "v2" // ordered keys (compareV2)
)

// KeyRanking is the settings key holding the ranking mode.
const KeyRanking = "quality_ranking"

// NormalizeRanking reads a stored mode: anything but "v2" is classic.
func NormalizeRanking(mode string) string {
	if strings.TrimSpace(strings.ToLower(mode)) == RankingV2 {
		return RankingV2
	}
	return RankingV1
}

// ValidRanking reports whether a mode may be saved.
func ValidRanking(mode string) bool { return mode == RankingV1 || mode == RankingV2 }

// What decided a pick when no comparison did.
const (
	KeyOnly RankKey = "only" // the only eligible release
	KeyTie  RankKey = "tie"  // equal on every key: the order they came in
)

// compareV2 orders two eligible evaluations: negative when a ranks first, positive when b
// does, 0 when no key tells them apart. The key is the one that decided (KeyTie for none).
//
// Keys, in order:
//
//  1. avoid — a built-in format's Avoid (target or Other formats), the bitrate floor or an
//     Unwanted avoid (TargetAvoided); a negative custom format or keyword score is not an
//     avoid here
//  2. health — a dead torrent (no seeders) after every seeded one: it never downloads, so
//     like classic ranking it goes last within its tier, whatever else it has
//  3. resolution — the goal first (ResTier)
//  4. preferences — more of the target's Prefer/Must formats (PrefCount; waived ones don't count)
//  5. custom — custom formats and keywords, additive, negatives included (CustomScore)
//  6. source — Remux > BluRay > WEB > HDTV > DVD > unstated (SourceTier), then proper
//  7. group — the release group's tier (GroupTier)
//  8. bitrate — inside the window first; then the higher bitrate (codec-normalised), or the
//     lower when the profile leans small (SmallBias > 0). Within 10% is a tie.
//  9. health — more seeders, for releases whose magnitude is within 10%
//  10. size — the larger file, or the smaller when the profile leans small
func compareV2(p Profile, a, b *Evaluation) (int, RankKey) {
	if a.TargetAvoided != b.TargetAvoided {
		return order(!a.TargetAvoided), KeyAvoid
	}
	if as, bs := a.Candidate.Seeders > 0, b.Candidate.Seeders > 0; as != bs {
		return order(as), KeyHealth
	}
	if a.ResTier != b.ResTier {
		return order(a.ResTier > b.ResTier), KeyResolution
	}
	if a.PrefCount != b.PrefCount {
		return order(a.PrefCount > b.PrefCount), KeyPreferences
	}
	if a.CustomScore != b.CustomScore {
		return order(a.CustomScore > b.CustomScore), KeyCustom
	}
	if a.SourceTier != b.SourceTier {
		return order(a.SourceTier > b.SourceTier), KeySource
	}
	if a.ProperFix != b.ProperFix {
		return order(a.ProperFix), KeyProper
	}
	if a.GroupTier != b.GroupTier {
		return order(a.GroupTier > b.GroupTier), KeyGroup
	}
	small := p.SmallBias > 0
	if a.InWindow != b.InWindow {
		return order(a.InWindow), KeyBitrate
	}
	if a.EffBitrate > 0 && b.EffBitrate > 0 && !nearEqual(a.EffBitrate, b.EffBitrate) {
		return order((a.EffBitrate > b.EffBitrate) != small), KeyBitrate
	}
	am, bm := v2Magnitude(a, b)
	if (am == bm || nearEqual(am, bm)) && a.Candidate.Seeders != b.Candidate.Seeders {
		return order(a.Candidate.Seeders > b.Candidate.Seeders), KeyHealth
	}
	if am != bm {
		return order((am > bm) != small), KeySize
	}
	return 0, KeyTie
}

// order turns "a ranks first" into compareV2's sign.
func order(aFirst bool) int {
	if aFirst {
		return -1
	}
	return 1
}

// v2Magnitude is what the health band and the size key compare: the codec-normalised
// bitrates when both are known, else the sizes.
func v2Magnitude(a, b *Evaluation) (float64, float64) {
	if a.EffBitrate > 0 && b.EffBitrate > 0 {
		return a.EffBitrate, b.EffBitrate
	}
	return a.Candidate.SizeGB, b.Candidate.SizeGB
}

// sortV2 orders eligible evaluations best-first by compareV2 and fills each one's LostOn:
// the key it lost on against the release just above it.
func sortV2(p Profile, evs []Evaluation) {
	sort.SliceStable(evs, func(i, j int) bool {
		c, _ := compareV2(p, &evs[i], &evs[j])
		return c < 0
	})
	fillLostOn(evs, func(above, ev *Evaluation) RankKey {
		_, k := compareV2(p, above, ev)
		return k
	})
}

// fillLostOn sets LostOn on every evaluation after the first, from key(above, it).
func fillLostOn(evs []Evaluation, key func(above, ev *Evaluation) RankKey) {
	for i := range evs {
		evs[i].LostOn = ""
		if i > 0 {
			evs[i].LostOn = key(&evs[i-1], &evs[i])
		}
	}
}

// decidedBy is what decided a ranked list's pick: what the runner-up lost on, KeyOnly with
// one release, "" with none.
func decidedBy(evs []Evaluation) RankKey {
	switch len(evs) {
	case 0:
		return ""
	case 1:
		return KeyOnly
	}
	return evs[1].LostOn
}

// v2Reason is the "Chosen over the … —" text for a v2 comparison: why loser ranked below
// winner on key k. The wording matches decidingFactor's for the same key, so the two modes
// explain a pick in one voice.
func v2Reason(p Profile, k RankKey, winner, loser Evaluation) string {
	wr, lr := winner.Candidate.Release, loser.Candidate.Release
	switch k {
	case KeyAvoid:
		return "it has " + strings.Join(loser.AvoidedFormats, ", ") + ", which you avoid"
	case KeyResolution:
		return "lower resolution"
	case KeyPreferences:
		return preferenceLoss(winner, loser)
	case KeyCustom:
		return "scores lower on your custom formats"
	case KeySource:
		return sourceLoss(wr, lr)
	case KeyProper:
		if sameGroupProper(winner, loser) {
			return "the " + properWord(wr) + " fix replaces it"
		}
		return "it isn't a " + properWord(wr)
	case KeyGroup:
		return groupLoss(winner)
	case KeyBitrate:
		switch {
		case !loser.InWindow:
			return "its bitrate is outside your window"
		case p.SmallBias > 0:
			return "a higher bitrate, and you prefer smaller files"
		}
		return "lower bitrate"
	case KeyHealth:
		if loser.Candidate.Seeders <= 0 && winner.Candidate.Seeders > 0 {
			return "it has no seeders"
		}
		return "fewer seeders"
	case KeySize:
		if p.SmallBias > 0 {
			return "larger, and you prefer smaller files"
		}
		return "smaller file"
	}
	return "an equal match, listed second"
}
