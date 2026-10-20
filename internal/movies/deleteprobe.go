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
// linkProbe gives the very same answers for one pass of those checks, resolving each
// folder once instead of each file:
//   - pathguard.Resolve(p), when p's last part isn't a link, is its folder's resolution
//     with that part joined back; when it's missing below a folder resolved this way,
//     too (Resolve rejoins a missing tail), and nothing exists under it. So each folder
//     is resolved once, from its own folder's resolution, and only a link, a part that
//     couldn't be looked at, or a missing part below a link is resolved in full, as
//     before. A library with no links on its paths is never resolved link by link.
//   - Whether a part is a link is learnt with one Lstat per distinct path, never from a
//     folder listing's entry types. aliasInto lists the Movies folders and the movie's
//     parent, and Lstats every entry not listed as a plain file (most already were, as
//     parts of records' paths).
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
	err     error
}

type resolution struct {
	path    string
	err     error
	missing bool // nothing is there (known only for paths resolved folder by folder)
	full    bool // resolved in full by pathguard.Resolve
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
	return &linkProbe{
		fs: fsys,
		// Windows' EvalSymlinks rewrites names (case, short names) and treats junctions
		// and mount points its own way: resolve every path in full there, as before
		// (each still only once per pass).
		fast:     runtime.GOOS != "windows",
		others:   others,
		kinds:    map[string]entryKind{},
		lists:    map[string]*dirList{},
		resolved: map[string]resolution{},
		links:    map[string]linkTarget{},
	}
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

// aliasInto is Service.aliasInto, looking in each of roots and beside dir. An entry the
// listing doesn't call a plain file is Lstat'd: a link listed as a folder still counts,
// and one that can't be looked at is returned (refuse rather than guess).
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
			link := filepath.Join(d, e.Name())
			if typ := e.Type(); typ&fs.ModeSymlink == 0 {
				// A listing's types aren't trusted for links (some FUSE and SMB
				// filesystems list a link as a folder): anything listed as other than a
				// plain file is Lstat'd, once per pass. One that can't be looked at counts.
				if typ.IsRegular() {
					continue
				}
				switch p.kindOf(link) {
				case kindLink:
				case kindErr:
					return link
				default:
					continue
				}
			}
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
// last part joined back on when it isn't a link (or is missing below a folder resolved
// folder by folder), in full otherwise.
//
// A missing part below a folder that was itself resolved in full is resolved in full
// too, and so is everything below it. Resolve follows a link that doesn't lead anywhere
// by its written target, cleaned as text ("Gone/../Coll" is "Coll" even with Gone
// missing), while Lstat walks the disk and finds nothing there: only Resolve can say
// what such a path resolves to.
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
			// Nothing exists under a path that doesn't, and pr was resolved folder by
			// folder down from real folders, so Resolve rejoins c's name the same way.
			r = resolution{path: join, missing: true}
		default:
			switch p.kindOf(c) {
			case kindMissing:
				if pr.full {
					full = true
				} else {
					r = resolution{path: join, missing: true}
				}
			case kindDir, kindFile:
				r = resolution{path: join}
			default: // a link, or it couldn't be looked at
				full = true
			}
		}
	}
	if full {
		r = resolution{full: true}
		r.path, r.err = p.fs.resolve(c)
	}
	p.resolved[c] = r
	return r
}

// kindOf is what path is, as Lstat sees it, looked at once per pass. Always Lstat: a
// folder listing's entry types aren't trusted (some FUSE and SMB filesystems report a
// link as a folder), and a folder that can be listed but not entered must fail here as
// it fails Resolve.
func (p *linkProbe) kindOf(path string) entryKind {
	if k, ok := p.kinds[path]; ok {
		return k
	}
	var k entryKind
	fi, err := p.fs.lstat(path)
	switch {
	case err == nil:
		k = kindOfMode(fi.Mode())
	case errors.Is(err, fs.ErrNotExist):
		k = kindMissing
	default:
		k = kindErr
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

// list reads dir once per pass, for aliasInto (which trusts its types only for plain
// files). A listing that failed part way counts as failed.
func (p *linkProbe) list(dir string) *dirList {
	if l, ok := p.lists[dir]; ok {
		return l
	}
	entries, err := p.fs.readDir(dir)
	l := &dirList{entries: entries, err: err}
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
