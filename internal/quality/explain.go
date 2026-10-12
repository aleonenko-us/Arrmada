package quality

// equalMatch is LostOnText for a release nothing told apart from the one above it.
const equalMatch = "an equal match"

// explainRanks fills LostOnText on every eligible release after the first and returns what
// decided the winner over the runner-up. evs must already be in Decide's order, with LostOn
// filled by the comparator that produced it (sortV1 from decidingFactor, sortV2 from
// compareV2), so each pair is explained the way it was actually separated and the
// explanation, the recorded deciding key and ranking v2's shadow record all agree. In
// shadow mode classic ranking decides, so the rows describe classic's order.
//
// The words come from one table per mode: decidingFactor's for classic, v2Reason (which
// uses the same phrases key for key) for ranking v2.
func explainRanks(p Profile, evs []Evaluation, mode string) RankKey {
	for i := 1; i < len(evs); i++ {
		switch {
		case evs[i].LostOn == KeyTie:
			evs[i].LostOnText = equalMatch
		case mode == RankingV2:
			evs[i].LostOnText = v2Reason(p, evs[i].LostOn, evs[i-1], evs[i])
		default:
			_, evs[i].LostOnText = decidingFactor(p, evs[i-1], evs[i])
		}
	}
	return decidedBy(evs)
}
