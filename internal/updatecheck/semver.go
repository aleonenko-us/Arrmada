package updatecheck

import (
	"strconv"
	"strings"
)

// semver is a parsed version: MAJOR.MINOR.PATCH with an optional pre-release.
type semver struct {
	num [3]int
	pre []string // dot-separated pre-release identifiers; empty = a final release
}

// parseSemver reads "v1.2.3", "1.2.3-rc.1" or "1.2.3+build" (build metadata is ignored).
// update.sh's "-dirty" mark on a build with local edits isn't a pre-release and is dropped.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s = strings.TrimSuffix(s, "-dirty")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		if s[i+1:] == "" {
			return semver{}, false
		}
		v.pre = strings.Split(s[i+1:], ".")
		for _, id := range v.pre {
			if id == "" {
				return semver{}, false
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		v.num[i] = n
	}
	return v, true
}

// compare is -1, 0 or 1 as a is older than, the same as, or newer than b (SemVer 2.0:
// a pre-release is older than its release; identifiers compare numerically when both
// are numbers, otherwise as text, and numbers sort before text).
func (a semver) compare(b semver) int {
	for i := range a.num {
		if a.num[i] != b.num[i] {
			if a.num[i] < b.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// newer reports whether latest is a newer version than current. Anything that doesn't
// parse is never "newer".
func newer(latest, current string) bool {
	l, ok1 := parseSemver(latest)
	c, ok2 := parseSemver(current)
	return ok1 && ok2 && l.compare(c) > 0
}
