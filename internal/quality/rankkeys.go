package quality

import "github.com/tristenlammi/arrmada/internal/parser"

// Rank keys: an Evaluation taken apart into the components ranking v2 compares one at a
// time, in order (rankv2.go) — instead of the one summed Total, where stacked preferences
// can outscore the goal resolution and any negative custom score is a cliff.
//
// Filling them changes nothing: Total, Avoided and every decision stay exactly as they
// were (the ranking golden file pins that). They only describe the release.

// Source tiers for the source key: WEB-DL and WEBRip are one WEB tier, an unstated source
// sits below every stated one but is never a cam, and a cam (eligible only on a profile
// that allows pre-release copies) is below everything.
const (
	srcTierCAM     = -1
	srcTierUnknown = 0
	srcTierDVD     = 1
	srcTierHDTV    = 2
	srcTierWEB     = 4
	srcTierBluRay  = 5
	srcTierRemux   = 6
)

// rankSourceTier is a source's tier for the source key.
func rankSourceTier(s parser.Source) int {
	switch s {
	case parser.SourceRemux:
		return srcTierRemux
	case parser.SourceBluray:
		return srcTierBluRay
	case parser.SourceWebDL, parser.SourceWebRip:
		return srcTierWEB
	case parser.SourceHDTV:
		return srcTierHDTV
	case parser.SourceDVD:
		return srcTierDVD
	case parser.SourceCAM:
		return srcTierCAM
	}
	return srcTierUnknown
}

// fillRankKeys sets the decomposed keys of an eligible evaluation. TargetAvoided and
// CustomAvoided are set while scoring (Evaluate), where it's known which kind of format
// carried a negative score.
func (ev *Evaluation) fillRankKeys(p Profile) {
	c := ev.Candidate
	r := c.Release
	// The goal (the highest allowed resolution) is the top tier, fallbacks below it by
	// rank; a resolution the profile doesn't allow never gets here, so the plain ladder
	// orders both a listed and an any-resolution profile correctly.
	ev.ResTier = resRank[r.Resolution]
	ev.PrefCount = 0
	for _, m := range ev.Matched {
		// Matched holds the target formats the release has that the profile prefers or
		// requires, next to custom formats and keywords — only the target ones count here.
		if isTargetFormat(m) {
			ev.PrefCount++
		}
	}
	ev.SourceTier = rankSourceTier(r.Source)
	ev.ProperFix = isProper(r)
	ev.GroupTier = p.tierOf(r.Group)
	ev.EffBitrate = c.effectiveBitrateMbps()
	ev.InWindow = inWindow(p, c)
}

// inWindow reports a bitrate inside the resolution's window — or no window, or no bitrate
// to judge (an unknown runtime). Above the ceiling is rejected before ranking, so in
// practice this is "not under the floor".
func inWindow(p Profile, c Candidate) bool {
	w, ok := p.Windows[ResolutionKey(c.Release.Resolution)]
	if !ok || (w.Min <= 0 && w.Max <= 0) {
		return true
	}
	br := c.bitrateMbps()
	if br <= 0 {
		return true
	}
	return (w.Min <= 0 || br >= w.Min) && (w.Max <= 0 || br <= w.Max)
}
