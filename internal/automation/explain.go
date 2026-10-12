package automation

import "github.com/tristenlammi/arrmada/internal/quality"

// explainRow adds to an interactive-search row why it ranks where it does, from its
// Evaluation: the formats it matched, the ones it has that the profile avoids, a waived
// preference, and what it lost on against the release ranked just above it.
//
// Only an eligible row gets any of it — a rejected one already says why in its reject
// reason. The "lost on" line needs ranked as well: it compares the row with its neighbour
// in the main ranking, and a usenet, blocklisted or other-track release is ranked in a
// separate list, where "lower than the one above" would point at a release it never
// competed with. Call it after the row's eligibility is final (withIndexerFacts, the
// track filter).
func explainRow(rr RankedRelease, ev quality.Evaluation, ranked bool) RankedRelease {
	if !rr.Eligible {
		return rr
	}
	rr.Matched = ev.Matched
	rr.Avoided = ev.AvoidedFormats
	rr.BonusWaived = ev.BonusWaived
	if ranked && ev.LostOnText != "" {
		rr.LostOn, rr.LostOnKey = ev.LostOnText, string(ev.LostOn)
	}
	return rr
}
