package download

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Remote path mapping. An external or seedbox qBittorrent reports paths as it sees them
// ("/downloads/Movie.2024"), and Arrmada may have that same folder mounted somewhere else
// ("/media/downloads"). A client's PathMaps translate between the two: every path the
// client reports goes through ToLocal before anything reads it (the shared queue read, so
// imports, stall checks and health all see local paths), and the save path Arrmada sends
// with a new download goes through ToRemote. A client with no mappings — every existing
// one, and the bundled qBittorrent — is untouched: both are then the identity.

// PathMap is one mapping: the folder as the download client names it, and the same
// folder as Arrmada sees it.
type PathMap struct {
	Remote string `json:"remote"`
	Local  string `json:"local"`
}

// PathMaps is a client's mappings, matched longest remote (or local) prefix first.
type PathMaps []PathMap

// maxPathMaps bounds a client's list; more than a handful is a typo or a script.
const maxPathMaps = 20

// remotePath is a client-side path in a form that can be compared: forward slashes,
// cleaned, and whether it's a Windows path (a qBittorrent running on Windows says
// "D:\Torrents\Movie"), which compares without regard to case.
type remotePath struct {
	p       string
	windows bool
}

// parseRemote normalises a path as a download client reports it. ok is false for a path
// that isn't absolute (nothing can be mapped from a relative one).
func parseRemote(p string) (remotePath, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) {
		return remotePath{}, false
	}
	// Only a drive letter or a UNC share makes it Windows: a backslash is a legal character
	// in a Linux file name.
	win := isDriveLetter(p) || strings.HasPrefix(p, `\\`)
	if win {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	switch {
	case win && strings.HasPrefix(p, "//"):
		// A UNC share (\\server\share): path.Clean would fold the leading pair.
		return remotePath{p: "//" + strings.TrimPrefix(path.Clean("/"+strings.TrimLeft(p, "/")), "/"), windows: true}, true
	case strings.HasPrefix(p, "/"):
		return remotePath{p: path.Clean(p), windows: win}, true
	case isDriveLetter(p):
		rest := path.Clean("/" + p[2:])
		return remotePath{p: strings.ToUpper(p[:1]) + ":" + rest, windows: true}, true
	}
	return remotePath{}, false
}

func isDriveLetter(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z') &&
		(len(p) == 2 || p[2] == '/' || p[2] == '\\')
}

// isRoot reports whether a normalised remote path is a whole disk ("/", "D:/", "//host").
func (r remotePath) isRoot() bool {
	if r.p == "/" {
		return true
	}
	if len(r.p) == 3 && r.p[1] == ':' { // "D:/"
		return true
	}
	return strings.HasPrefix(r.p, "//") && !strings.Contains(strings.TrimPrefix(r.p, "//"), "/")
}

