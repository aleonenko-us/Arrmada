package movies

import (
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// compareAll checks resolvePath, recordedInto (single + list + shared) and aliasInto
// against the full-resolve copies for every dir.
func compareAll(t *testing.T, label string, records, dirs, roots []string) int {
	t.Helper()
	bad := 0
	probe := newLinkProbe(records)
	for _, rec := range records {
		want, werr := pathguard.Resolve(rec)
		got, gerr := probe.resolvePath(rec)
		if (werr != nil) != (gerr != nil) || got != want {
			bad++
			t.Errorf("%s: resolve %q: got %q,%v want %q,%v", label, rec, got, gerr, want, werr)
		}
	}
	for _, dir := range dirs {
		for _, rec := range records {
			if g, w := recordedInto(dir, []string{rec}), recordedIntoFull(dir, []string{rec}); g != w {
				bad++
				t.Errorf("%s: recordedInto(%s,[%s]) = %q want %q", label, dir, rec, g, w)
			}
		}
		if g, w := newLinkProbe(records).recordedInto(dir), recordedIntoFull(dir, records); g != w {
			bad++
			t.Errorf("%s: recordedInto(%s, all) = %q want %q", label, dir, g, w)
		}
		if g, w := probe.recordedInto(dir), recordedIntoFull(dir, records); g != w {
			bad++
			t.Errorf("%s: shared recordedInto(%s) = %q want %q", label, dir, g, w)
		}
		if g, w := probe.aliasInto(dir, roots), aliasIntoFull(dir, roots); g != w {
			bad++
			t.Errorf("%s: aliasInto(%s) = %q want %q", label, dir, g, w)
		}
	}
	return bad
}

func mustLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// Hand-built adversarial layout: link chains, ".." in targets, targets relative to a
// different folder, root via link, record == folder, unicode/case variants.
func TestAdvHandLayout(t *testing.T) {
	top := t.TempDir()
	a := func(r string) string { return filepath.Join(top, filepath.FromSlash(r)) }
	for _, f := range []string{
		"disk/Movies/Heat (1995)/Heat (1995).mkv",
		"disk/Movies/Heat (1995)/extras/e.mkv",
		"disk/Movies/Other/o.mkv",
		"disk/Movies/Other/p.mkv",
		"disk/Movies/Café (2001)/c.mkv",  // NFC
		"disk/Movies/Café (2002)/d.mkv", // NFD
		"disk/deep/a/b/c/f.mkv",
	} {
		probeFile(t, a(f))
	}
	mustLink(t, "disk", a("data"))                                       // root via link
	mustLink(t, "L2", a("disk/Movies/L1"))                               // link -> link
	mustLink(t, "L3", a("disk/Movies/L2"))                               // -> link
	mustLink(t, "Heat (1995)", a("disk/Movies/L3"))                      // -> heat
	mustLink(t, "../Movies/./Heat (1995)/../Heat (1995)", a("disk/x/H")) // .. in target
	mustLink(t, "../../Movies/Heat (1995)", a("disk/deep/a/Up"))         // relative to its own dir
	mustLink(t, "a/Up", a("disk/deep/ViaUp"))                            // link whose target is a link relative elsewhere
	mustLink(t, "../..", a("disk/deep/a/b/c/UpUp"))                      // .. physical through links
	mustLink(t, a("disk/deep/a/b/c"), a("disk/Movies/C"))                // abs link to deep
	mustLink(t, "Heat (1995)/nope/x", a("disk/Movies/DangInHeat"))       // dangling into heat
	mustLink(t, "DangInHeat", a("disk/Movies/DangChain"))                // link -> dangling link
	mustLink(t, "Heat (1995)/Heat (1995).mkv", a("disk/Movies/Other/q.mkv"))
	mustLink(t, "../Heat (1995)", a("disk/Movies/Other/HeatDir"))
	// Movies/C/UpUp -> deep/a/b/c/../.. physically = deep/a ; lexically Movies/C/.. = Movies
	records := []string{
		a("data/Movies/L1/x.mkv"),
		a("data/Movies/L1"),
		a("disk/Movies/L2/extras/e.mkv"),
		a("disk/x/H/new.mkv"),
		a("disk/deep/a/Up/z.mkv"),
		a("disk/deep/ViaUp/z.mkv"),
		a("disk/deep/a/b/c/UpUp/Up/w.mkv"),
		a("disk/Movies/C/UpUp/Up/w.mkv"),
		a("disk/Movies/C/../Heat (1995)/lex.mkv"),
		a("disk/Movies/DangChain/y.mkv"),
		a("disk/Movies/DangChain"),
		a("disk/Movies/Other/o.mkv"),
		a("disk/Movies/Other/p.mkv"),
		a("disk/Movies/Other/q.mkv"),
		a("disk/Movies/Other/HeatDir/h.mkv"),
		a("disk/Movies/Other/HEATDIR/h.mkv"),
		a("disk/Movies/Café (2001)/c.mkv"),
		a("disk/Movies/Café (2001)/c.mkv"),
		a("disk/Movies/Café (2002)/d.mkv"),
		a("disk/Movies/Café (2002)/d.mkv"),
		a("data/Movies/Heat (1995)"),
		a("data"),
		a("data/../disk/Movies/Heat (1995)/Heat (1995).mkv"),
		a("disk/Movies/Heat (1995)/Heat (1995).mkv/sub"),
	}
	dirs := []string{
		a("disk/Movies/Heat (1995)"), a("data/Movies/Heat (1995)"), a("disk/Movies/L1"),
		a("disk/deep/a"), a("disk/deep"), a("disk/Movies/Other"), a("disk/Movies/Café (2001)"),
		a("disk/Movies/Café (2002)"), a("disk/Movies/Heat (1995)/nope"), a("data"), a("disk/Movies"),
	}
	roots := []string{a("disk/Movies"), a("data/Movies")}
	compareAll(t, "hand", records, dirs, roots)
}

// Randomised trees: real dirs/files, relative/absolute/../chained/dangling/looping links,
// records through any of them, in any case. Every answer must match.
func TestAdvRandomLayouts(t *testing.T) {
	seeds := 300
	if s := os.Getenv("ADV_SEEDS"); s != "" {
		fmt.Sscan(s, &seeds)
	}
	total := 0
	for seed := 0; seed < seeds; seed++ {
		r := rand.New(rand.NewSource(int64(seed)))
		if os.Getenv("ADV_TRACE") != "" {
			fmt.Fprintln(os.Stderr, "SEED", seed)
		}
		top := t.TempDir()
		names := []string{"A", "a", "B", "Heat", "HEAT", "x", "Café", "Café", "..x", "L"}
		var dirsRel = []string{"lib"}
		var all []string
		// dirs
		for i := 0; i < 12; i++ {
			p := dirsRel[r.Intn(len(dirsRel))] + "/" + names[r.Intn(len(names))]
			if os.MkdirAll(filepath.Join(top, p), 0o755) == nil {
				dirsRel = append(dirsRel, p)
			}
		}
		// files
		for i := 0; i < 15; i++ {
			p := dirsRel[r.Intn(len(dirsRel))] + "/f" + names[r.Intn(len(names))] + ".mkv"
			if fi, err := os.Lstat(filepath.Join(top, p)); err == nil && fi.IsDir() {
				continue
			}
			os.WriteFile(filepath.Join(top, p), []byte("x"), 0o644)
			all = append(all, p)
		}
		// links
		var linksRel []string
		for i := 0; i < 10; i++ {
			at := dirsRel[r.Intn(len(dirsRel))] + "/l" + names[r.Intn(len(names))]
			var target string
			pool := append(append(append([]string{}, dirsRel...), all...), linksRel...)
			dest := pool[r.Intn(len(pool))]
			switch r.Intn(5) {
			case 0:
				target = filepath.Join(top, dest)
			case 1:
				rel, _ := filepath.Rel(filepath.Join(top, filepath.Dir(at)), filepath.Join(top, dest))
				target = rel
			case 2:
				if os.Getenv("ADV_NO_DOTDOT_MISSING") != "" {
					target = "../" + filepath.Base(dest)
					break
				}
				target = "../" + names[r.Intn(len(names))] + "/../" + filepath.Base(dest)
			case 3:
				target = dest + "/missing/deeper" // relative to wrong folder, likely dangling
			case 4:
				target = strings.Repeat("../", r.Intn(4)) + dest
			}
			if os.Symlink(target, filepath.Join(top, at)) == nil {
				linksRel = append(linksRel, at)
			}
		}
		pool := append(append(append([]string{}, dirsRel...), all...), linksRel...)
		var records []string
		for i := 0; i < 25; i++ {
			p := pool[r.Intn(len(pool))]
			switch r.Intn(5) {
			case 0:
				p += "/" + names[r.Intn(len(names))] + ".mkv"
			case 1:
				p = strings.ToUpper(p)
			case 2:
				p += "/../" + names[r.Intn(len(names))]
			case 3:
				p += "/sub/" + names[r.Intn(len(names))]
			}
			records = append(records, filepath.Join(top, p))
		}
		var dirs []string
		for i := 0; i < 6; i++ {
			dirs = append(dirs, filepath.Join(top, pool[r.Intn(len(pool))]))
		}
		roots := []string{filepath.Join(top, "lib"), filepath.Join(top, dirsRel[r.Intn(len(dirsRel))])}
		if os.Getenv("ADV_DUMP") == fmt.Sprint(seed) {
			filepath.Walk(top, func(p string, fi os.FileInfo, err error) error {
				l, _ := os.Readlink(p)
				t.Logf("FS %s %v -> %q", strings.TrimPrefix(p, top), fi.Mode(), l)
				return nil
			})
			for _, rec := range records {
				w, werr := pathguard.Resolve(rec)
				pr := newLinkProbe(records)
				g, gerr := pr.resolvePath(rec)
				if g != w || (werr != nil) != (gerr != nil) {
					t.Logf("REC %s", strings.TrimPrefix(rec, top))
					for k, v := range pr.kinds {
						if strings.HasPrefix(k, top) && strings.HasPrefix(rec, k) {
							t.Logf("  kind %s=%d", strings.TrimPrefix(k, top), v)
						}
					}
					for k, v := range pr.resolved {
						if strings.HasPrefix(k, top) && strings.HasPrefix(rec, k) {
							t.Logf("  res %s=%+v", strings.TrimPrefix(k, top), v)
						}
					}
				}
			}
		}
		total += compareAll(t, fmt.Sprintf("seed%d", seed), records, dirs, roots)
		if total > 30 {
			t.Fatal("too many mismatches")
		}
	}
}

// A folder that can be listed but not searched (r without x). Run as non-root.
func TestAdvPermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	for _, mode := range []fs.FileMode{0o444, 0o555, 0o333, 0o000, 0o111} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			top := t.TempDir()
			heat := filepath.Join(top, "Movies", "Heat (1995)")
			probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
			locked := filepath.Join(top, "Movies", "Locked (2000)")
			probeFile(t, filepath.Join(locked, "a.mkv"))
			probeFile(t, filepath.Join(locked, "b.mkv"))
			mustLink(t, "../Heat (1995)", filepath.Join(locked, "HeatLink"))
			records := []string{filepath.Join(locked, "a.mkv"), filepath.Join(locked, "b.mkv"),
				filepath.Join(locked, "HeatLink", "x.mkv")}
			os.Chmod(locked, mode)
			defer os.Chmod(locked, 0o755)
			for _, recs := range [][]string{records[:2], records[:1], records} {
				g, w := recordedInto(heat, recs), recordedIntoFull(heat, recs)
				if g != w {
					t.Errorf("mode %o recs %d: probe %q, full %q", mode, len(recs), g, w)
				}
			}
			roots := []string{filepath.Join(top, "Movies"), locked}
			p := newLinkProbe(records)
			if g, w := p.aliasInto(heat, roots), aliasIntoFull(heat, roots); g != w {
				t.Errorf("aliasInto mode %o: %q vs %q", mode, g, w)
			}
		})
	}
}

