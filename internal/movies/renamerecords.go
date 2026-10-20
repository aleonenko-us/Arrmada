package movies

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// renameRecords is what one rename pass — a preview of one movie or the whole library, or
// one apply — knows of the files recorded for every film: read from the database once,
// resolved through links once (one linkProbe for the pass), and indexed by folder, so
// asking "does another film have a file in or under this folder?" costs a few map lookups
// instead of resolving every other film's record again for each movie. An apply updates it
// as it moves files (moved), so a later movie in the same pass sees where earlier ones went.
//
// The index gives the same answers as recordedInto: a record counts when it lies in or
// under the folder as written, ignoring case, or once both are resolved through links; a
// record or folder that can't be resolved counts too (refuse rather than guess).
type renameRecords struct {
	load    func() ([]recordedPath, error)
	loaded  bool
	err     error
	byMovie map[int64][]string          // movie → its recorded files, as written
	inDir   map[string]map[string]int64 // lower-case folder → records directly in it → movie
	written map[string]map[int64]string // lower-case path and each folder above it → a record per movie at or under it
	real    map[string]map[int64]string // the same over resolved paths (built on first need)
	failed  map[int64]string            // a record per movie that couldn't be resolved
	probe   *linkProbe
	built   bool // real and failed are built

	// The Movies folders' links, found once per pass (aliasInto).
	rootsListed bool
	rootSet     []string
	rootLinks   []string
	rootErr     string // an entry that couldn't be looked at: every folder refuses
}

// newRenameRecords makes the records for one pass. Nothing is read until it's needed.
func (s *Service) newRenameRecords(ctx context.Context) *renameRecords {
	ctx = context.WithoutCancel(ctx) // a pass that started finishes what it moved
	return &renameRecords{load: func() ([]recordedPath, error) { return s.repo.RecordedPaths(ctx) }}
}

func (r *renameRecords) ensure() error {
	if r.loaded {
		return r.err
	}
	r.loaded = true
	rows, err := r.load()
	if err != nil {
		r.err = err
		return err
	}
	r.byMovie = map[int64][]string{}
	r.inDir = map[string]map[string]int64{}
	r.written = map[string]map[int64]string{}
	var all []string
	for _, row := range rows {
		r.byMovie[row.movieID] = append(r.byMovie[row.movieID], row.path)
		all = append(all, row.path)
		r.add(row.movieID, row.path, false)
	}
	r.probe = newLinkProbe(all)
	return nil
}

// add indexes one record (resolved too once the resolved index exists).
func (r *renameRecords) add(id int64, p string, resolved bool) {
	clean := filepath.Clean(p)
	dir := strings.ToLower(filepath.Dir(clean))
	if r.inDir[dir] == nil {
		r.inDir[dir] = map[string]int64{}
	}
	r.inDir[dir][clean] = id
	addAncestors(r.written, strings.ToLower(clean), id, p)
	if resolved {
		r.addResolved(id, p)
	}
}

func (r *renameRecords) addResolved(id int64, p string) {
	rp, err := r.probe.resolvePath(p)
	if err != nil {
		if _, ok := r.failed[id]; !ok {
			r.failed[id] = p
		}
		return
	}
	addAncestors(r.real, strings.ToLower(filepath.Clean(rp)), id, p)
}

// addAncestors notes record p of movie id under key and every folder above it.
func addAncestors(idx map[string]map[int64]string, key string, id int64, p string) {
	for {
		if idx[key] == nil {
			idx[key] = map[int64]string{}
		}
		if _, ok := idx[key][id]; !ok {
			idx[key][id] = p
		}
		parent := filepath.Dir(key)
		if parent == key {
			return
		}
		key = parent
	}
}

func (r *renameRecords) buildResolved() {
	if r.built {
		return
	}
	r.built = true
	r.real = map[string]map[int64]string{}
	r.failed = map[int64]string{}
	for id, paths := range r.byMovie {
		for _, p := range paths {
			r.addResolved(id, p)
		}
	}
}

// othersIn lists the files recorded for films other than id directly in dir (any case).
func (r *renameRecords) othersIn(dir string, id int64) []string {
	if r.ensure() != nil {
		return nil
	}
	var out []string
	for p, owner := range r.inDir[strings.ToLower(filepath.Clean(dir))] {
		if owner != id {
			out = append(out, p)
		}
	}
	return out
}