// under reports whether r is prefix or inside it, and what follows prefix ("" or "/a/b").
func (r remotePath) under(prefix remotePath) (string, bool) {
	a, b := r.p, prefix.p
	if r.windows || prefix.windows {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	if a == b {
		return "", true
	}
	if strings.HasSuffix(b, "/") { // a root such as "/" or "D:/"
		if strings.HasPrefix(a, b) {
			return "/" + r.p[len(prefix.p):], true
		}
		return "", false
	}
	if strings.HasPrefix(a, b+"/") {
		return r.p[len(prefix.p):], true
	}
	return "", false
}

// String writes the path back in the client's own form.
func (r remotePath) String() string {
	if r.windows {
		return strings.ReplaceAll(r.p, "/", `\`)
	}
	return r.p
}

// localUnder reports whether p is base or inside it (both cleaned), and the rest.
func localUnder(p, base string) (string, bool) {
	if p == base {
		return "", true
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// ToLocal turns a path the client reported into the path Arrmada reads. A path no mapping
// covers comes back exactly as given, so a client without mappings is untouched. The
// remote path is cleaned before matching, so "/downloads/../etc" is not under /downloads,
// and the result is always inside the mapping's local folder.
func (m PathMaps) ToLocal(p string) string {
	if len(m) == 0 || p == "" {
		return p
	}
	rp, ok := parseRemote(p)
	if !ok {
		return p
	}
	best, rest := -1, ""
	for i, pm := range m {
		prefix, ok := parseRemote(pm.Remote)
		if !ok {
			continue
		}
		if r, ok := rp.under(prefix); ok && (best < 0 || len(prefix.p) > len(m[best].normRemote())) {
			best, rest = i, r
		}
	}
	if best < 0 {
		return p
	}
	local := filepath.Clean(m[best].Local)
	out := filepath.Join(local, filepath.FromSlash(strings.TrimPrefix(rest, "/")))
	if _, inside := localUnder(out, local); !inside {
		return p // can't happen with a cleaned rest; refuse rather than escape
	}
	return out
}

func (pm PathMap) normRemote() string {
	r, _ := parseRemote(pm.Remote)
	return r.p
}

// ToRemote turns a folder Arrmada names (its downloads folder, as a save path) into the
// client's path for it. A path no mapping covers comes back as given.
func (m PathMaps) ToRemote(p string) string {
	if len(m) == 0 || p == "" || !filepath.IsAbs(p) {
		return p
	}
	lp := filepath.Clean(p)
	best, rest := -1, ""
	for i, pm := range m {
		local := filepath.Clean(pm.Local)
		if r, ok := localUnder(lp, local); ok && (best < 0 || len(local) > len(filepath.Clean(m[best].Local))) {
			best, rest = i, r
		}
	}
	if best < 0 {
		return p
	}
	remote, ok := parseRemote(m[best].Remote)
	if !ok {
		return p
	}
	if rest != "" {
		remote.p = path.Join(remote.p, filepath.ToSlash(rest))
	}
	return remote.String()
}

// ErrBadPathMap is wrapped by every reason a mapping is refused.
var ErrBadPathMap = errors.New("invalid path mapping")

// defaultDataDir is where the database lives in the container. Media must never be put
// there, whatever ARRMADA_DATA_DIR says.
const defaultDataDir = "/data"

// ValidatePathMaps checks a client's mappings and returns them normalised (cleaned, no
// trailing slashes, blanks dropped). It refuses anything that would let a mapping reach
// where it mustn't: a relative path, a whole disk on either side, a local folder that is,
// holds or sits inside the data folder (the database lives there, never media), and the
// same remote folder twice.
func ValidatePathMaps(maps []PathMap, dataDir string) (PathMaps, error) {
	if len(maps) > maxPathMaps {
		return nil, fmt.Errorf("%w: at most %d mappings per client", ErrBadPathMap, maxPathMaps)
	}
	var protected []string
	for _, d := range []string{dataDir, defaultDataDir} {
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			protected = append(protected, filepath.Clean(abs))
		}
	}
	out := make(PathMaps, 0, len(maps))
	seen := map[string]bool{}
	for _, pm := range maps {
		if strings.TrimSpace(pm.Remote) == "" && strings.TrimSpace(pm.Local) == "" {
			continue
		}
		rp, ok := parseRemote(pm.Remote)
		if !ok {
			return nil, fmt.Errorf("%w: %q isn't a full path as the download client sees it (it starts with / or a drive letter)", ErrBadPathMap, pm.Remote)
		}
		if rp.isRoot() {
			return nil, fmt.Errorf("%w: map the folder the client saves to, not the whole of %s", ErrBadPathMap, rp.String())
		}
		local := strings.TrimSpace(pm.Local)
		if local == "" || strings.ContainsRune(local, 0) || !filepath.IsAbs(local) || !strings.HasPrefix(filepath.ToSlash(local), "/") {
			return nil, fmt.Errorf("%w: %q isn't a full path inside the Arrmada container (it starts with /)", ErrBadPathMap, pm.Local)
		}
		local = filepath.Clean(local)
		if filepath.Dir(local) == local {
			return nil, fmt.Errorf("%w: the Arrmada side can't be the whole filesystem (/)", ErrBadPathMap)
		}
		for _, d := range protected {
			_, inData := localUnder(local, d)
			_, holdsData := localUnder(d, local)
			if inData || holdsData {
				return nil, fmt.Errorf("%w: %s is Arrmada's data folder, where its database lives; downloads and media must never be put there", ErrBadPathMap, local)
			}
		}
		key := rp.p
		if rp.windows {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: %s is mapped twice", ErrBadPathMap, rp.String())
		}
		seen[key] = true
		out = append(out, PathMap{Remote: rp.String(), Local: local})
	}
	return out, nil
}

// encodePathMaps is the column value: "" for none, so an unmapped client's row is as before.
func encodePathMaps(m PathMaps) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// decodePathMaps reads the column. A value that doesn't parse maps nothing rather than
// failing every read of the client list.
func decodePathMaps(s string) PathMaps {
	if s == "" {
		return nil
	}
	var m PathMaps
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}