// fakeDirEntry lets a test lie about a listing's types or contents (stale listing,
// filesystem whose d_type disagrees with lstat).
type fakeDirEntry struct {
	name string
	typ  fs.FileMode
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.typ.IsDir() }
func (f fakeDirEntry) Type() fs.FileMode          { return f.typ }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrInvalid }

// A file replaced by a symlink into Heat after the folder was listed: the probe
// believes the listing; full resolve (later) sees the link.
func TestAdvSwapAfterListing(t *testing.T) {
	top := t.TempDir()
	heat := filepath.Join(top, "Movies", "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	other := filepath.Join(top, "Movies", "Other")
	probeFile(t, filepath.Join(other, "a.mkv"))
	os.MkdirAll(filepath.Join(other, "Sub"), 0o755)
	records := []string{filepath.Join(other, "a.mkv"), filepath.Join(other, "Sub", "x.mkv")}
	fsys := osProbeFS
	fsys.readDir = func(d string) ([]fs.DirEntry, error) {
		e, err := os.ReadDir(d)
		if d == other {
			os.RemoveAll(filepath.Join(other, "Sub"))
			os.Symlink("../Heat (1995)", filepath.Join(other, "Sub"))
		}
		return e, err
	}
	g := newLinkProbeFS(fsys, records).recordedInto(heat)
	w := recordedIntoFull(heat, records)
	if g != w {
		t.Errorf("swap after listing: probe %q, full (after swap) %q", g, w)
	}
}

// A filesystem whose readdir reports a symlink as a regular entry (d_type wrong).
func TestAdvWrongDType(t *testing.T) {
	top := t.TempDir()
	heat := filepath.Join(top, "Movies", "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	other := filepath.Join(top, "Movies", "Other")
	probeFile(t, filepath.Join(other, "a.mkv"))
	mustLink(t, "../Heat (1995)", filepath.Join(other, "Sub"))
	records := []string{filepath.Join(other, "a.mkv"), filepath.Join(other, "Sub", "x.mkv")}
	fsys := osProbeFS
	fsys.readDir = func(d string) ([]fs.DirEntry, error) {
		es, err := os.ReadDir(d)
		var out []fs.DirEntry
		for _, e := range es {
			typ := e.Type()
			if typ&fs.ModeSymlink != 0 {
				typ = fs.ModeDir
			}
			out = append(out, fakeDirEntry{e.Name(), typ})
		}
		return out, err
	}
	g := newLinkProbeFS(fsys, records).recordedInto(heat)
	w := recordedIntoFull(heat, records)
	if g != w {
		t.Errorf("wrong d_type: probe %q, full %q", g, w)
	}
}

// The dangerous shape: a link whose target passes through a missing folder and "..".
// The kernel says everything under it is missing; pathguard.Resolve cleans the target
// lexically and follows what's there.
func TestAdvDotDotThroughMissing(t *testing.T) {
	top := t.TempDir()
	m := filepath.Join(top, "Movies")
	heat := filepath.Join(m, "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	os.MkdirAll(filepath.Join(m, "Coll"), 0o755)
	mustLink(t, "../Heat (1995)", filepath.Join(m, "Coll", "Alias"))
	mustLink(t, "Gone/../Coll", filepath.Join(m, "Weird"))
	rec := filepath.Join(m, "Weird", "Alias", "Heat 2 (2026).mkv")
	w, werr := pathguard.Resolve(rec)
	g, gerr := newLinkProbe([]string{rec}).resolvePath(rec)
	t.Logf("Resolve=%q,%v probe=%q,%v", w, werr, g, gerr)
	gi, wi := recordedInto(heat, []string{rec}), recordedIntoFull(heat, []string{rec})
	t.Logf("recordedInto(Heat): probe %q, full %q", gi, wi)
	if gi != wi {
		t.Errorf("DISAGREE: probe would let Heat's folder be emptied; full resolve keeps it")
	}
}
