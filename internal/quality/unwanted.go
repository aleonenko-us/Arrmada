package quality

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// The Unwanted pack: kinds of release almost nobody wants in a library, on by default and
// shown in every video profile's Rules. Each item is refused outright (reject), ranked in
// the avoided tier — only taken when nothing else is eligible (avoid) — or ignored (allow),
// per profile. The parser reads the traits (parser/unwanted.go); this decides what a
// profile does about them, and says so by name instead of a hidden score.

// Unwanted items.
const (
	UnwantedFullDisc        = "full_disc"         // a BDMV/ISO disc image: the importer can't import it
	UnwantedThreeD          = "three_d"           // 3D (SBS/OU)
	UnwantedExtras          = "extras"            // bonus material, not the feature
	UnwantedUpscaled        = "upscaled"          // upscaled from a lower resolution
	UnwantedForeign         = "foreign"           // audio only in a language nobody here speaks
	UnwantedLowQualityGroup = "low_quality_group" // a group known for over-compressed encodes
	UnwantedDVNoFallback    = "dv_no_fallback"    // Dolby Vision with no HDR10/HDR10+/HLG base
)

// What a profile does about an item.
const (
	UnwantedReject = "reject"
	UnwantedAvoid  = "avoid"
	UnwantedAllow  = "allow"
)

// RejectUnwanted is the reject code for a release refused by an Unwanted item; the reason
// sentence names which.
const RejectUnwanted = "unwanted"

// DefaultLowQualityGroups are the groups the low-quality-group item matches when a profile
// hasn't edited the list. Matched exactly (case aside), never as a substring.
var DefaultLowQualityGroups = []string{"YIFY", "YTS", "MeGusta"}

// Unwanted is a profile's choices for the pack, stored as JSON in quality_profiles.unwanted.
// The zero value is the defaults.
type Unwanted struct {
	// Items maps an item to reject, avoid or allow; a missing item is its default.
	Items map[string]string `json:"items,omitempty"`
	// LowQualityGroups is the low-quality-group item's list; nil means
	// DefaultLowQualityGroups, and an empty list matches nothing.
	LowQualityGroups []string `json:"low_quality_groups"`
}

// UnwantedItem describes one item for the profile editor.
type UnwantedItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Default     string `json:"default"`
}

// unwantedItems are the items in the order they're checked and shown.
var unwantedItems = []UnwantedItem{
	{UnwantedFullDisc, "Full disc image (BR-DISK)", "An untouched Blu-ray folder or ISO — Arrmada can't import these", UnwantedReject},
	{UnwantedThreeD, "3D", "Side-by-side or over-under 3D releases", UnwantedReject},
	{UnwantedExtras, "Extras", "Featurettes and bonus material, not the film or episode", UnwantedReject},
	{UnwantedUpscaled, "Upscaled", "Blown up from a lower resolution to look like 4K", UnwantedAvoid},
	{UnwantedForeign, "Foreign-only audio", "Dubbed only into a language that is neither the title's original language nor one you keep", UnwantedAvoid},
	{UnwantedLowQualityGroup, "Low-quality group", "Groups known for over-compressed encodes", UnwantedAvoid},
	{UnwantedDVNoFallback, "Dolby Vision without HDR fallback", "Plays with wrong colours on screens without Dolby Vision (avoided unless you prefer Dolby Vision)", UnwantedAvoid},
}

// UnwantedCatalog lists the items with their defaults, for the profile editor.
func UnwantedCatalog() []UnwantedItem { return append([]UnwantedItem(nil), unwantedItems...) }

func validUnwantedAction(a string) bool {
	return a == UnwantedReject || a == UnwantedAvoid || a == UnwantedAllow
}

func knownUnwantedItem(k string) bool {
	for _, it := range unwantedItems {
		if it.Key == k {
			return true
		}
	}
	return false
}

