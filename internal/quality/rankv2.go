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
// It is the default ranking: "quality_ranking" never saved reads v2. Classic ranking (v1)
// stays one setting away as the rollback — saving "v1" puts every grab and every upgrade
// back on the summed score. Whichever decides, the service runs the other alongside every
// decision and records where the two pick differently (shadow.go), so they can still be
// compared.

// Ranking modes ("quality_ranking").
const (
	RankingV1 = "v1" // classic: the summed score, then magnitude, source and seeders
	RankingV2 = "v2" // ordered keys (compareV2)
)

// KeyRanking is the settings key holding the ranking mode.
const KeyRanking = "quality_ranking"

// DefaultRanking is the mode in effect while "quality_ranking" has never been saved. It is
// a settings default, not a stored value, so an install that saved "v1" keeps classic
// ranking whatever the default is.
const DefaultRanking = RankingV2

// NormalizeRanking reads a mode: anything but "v2" is classic. An engine's own Ranking
// field reads through it too, where "" (an engine built outside the service) is classic;
// the setting reads through ReadRanking, which supplies DefaultRanking when nothing is saved.
func NormalizeRanking(mode string) string {
	if strings.TrimSpace(strings.ToLower(mode)) == RankingV2 {
		return RankingV2
	}
	return RankingV1
}

// ReadRanking is the mode a "quality_ranking" setting puts in effect, from get (a settings
// read that returns def when the key was never saved): DefaultRanking unless "v1" or "v2"
// was saved — a saved blank is no choice, so it reads the default too. The service and the
// settings API both read it here, so they can't disagree.
func ReadRanking(get func(key, def string) string) string {
	v := strings.TrimSpace(get(KeyRanking, DefaultRanking))
	if v == "" {
		return DefaultRanking
	}
	return NormalizeRanking(v)
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
//     like classic ranking it goes last within its tier, whatever else it has (a usenet
//     release is never dead)
//  3. resolution — the goal first (ResTier)
//  4. preferences — more of the target's Prefer/Must formats (PrefCount; waived ones don't count)
//  5. custom — custom formats and keywords, additive, negatives included (CustomScore)
//  6. source — Remux > BluRay > WEB > HDTV > DVD > unstated (SourceTier), then proper
//  7. group — the release group's tier (GroupTier)
//  8. network — the preferred network, when download_network_preference sets one
//  9. bitrate — inside the window first; then the higher bitrate (codec-normalised), or the
//     lower when the profile leans small (SmallBias > 0). Within 10% is a tie.
//  10. health — more seeders, for two torrents whose magnitude is within 10%
//  11. size — the larger file, or the smaller when the profile leans small
func compareV2(p Profile, a, b *Evaluation) (int, RankKey) {
	if a.TargetAvoided != b.TargetAvoided {
		return order(!a.TargetAvoided), KeyAvoid
	}
	if as, bs := a.Candidate.alive(), b.Candidate.alive(); as != bs {
		return order(as), KeyHealth
	}
	if c, k := compareQualityKeys(a, b); c != 0 {
		return c, k
	}
	if a.GroupTier != b.GroupTier {
		return order(a.GroupTier > b.GroupTier), KeyGroup
	}
	if decided, aFirst := p.networkOrder(a.Candidate, b.Candidate); decided {
		return order(aFirst), KeyNetwork
	}
	small := p.SmallBias > 0
	if a.InWindow != b.InWindow {
		return order(a.InWindow), KeyBitrate
	}
	if a.EffBitrate > 0 && b.EffBitrate > 0 && !nearEqual(a.EffBitrate, b.EffBitrate) {
		return order((a.EffBitrate > b.EffBitrate) != small), KeyBitrate
	}
	am, bm := v2Magnitude(a, b)
	if seedersComparable(a.Candidate, b.Candidate) && (am == bm || nearEqual(am, bm)) && a.Candidate.Seeders != b.Candidate.Seeders {
		return order(a.Candidate.Seeders > b.Candidate.Seeders), KeyHealth
	}
	if am != bm {
		return order((am > bm) != small), KeySize
	}
	return 0, KeyTie
}

// compareQualityKeys is compareV2's middle run (keys 3 to 6): resolution, preferences,
// custom, source and proper — what the release is, rather than who made it or how it
// travels. They are also the only keys an upgrade is ever decided on (upgradev2.go), so
// the upgrade sweeps and the import gates read them from the comparator that ranks grabs.
func compareQualityKeys(a, b *Evaluation) (int, RankKey) {
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
	case KeyNetwork:
		return p.networkLoss()
	case KeyBitrate:
		switch {
		case !loser.InWindow:
			return "its bitrate is outside your window"
		case p.SmallBias > 0:
			return "a higher bitrate, and you prefer smaller files"
		}
		return "lower bitrate"
	case KeyHealth:
		if !loser.Candidate.alive() && winner.Candidate.alive() {
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
