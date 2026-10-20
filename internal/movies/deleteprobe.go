package movies

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// A movie delete asks, for the folder it would empty, whether any other film has a file
// recorded in it — through links too (recordedInto) — and whether a link elsewhere points
// into it (aliasInto). Resolving every other film's recorded path in full costs a handful
// of lstat calls per record: tens of thousands on a large library, each a round trip on a
// network share, which can take the delete dialog's preview past a proxy's timeout.
//
// linkProbe gives the very same answers for one pass of those checks, looking at each
// folder once instead of each file:
//   - pathguard.Resolve(p), when p's last part isn't a link, is its folder's resolution
//     with that part joined back — whether the part exists or not (Resolve rejoins a
//     missing tail), and nothing exists under a path that doesn't. So each folder is
//     resolved once, from its own folder's resolution, and only a link (or something that
//     couldn't be looked at) is resolved in full, as before. A library with no links on
//     its paths is never resolved link by link at all.
//   - Whether a folder or file is a link is learnt once per path, from one listing of
//     its folder when that folder holds more than one path the pass asks about, else
//     from one Lstat. A name the listing doesn't show exactly is Lstat'd: the listing
//     never stands in for "missing".
//   - Everything is remembered for the pass only. A probe is made for one plan (the
//     preview's, or the delete's) and a fresh one for the re-check right before
//     leftovers move, so nothing seen earlier hides a change made since. Nothing is kept
//     between deletes.
//
// Any lookup that fails in an unexpected way sends the path to pathguard.Resolve, whose
// error keeps every leftover, exactly as before.

// probeFS is the filesystem a linkProbe looks at; tests count the calls.
type probeFS struct {
	lstat        func(string) (fs.FileInfo, error)
	readDir      func(string) ([]fs.DirEntry, error)
	resolve      func(string) (string, error)
	evalSymlinks func(string) (string, error)
	readlink     func(string) (string, error)
}

var osProbeFS = probeFS{
	lstat:        os.Lstat,
	readDir:      os.ReadDir,
	resolve:      pathguard.Resolve,
	evalSymlinks: filepath.EvalSymlinks,
	readlink:     os.Readlink,
}

// linkProbe answers recordedInto and aliasInto for one pass (see above). Not safe for
// use by more than one goroutine.
type linkProbe struct {
	fs     probeFS
	fast   bool     // resolve folder by folder (not on Windows)
	others []string // every file recorded for other films

	children map[string]int // folder → distinct recorded paths (or their folders) directly in it
	kinds    map[string]entryKind
	lists    map[string]*dirList
	resolved map[string]resolution
	links    map[string]linkTarget
}

type entryKind uint8

const (
	kindMissing entryKind = iota + 1
	kindDir               // a real folder
	kindFile              // anything else that isn't a link
	kindLink
	kindErr // it couldn't be looked at
)

type dirList struct {
	entries []fs.DirEntry
	modes   map[string]fs.FileMode
	err     error
}

type resolution struct {
	path    string
	err     error
	missing bool // nothing is there (known only for paths resolved folder by folder)
}

type linkTarget struct {
	target  string
	err     error
	readErr error // Readlink's, looked at only when err != nil
}

// newLinkProbe makes a probe for one pass over others (other films' recorded paths).
func newLinkProbe(others []string) *linkProbe {
	return newLinkProbeFS(osProbeFS, others)
}

func newLinkProbeFS(fsys probeFS, others []string) *linkProbe {
	p := &linkProbe{
		fs: fsys,
		// Windows' EvalSymlinks rewrites names (case, short names) and treats junctions
		// and mount points its own way: resolve every path in full there, as before
		// (each still only once per pass).
		fast:     runtime.GOOS != "windows",
		others:   others,
		children: map[string]int{},
		kinds:    map[string]entryKind{},
		lists:    map[string]*dirList{},
		resolved: map[string]resolution{},
		links:    map[string]linkTarget{},
	}
	seen := map[string]bool{}
	for _, o := range others {
		if !filepath.IsAbs(o) {
			continue
		}
		for q := filepath.Clean(o); !seen[q]; {
			seen[q] = true
			parent := filepath.Dir(q)
			if parent == q {
				break
			}
			p.children[parent]++
			q = parent
		}
	}
	return p
}

// recordedInto is recordedInto (deletefolder.go) over the probe's records.
func (p *linkProbe) recordedInto(dir string) string {
	clean := filepath.Clean(dir)
	realDir, derr := p.resolvePath(clean)
	for _, o := range p.others {
		if pathInside(o, clean) {
			return o
		}
		if derr != nil {
			return o
		}
		ro, err := p.resolvePath(o)
		if err != nil || pathInside(ro, realDir) {
			return o
		}
	}
	return ""
}

