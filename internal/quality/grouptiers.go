package quality

import (
	"fmt"
	"strings"
)

// Release-group tiers: two short lists of groups a profile trusts most (tier 1) and next
// (tier 2). Under ranking v2 they order otherwise-equal releases after source — a tier-1
// remux above another remux — and say so ("not a tier-1 release group"). They never reject
// anything, and classic ranking (v1) ignores them. Groups to stay away from belong in the
// Unwanted pack's low-quality list instead, so one group is never in both.

// GroupTiers is a profile's tiers, stored as JSON in quality_profiles.group_tiers.
type GroupTiers struct {
	Tier1 []string `json:"tier1"`
	Tier2 []string `json:"tier2"`
}

// The GroupTier values the v2 group key compares: higher ranks first.
const (
	groupTierNone = 0
	groupTierTwo  = 1
	groupTierOne  = 2
)

// maxTierGroups bounds each list.
const maxTierGroups = 100

// normalized trims and dedupes both lists (case aside); a group listed in both stays in
// tier 1 only. Empty lists stay empty, not nil, so "no tiers" survives a round trip.
func (g GroupTiers) normalized() GroupTiers {
	seen := map[string]bool{}
	clean := func(in []string) []string {
		out := []string{}
		for _, n := range in {
			n = strings.TrimSpace(n)
			if n == "" || seen[strings.ToLower(n)] {
				continue
			}
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
		return out
	}
	return GroupTiers{Tier1: clean(g.Tier1), Tier2: clean(g.Tier2)}
}

// effectiveTiers is the tiers a stored profile runs with: its own, or the built-in
// suggestions when it has never set any (nil).
func effectiveTiers(g *GroupTiers) GroupTiers {
	if g == nil {
		return DefaultGroupTiers()
	}
	return g.normalized()
}

// tierOf is a release group's GroupTier: tier 1, tier 2 or none. Exact, ignoring case —
// "FLUXX" is not FLUX. A group the profile counts as low quality never gets a tier.
func (p Profile) tierOf(group string) int {
	group = strings.TrimSpace(group)
	if group == "" {
		return groupTierNone
	}
	for _, g := range p.Unwanted.groups() {
		if strings.EqualFold(g, group) {
			return groupTierNone
		}
	}
	for _, g := range p.GroupTiers.Tier1 {
		if strings.EqualFold(g, group) {
			return groupTierOne
		}
	}
	for _, g := range p.GroupTiers.Tier2 {
		if strings.EqualFold(g, group) {
			return groupTierTwo
		}
	}
	return groupTierNone
}

// GroupTiersError explains tiers a save must refuse, "" when they're fine: a group in both
// tiers, one the profile also lists as low quality, or an over-long list.
func GroupTiersError(g *GroupTiers, u Unwanted) string {
	if g == nil {
		return ""
	}
	if len(g.Tier1) > maxTierGroups || len(g.Tier2) > maxTierGroups {
		return fmt.Sprintf("a release-group tier can hold at most %d groups", maxTierGroups)
	}
	one := map[string]bool{}
	for _, n := range g.Tier1 {
		one[strings.ToLower(strings.TrimSpace(n))] = true
	}
	for _, n := range g.Tier2 {
		if one[strings.ToLower(strings.TrimSpace(n))] {
			return fmt.Sprintf("%s is in both release-group tiers — keep it in one", strings.TrimSpace(n))
		}
	}
	low := u.normalized().groups()
	for _, n := range append(append([]string(nil), g.Tier1...), g.Tier2...) {
		for _, l := range low {
			if strings.EqualFold(strings.TrimSpace(n), l) {
				return fmt.Sprintf("%s is one of your low-quality groups (Rules → Unwanted) — take it out of one list", strings.TrimSpace(n))
			}
		}
	}
	return ""
}

// sameTiers reports two tier sets with the same groups in each tier, case and order aside.
func sameTiers(a, b GroupTiers) bool {
	return sameGroups(a.Tier1, b.Tier1) && sameGroups(a.Tier2, b.Tier2)
}

// groupLoss is the "Chosen over the … —" text for a release that lost on the group key.
func groupLoss(winner Evaluation) string {
	if winner.GroupTier == groupTierOne {
		return "not a tier-1 release group"
	}
	return "not in one of your release-group tiers"
}
