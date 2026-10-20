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

// Harsher generator, separate seed range (ADV2_BASE, ADV2_SEEDS): more links, multi-".."
// targets through missing names, links and files, deep records under links.
func TestAdv2Random(t *testing.T) {
	base, n := int64(100000), 300
	if s := os.Getenv("ADV2_BASE"); s != "" {
		fmt.Sscan(s, &base)
	}
	if s := os.Getenv("ADV2_SEEDS"); s != "" {
		fmt.Sscan(s, &n)
	}
	total := 0
	for i := 0; i < n; i++ {
		seed := base + int64(i)
		r := rand.New(rand.NewSource(seed))
		top := t.TempDir()
		names := []string{"A", "a", "Heat", "HEAT", "Gone", "Café", "Café", "L", "..x", "f.mkv"}
		pick := func(s []string) string { return s[r.Intn(len(s))] }
		dirs := []string{"lib"}
		var files, links []string
		for j := 0; j < 10; j++ {
			p := pick(dirs) + "/" + pick(names)
			if os.MkdirAll(filepath.Join(top, p), 0o755) == nil {
				dirs = append(dirs, p)
			}
		}
		for j := 0; j < 10; j++ {
			p := pick(dirs) + "/" + pick(names) + ".mkv"
			if os.WriteFile(filepath.Join(top, p), []byte("x"), 0o644) == nil {
				files = append(files, p)
			}
		}
		for j := 0; j < 16; j++ {
			at := pick(dirs) + "/l" + pick(names)
			pool := append(append(append([]string{}, dirs...), files...), links...)
			dest := pick(pool)
			var tg string
			switch r.Intn(7) {
			case 0:
				tg = filepath.Join(top, dest)
			case 1:
				rel, _ := filepath.Rel(filepath.Join(top, filepath.Dir(at)), filepath.Join(top, dest))
				tg = rel
			case 2: // through a missing name then back
				rel, _ := filepath.Rel(filepath.Join(top, filepath.Dir(at)), filepath.Join(top, dest))
				tg = "Gone" + fmt.Sprint(r.Intn(3)) + "/../" + rel
			case 3: // through a link then back (physical vs lexical ..)
				rel, _ := filepath.Rel(filepath.Join(top, filepath.Dir(at)), filepath.Join(top, dest))
				if len(links) > 0 {
					tg = filepath.Base(pick(links)) + "/../" + rel
				} else {
					tg = rel
				}
			case 4: // through a file then back
				tg = filepath.Join(top, pick(append(files, "lib/none.mkv"))) + "/../" + filepath.Base(dest)
			case 5: // absolute with missing then ..
				tg = top + "/lib/nope/x/../../" + strings.TrimPrefix(dest, "lib/")
			case 6:
				tg = strings.Repeat("../", r.Intn(4)) + dest + pick([]string{"", "/sub", "/../x"})
			}
			if os.Symlink(tg, filepath.Join(top, at)) == nil {
				links = append(links, at)
			}
		}
		pool := append(append(append([]string{}, dirs...), files...), links...)
		var recs []string
		for j := 0; j < 25; j++ {
			p := pick(pool)
			for k := r.Intn(4); k > 0; k-- {
				switch r.Intn(5) {
				case 0:
					p += "/" + pick(names)
				case 1:
					p += "/" + filepath.Base(pick(links))
				case 2:
					p += "/.."
				case 3:
					p = strings.ToUpper(p)
				case 4:
					p += "/Gone"
				}
			}
			recs = append(recs, filepath.Join(top, p))
		}
		var ds []string
		for j := 0; j < 6; j++ {
			ds = append(ds, filepath.Join(top, pick(pool)))
		}
		roots := []string{filepath.Join(top, "lib"), filepath.Join(top, pick(dirs))}
		total += compareAll(t, fmt.Sprintf("adv2 seed%d", seed), recs, ds, roots)
		if total > 30 {
			t.Fatal("too many mismatches")
		}
	}
}

// ENOTDIR variant of finding 1: the lexical target is a file.
func TestAdv2EnotdirVariant(t *testing.T) {
	top := t.TempDir()
	m := filepath.Join(top, "Movies")
	heat := filepath.Join(m, "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	probeFile(t, filepath.Join(m, "file.mkv"))
	mustLink(t, "Gone/../file.mkv", filepath.Join(m, "la"))
	mustLink(t, "Gone/../Heat (1995)/Heat (1995).mkv", filepath.Join(m, "lb"))
	recs := []string{filepath.Join(m, "la", "a.mkv"), filepath.Join(m, "la", "b", "c.mkv"),
		filepath.Join(m, "lb", "x"), filepath.Join(m, "lb")}
	compareAll(t, "enotdir", recs, []string{heat, m, filepath.Join(m, "la")}, []string{m})
	for _, rec := range recs {
		g, ge := newLinkProbe(recs).resolvePath(rec)
		w, we := pathguard.Resolve(rec)
		t.Logf("%s: probe %q,%v full %q,%v", strings.TrimPrefix(rec, top), g, ge, w, we)
	}
}

// Within one pass a path is first seen missing and later appears as a link into Heat.
// Documents cache behaviour (old code re-looked each time).
func TestAdv2AppearsLaterInPass(t *testing.T) {
	top := t.TempDir()
	m := filepath.Join(top, "Movies")
	heat := filepath.Join(m, "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	os.MkdirAll(filepath.Join(m, "Other"), 0o755)
	r1 := filepath.Join(m, "Other", "X", "a.mkv")
	r2 := filepath.Join(m, "Other", "X", "b.mkv")
	n := 0
	fsys := osProbeFS
	fsys.lstat = func(p string) (fs.FileInfo, error) {
		fi, err := os.Lstat(p)
		if p == filepath.Join(m, "Other", "X") {
			n++
			if n == 1 {
				os.Symlink("../Heat (1995)", p)
			}
		}
		return fi, err
	}
	g := newLinkProbeFS(fsys, []string{r1, r2}).recordedInto(heat)
	w := recordedIntoFull(heat, []string{r1, r2})
	t.Logf("appears mid-pass: probe %q, full-after %q", g, w)
}