// UnwantedError explains a profile's invalid Unwanted choice, "" when every choice is
// valid. Saves are refused on it rather than silently dropping the choice.
func UnwantedError(u Unwanted) string {
	keys := make([]string, 0, len(u.Items))
	for k := range u.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !knownUnwantedItem(strings.TrimSpace(k)) {
			return "unknown Unwanted item " + strconv.Quote(k)
		}
		if !validUnwantedAction(strings.ToLower(strings.TrimSpace(u.Items[k]))) {
			return "Unwanted " + k + " must be reject, avoid or allow"
		}
	}
	return ""
}

// normalized drops unknown items and invalid actions, and trims and dedupes the group list
// (keeping nil as "the default list").
func (u Unwanted) normalized() Unwanted {
	out := Unwanted{}
	for k, a := range u.Items {
		k, a = strings.TrimSpace(k), strings.ToLower(strings.TrimSpace(a))
		if knownUnwantedItem(k) && validUnwantedAction(a) {
			if out.Items == nil {
				out.Items = map[string]string{}
			}
			out.Items[k] = a
		}
	}
	if u.LowQualityGroups != nil {
		out.LowQualityGroups = []string{}
		seen := map[string]bool{}
		for _, g := range u.LowQualityGroups {
			g = strings.TrimSpace(g)
			if g == "" || seen[strings.ToLower(g)] {
				continue
			}
			seen[strings.ToLower(g)] = true
			out.LowQualityGroups = append(out.LowQualityGroups, g)
		}
	}
	return out
}

// IsDefault reports whether nothing has been changed from the defaults.
func (u Unwanted) IsDefault() bool {
	n := u.normalized()
	return len(n.Items) == 0 && n.LowQualityGroups == nil
}

// groups is the low-quality-group list in effect.
func (u Unwanted) groups() []string {
	if u.LowQualityGroups == nil {
		return DefaultLowQualityGroups
	}
	return u.LowQualityGroups
}

// action is what profile p does about item: its own choice, else the default. Dolby
// Vision without a fallback is allowed by default when the target prefers or requires
// Dolby Vision — someone who asked for it has the screens for it.
func (p Profile) unwantedAction(item string) string {
	if p.NoUnwanted {
		return UnwantedAllow
	}
	if a := p.Unwanted.Items[item]; validUnwantedAction(a) {
		return a
	}
	if item == UnwantedDVNoFallback && p.prefersDV() {
		return UnwantedAllow
	}
	for _, it := range unwantedItems {
		if it.Key == item {
			return it.Default
		}
	}
	return UnwantedAllow
}

// prefersDV reports a profile that scores Dolby Vision up or requires it.
func (p Profile) prefersDV() bool {
	return p.FormatScores["Dolby Vision"] > 0 || containsStr(p.Required, "Dolby Vision")
}

// unwantedHit is one Unwanted item a release has: why it's refused, and how it's named in
// the avoided list ("it has <avoid>, which you avoid").
type unwantedHit struct {
	item, reject, avoid string
}

// unwantedHits lists the items release c has, in check order. Book and music profiles
// (NoUnwanted) are never judged: "TS Eliot" isn't a telesync and "Extras" is a fine title.
func (p Profile) unwantedHits(c Candidate) []unwantedHit {
	if p.NoUnwanted {
		return nil
	}
	r := c.Release
	var out []unwantedHit
	if r.FullDisc {
		out = append(out, unwantedHit{UnwantedFullDisc, "A full Blu-ray disc image — Arrmada can't import these", "Full disc image"})
	}
	if r.ThreeD {
		out = append(out, unwantedHit{UnwantedThreeD, "A 3D release — this profile never grabs those", "3D"})
	}
	if r.Extras {
		out = append(out, unwantedHit{UnwantedExtras, "Extras or bonus material, not the feature itself", "Extras"})
	}
	if r.Upscaled {
		out = append(out, unwantedHit{UnwantedUpscaled, "Upscaled from a lower resolution — this profile refuses upscales", "Upscaled"})
	}
	// A profile that says which audio languages it wants (a Must or a Prefer in its Audio
	// language row) has said what "foreign" means for itself: the item defers to that, or a
	// German-preferring profile would see every German release avoided as foreign-only.
	if lang, ok := foreignOnly(r, c.OrigLanguage, p.OwnerLanguages); ok && !positiveLangRules(p.languageRules(c)) {
		name := langs.Name(lang)
		out = append(out, unwantedHit{UnwantedForeign,
			fmt.Sprintf("%s-only — this title's original language is %s", name, langs.Name(c.OrigLanguage)),
			"Foreign-only audio (" + name + ")"})
	}
	if g, ok := lowQualityGroup(c, p.Unwanted.groups()); ok {
		out = append(out, unwantedHit{UnwantedLowQualityGroup, "From a low-quality group (" + g + ") — this profile refuses them", "Low-quality group (" + g + ")"})
	}
	// Someone avoiding Dolby Vision outright already has it in the avoided tier.
	if dvNoFallback(r) && p.FormatScores["Dolby Vision"] >= 0 {
		out = append(out, unwantedHit{UnwantedDVNoFallback, "Dolby Vision without an HDR10 fallback — this profile refuses those", "Dolby Vision without HDR fallback"})
	}
	return out
}

