package arrimport

import (
	"fmt"
	"path"
	"strings"
)

// PathMap rewrites the start of a path as Radarr or Sonarr see it (From, in their container)
// into where the same folder is inside Arrmada's (To): /movies → /storage/movies.
type PathMap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// slashes is p with Windows separators as forward slashes and no trailing slash.
func slashes(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// windowsy reports whether p is a Windows path ("D:/Movies" after slashes).
func windowsy(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z'
}

// under reports whether p is prefix or inside it (Windows paths case-insensitively).
func under(p, prefix string) bool {
	if prefix == "" {
		return false
	}
	eq := func(a, b string) bool { return a == b }
	if windowsy(prefix) {
		eq = strings.EqualFold
	}
	if len(p) < len(prefix) || !eq(p[:len(prefix)], prefix) {
		return false
	}
	return len(p) == len(prefix) || prefix == "/" || p[len(prefix)] == '/'
}

// Remap is p, a path in the app's container, rewritten by the map with the longest From
// that is p or one of its parents. A path no map covers comes back as it was (with forward
// slashes): Arrmada may see it at the same place.
func Remap(p string, maps []PathMap) string {
	p = slashes(p)
	best := -1
	for i, m := range maps {
		from := slashes(m.From)
		if under(p, from) && (best < 0 || len(from) > len(slashes(maps[best].From))) {
			best = i
		}
	}
	if best < 0 {
		return p
	}
	from, to := slashes(maps[best].From), slashes(maps[best].To)
	rest := strings.TrimPrefix(p[len(from):], "/")
	if rest == "" {
		return path.Clean(to)
	}
	return path.Join(to, rest)
}

// CleanPathMaps checks the person's maps: each needs both sides, To must be an absolute
// path inside Arrmada's container, and no From may appear twice. Blank rows are dropped.
func CleanPathMaps(maps []PathMap) ([]PathMap, error) {
	out := make([]PathMap, 0, len(maps))
	seen := map[string]bool{}
	for _, m := range maps {
		from, to := slashes(m.From), slashes(m.To)
		if from == "" && to == "" {
			continue
		}
		if from == "" || to == "" {
			return nil, fmt.Errorf("each path mapping needs both sides (%q → %q)", m.From, m.To)
		}
		if !strings.HasPrefix(to, "/") {
			return nil, fmt.Errorf("%q isn't a path inside Arrmada's container — it should start with / (for example /storage/movies)", m.To)
		}
		key := from
		if windowsy(from) {
			key = strings.ToLower(from)
		}
		if seen[key] {
			return nil, fmt.Errorf("%q is mapped twice", m.From)
		}
		seen[key] = true
		out = append(out, PathMap{From: from, To: path.Clean(to)})
	}
	return out, nil
}

// SuggestPathMaps is a map for every root folder matched to an Arrmada folder at another
// path (the same share mounted at /movies there and /storage/movies here).
func SuggestPathMaps(roots []Mapping, folders []Folder) []PathMap {
	byID := map[int64]string{}
	for _, f := range folders {
		byID[f.ID] = f.Path
	}
	var out []PathMap
	for _, r := range roots {
		if r.FolderID == nil {
			continue
		}
		to, ok := byID[*r.FolderID]
		if !ok || slashes(to) == "" || slashes(to) == slashes(r.Path) {
			continue
		}
		out = append(out, PathMap{From: slashes(r.Path), To: slashes(to)})
	}
	return out
}