// aliasInto is Service.aliasInto, looking in each of roots and beside dir.
func (p *linkProbe) aliasInto(dir string, roots []string) string {
	realDir, err := p.resolvePath(dir)
	if err != nil {
		return dir
	}
	look := append(append([]string(nil), roots...), filepath.Dir(dir))
	seen := map[string]bool{}
	for _, d := range look {
		if seen[d] {
			continue
		}
		seen[d] = true
		l := p.list(d)
		if l.err != nil {
			continue
		}
		for _, e := range l.entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			link := filepath.Join(d, e.Name())
			lt := p.linkTarget(link)
			if lt.err != nil {
				if lt.readErr != nil {
					return link
				}
				continue // dangling: it points at nothing that exists, so not into dir
			}
			t, r := strings.ToLower(lt.target), strings.ToLower(realDir)
			if t == r || strings.HasPrefix(t, r+string(filepath.Separator)) {
				return link
			}
		}
	}
	return ""
}

// pathInside reports whether p is d or lies under it, as written, ignoring case.
func pathInside(p, d string) bool {
	p, d = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(d))
	return p == d || strings.HasPrefix(p, d+string(filepath.Separator))
}

// resolvePath is pathguard.Resolve(o), worked out once per pass and folder by folder
// (see above).
func (p *linkProbe) resolvePath(o string) (string, error) {
	if r, ok := p.resolved[o]; ok {
		return r.path, r.err
	}
	var r resolution
	if p.fast && strings.TrimSpace(o) != "" && filepath.IsAbs(o) {
		r = p.resolveClean(filepath.Clean(o))
	} else {
		r.path, r.err = p.fs.resolve(o)
	}
	p.resolved[o] = r
	return r.path, r.err
}

// resolveClean resolves c, a clean absolute path, from its folder's resolution: c's
// last part joined back on when it's missing or isn't a link, in full otherwise.
func (p *linkProbe) resolveClean(c string) resolution {
	if r, ok := p.resolved[c]; ok {
		return r
	}
	var r resolution
	parent := filepath.Dir(c)
	full := parent == c // the root: resolved in full, once
	if !full {
		pr := p.resolveClean(parent)
		join := filepath.Join(pr.path, filepath.Base(c))
		switch {
		case pr.err != nil:
			full = true // it fails too; Resolve says how
		case pr.missing:
			r = resolution{path: join, missing: true} // nothing exists under a path that doesn't
		default:
			switch p.kindOf(c) {
			case kindMissing:
				r = resolution{path: join, missing: true}
			case kindDir, kindFile:
				r = resolution{path: join}
			default: // a link, or it couldn't be looked at
				full = true
			}
		}
	}
	if full {
		r = resolution{}
		r.path, r.err = p.fs.resolve(c)
	}
	p.resolved[c] = r
	return r
}

// kindOf is what path is, as Lstat sees it. When its folder holds more than one path the
// pass asks about, that folder is listed once and the listing answers; a name it doesn't
// show exactly (another case on a case-insensitive share, or one that came since) is
// Lstat'd.
func (p *linkProbe) kindOf(path string) entryKind {
	if k, ok := p.kinds[path]; ok {
		return k
	}
	var k entryKind
	if parent := filepath.Dir(path); parent != path && p.children[parent] > 1 {
		if l := p.list(parent); l.err == nil {
			if m, ok := l.modes[filepath.Base(path)]; ok {
				k = kindOfMode(m)
			}
		}
	}
	if k == 0 {
		fi, err := p.fs.lstat(path)
		switch {
		case err == nil:
			k = kindOfMode(fi.Mode())
		case errors.Is(err, fs.ErrNotExist):
			k = kindMissing
		default:
			k = kindErr
		}
	}
	p.kinds[path] = k
	return k
}

func kindOfMode(m fs.FileMode) entryKind {
	switch {
	case m&fs.ModeSymlink != 0:
		return kindLink
	case m.IsDir():
		return kindDir
	default:
		return kindFile
	}
}

// list reads dir once per pass. A listing that failed part way counts as failed.
func (p *linkProbe) list(dir string) *dirList {
	if l, ok := p.lists[dir]; ok {
		return l
	}
	entries, err := p.fs.readDir(dir)
	l := &dirList{entries: entries, err: err}
	if err == nil {
		l.modes = make(map[string]fs.FileMode, len(entries))
		for _, e := range entries {
			l.modes[e.Name()] = e.Type()
		}
	}
	p.lists[dir] = l
	return l
}

// linkTarget is EvalSymlinks(link) once per pass, with Readlink(link) when that fails.
func (p *linkProbe) linkTarget(link string) linkTarget {
	if lt, ok := p.links[link]; ok {
		return lt
	}
	var lt linkTarget
	lt.target, lt.err = p.fs.evalSymlinks(link)
	if lt.err != nil {
		_, lt.readErr = p.fs.readlink(link)
	}
	p.links[link] = lt
	return lt
}