// dvNoFallback reports Dolby Vision with no HDR10, HDR10+ or HLG base layer to fall back on.
func dvNoFallback(r parser.Release) bool {
	return containsStr(r.HDR, "DV") && !containsStr(r.HDR, "HDR10") && !containsStr(r.HDR, "HDR10+") && !containsStr(r.HDR, "HLG")
}

// foreignOnly reports a release whose stated audio is only in languages that are neither
// the title's original language nor one of the owner's, returning the first. A release
// that states no language, says MULTi/DUAL, or a title whose original language isn't
// known is never judged.
func foreignOnly(r parser.Release, orig string, owner []string) (string, bool) {
	if len(r.Languages) == 0 || r.Multi {
		return "", false
	}
	if _, ok := langs.Lookup(orig); !ok {
		return "", false
	}
	if len(owner) == 0 {
		owner = []string{"en"}
	}
	same := func(a, b string) bool { return langs.Base(a) == langs.Base(b) }
	for _, l := range r.Languages {
		if same(l, orig) {
			return "", false
		}
		for _, o := range owner {
			if same(l, o) {
				return "", false
			}
		}
	}
	return r.Languages[0], true
}

// lowQualityGroup matches the release's group against the list, exactly and ignoring case:
// "MYTSGRP" is not YTS. Besides the parsed group it reads the tag such groups put at the
// end instead ("...-YTS.MX", "...[YTS.MX]"), with or without its site suffix.
func lowQualityGroup(c Candidate, list []string) (string, bool) {
	var names []string
	if c.Release.Group != "" {
		names = append(names, c.Release.Group)
	}
	name := strings.TrimSpace(c.Name)
	switch {
	case strings.HasSuffix(name, "]"):
		if i := strings.LastIndex(name, "["); i >= 0 {
			names = append(names, name[i+1:len(name)-1])
		}
	default:
		if i := strings.LastIndex(name, "-"); i >= 0 {
			names = append(names, name[i+1:])
		}
	}
	for _, n := range append([]string(nil), names...) {
		if base, _, ok := strings.Cut(n, "."); ok {
			names = append(names, base)
		}
	}
	for _, n := range names {
		for _, g := range list {
			if strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(g)) {
				return g, true
			}
		}
	}
	return "", false
}

// unwantedSummary is the profile's non-default choices in a line ("3D allowed, YTS not a
// low-quality group"), "" when it runs the defaults.
func (u Unwanted) summary() string {
	var parts []string
	for _, it := range unwantedItems {
		if a, ok := u.Items[it.Key]; ok && a != it.Default {
			parts = append(parts, strings.ToLower(it.Label[:1])+it.Label[1:]+" "+map[string]string{
				UnwantedReject: "refused", UnwantedAvoid: "avoided", UnwantedAllow: "allowed"}[a])
		}
	}
	if u.LowQualityGroups != nil && !sameGroups(u.LowQualityGroups, DefaultLowQualityGroups) {
		parts = append(parts, "edited low-quality groups")
	}
	return strings.Join(parts, ", ")
}

func sameGroups(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := lowerSorted(a), lowerSorted(b)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func lowerSorted(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strings.ToLower(x)
	}
	sort.Strings(out)
	return out
}
