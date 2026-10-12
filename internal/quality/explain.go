package quality

// equalMatch is LostOnText for a release nothing told apart from the one above it.
const equalMatch = "an equal match"

// explainRanks fills LostOn and LostOnText on every eligible release after the first, from
// the deciding factor against the release ranked just above it, and returns what decided the
// winner over the runner-up. evs must already be in Decide's order: each pair is explained
// the way the comparator separated it (decidingFactor walks the same steps), so reading the
// list top to bottom says exactly why each one sits where it does.
//
// Ranking v2's comparator can return the deciding key natively; it then fills the same two
// fields and the text table stays the one place the words come from.
func explainRanks(p Profile, evs []Evaluation) RankKey {
	for i := 1; i < len(evs); i++ {
		k, why := decidingFactor(p, evs[i-1], evs[i])
		if k == "" {
			why = equalMatch
		}
		evs[i].LostOn, evs[i].LostOnText = k, why
	}
	if len(evs) < 2 {
		return ""
	}
	return evs[1].LostOn
}
