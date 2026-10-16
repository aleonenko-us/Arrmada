package arrimport

import (
	"strconv"
	"strings"
	"unicode"
)

// Target is one of Arrmada's quality profiles, as the profile picker sees it.
type Target struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	MaxResolution int    `json:"max_resolution"`
	Default       bool   `json:"default"`
}

// MaxResolution is the highest of a profile's allowed resolutions ("2160p", "1080p"…);
// 0 when it names none (any resolution).
func MaxResolution(allowed []string) int {
	best := 0
	for _, r := range allowed {
		digits := strings.TrimRightFunc(strings.TrimSpace(r), func(c rune) bool { return !unicode.IsDigit(c) })
		if n, err := strconv.Atoi(digits); err == nil && n > best {
			best = n
		}
	}
	return best
}

// nameKey compares profile names loosely: "HD-1080p" and "HD 1080p" are the same.
func nameKey(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// SuggestProfile is the Arrmada profile closest to one of the app's: the one of the same
// name, else one reaching the same highest resolution (the default profile first when it
// does), else the default profile. "" only when Arrmada has no profile at all.
func SuggestProfile(p Profile, targets []Target) string {
	if len(targets) == 0 {
		return ""
	}
	if k := nameKey(p.Name); k != "" {
		for _, t := range targets {
			if nameKey(t.Name) == k {
				return t.Key
			}
		}
	}
	var def, sameRes string
	for _, t := range targets {
		if t.Default && def == "" {
			def = t.Key
		}
		if p.MaxResolution > 0 && t.MaxResolution == p.MaxResolution {
			if t.Default {
				return t.Key
			}
			if sameRes == "" {
				sameRes = t.Key
			}
		}
	}
	if sameRes != "" {
		return sameRes
	}
	if def != "" {
		return def
	}
	return targets[0].Key
}