// recordedInto returns a file recorded for a film other than id that is dir or lies in or
// under it ("" when none). The records must have loaded (ensure).
func (r *renameRecords) recordedInto(dir string, id int64) string {
	clean := filepath.Clean(dir)
	if o := otherThan(r.written[strings.ToLower(clean)], id); o != "" {
		return o
	}
	realDir, derr := r.probe.resolvePath(clean)
	if derr != nil {
		for owner, paths := range r.byMovie {
			if owner != id && len(paths) > 0 {
				return paths[0]
			}
		}
		return ""
	}
	r.buildResolved()
	if o := otherThan(map[int64]string(r.failed), id); o != "" {
		return o
	}
	return otherThan(r.real[strings.ToLower(filepath.Clean(realDir))], id)
}

// anyInto is recordedInto (deletefolder.go) over paths, resolved with the pass's probe:
// one of them that is dir or lies in or under it, as written or through links ("" when
// none, and nothing is looked at when paths is empty).
func (r *renameRecords) anyInto(dir string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	clean := filepath.Clean(dir)
	realDir, derr := r.probe.resolvePath(clean)
	for _, p := range paths {
		if pathInside(p, clean) || derr != nil {
			return p
		}
		if rp, err := r.probe.resolvePath(p); err != nil || pathInside(rp, realDir) {
			return p
		}
	}
	return ""
}

// aliasInto is Service.aliasInto (the probe's) for one pass that asks it for many
// folders: the links in the Movies folders are found once per pass — the same entries the
// probe would look at each time, by the same rules (a listed link, or anything listed as
// other than a plain file that Lstat calls a link; one that can't be looked at refuses
// every folder) — and only the folder's own parent, when it isn't a Movies folder, is
// left to the probe. It returns the same "" or not as Service.aliasInto.
func (r *renameRecords) aliasInto(roots []string, dir string) string {
	realDir, err := r.probe.resolvePath(dir)
	if err != nil {
		return dir
	}
	if !r.rootsListed {
		r.rootsListed = true
		seen := map[string]bool{}
		for _, d := range roots {
			if seen[d] {
				continue
			}
			seen[d] = true
			r.rootSet = append(r.rootSet, d)
			l := r.probe.list(d)
			if l.err != nil {
				continue
			}
			for _, e := range l.entries {
				link := filepath.Join(d, e.Name())
				if typ := e.Type(); typ&fs.ModeSymlink == 0 {
					if typ.IsRegular() {
						continue
					}
					switch r.probe.kindOf(link) {
					case kindLink:
					case kindErr:
						if r.rootErr == "" {
							r.rootErr = link
						}
						continue
					default:
						continue
					}
				}
				r.rootLinks = append(r.rootLinks, link)
			}
		}
	}
	if r.rootErr != "" {
		return r.rootErr
	}
	rd := strings.ToLower(realDir)
	for _, link := range r.rootLinks {
		lt := r.probe.linkTarget(link)
		if lt.err != nil {
			if lt.readErr != nil {
				return link
			}
			continue // dangling: it points at nothing that exists, so not into dir
		}
		t := strings.ToLower(lt.target)
		if t == rd || strings.HasPrefix(t, rd+string(filepath.Separator)) {
			return link
		}
	}
	for _, d := range r.rootSet {
		if d == filepath.Dir(dir) {
			return "" // its parent is a Movies folder, looked at above
		}
	}
	return r.probe.aliasInto(dir, nil)
}

func otherThan(m map[int64]string, id int64) string {
	for owner, p := range m {
		if owner != id {
			return p
		}
	}
	return ""
}

// moved notes that movie id's file from is now at to (every record of it follows).
func (r *renameRecords) moved(id int64, from, to string) {
	if !r.loaded || r.err != nil {
		return
	}
	paths := r.byMovie[id]
	// Drop the movie from the index, then add its records back as they are now.
	for _, p := range paths {
		clean := filepath.Clean(p)
		delete(r.inDir[strings.ToLower(filepath.Dir(clean))], clean)
		dropAncestors(r.written, strings.ToLower(clean), id)
		if r.built {
			if rp, err := r.probe.resolvePath(p); err == nil {
				dropAncestors(r.real, strings.ToLower(filepath.Clean(rp)), id)
			}
		}
	}
	if r.built {
		delete(r.failed, id)
	}
	for i, p := range paths {
		if p == from {
			paths[i] = to
		}
	}
	for _, p := range paths {
		r.add(id, p, r.built)
	}
}

func dropAncestors(idx map[string]map[int64]string, key string, id int64) {
	for {
		delete(idx[key], id)
		parent := filepath.Dir(key)
		if parent == key {
			return
		}
		key = parent
	}
}
