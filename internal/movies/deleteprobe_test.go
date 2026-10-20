package movies

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// recordedIntoFull is recordedInto as it was before linkProbe: every record resolved in
// full, every pass. The probe must give the same answer for every layout.
func recordedIntoFull(dir string, others []string) string {
	clean := filepath.Clean(dir)
	realDir, derr := pathguard.Resolve(clean)
	for _, o := range others {
		if pathInside(o, clean) {
			return o
		}
		if derr != nil {
			return o
		}
		ro, err := pathguard.Resolve(o)
		if err != nil || pathInside(ro, realDir) {
			return o
		}
	}
	return ""
}

// aliasIntoFull is aliasInto as it was before linkProbe.
func aliasIntoFull(dir string, roots []string) string {
	realDir, err := pathguard.Resolve(dir)
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
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			link := filepath.Join(d, e.Name())
			target, err := filepath.EvalSymlinks(link)
			if err != nil {
				if _, lerr := os.Readlink(link); lerr != nil {
					return link
				}
				continue
			}
			t, r := strings.ToLower(target), strings.ToLower(realDir)
			if t == r || strings.HasPrefix(t, r+string(filepath.Separator)) {
				return link
			}
		}
	}
	return ""
}

// probeFile writes a one-byte file at p, making its folders.
func probeFile(t testing.TB, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// probeLayout builds a library with every kind of path a record can take: real folders,
// missing ones, links to folders and files, dangling links, a link loop, a file used as
// a folder, and folders holding many records (so listings answer) or one (Lstat does).
func probeLayout(t testing.TB) (top string, records, dirs []string) {
	t.Helper()
	top = t.TempDir()
	abs := func(rel string) string { return filepath.Join(top, filepath.FromSlash(rel)) }
	for _, f := range []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/poster.jpg",
		"Movies/A (1999)/A (1999).mkv",
		"Movies/Coll/Sub/Deep/x.mkv", "Movies/Coll/Sub/Deep/y.mkv", "Movies/Coll/one.mkv",
		"Movies/plainfile.txt",
		"Movies/Other/real.mkv",
	} {
		probeFile(t, abs(f))
	}
	for _, d := range []string{"Movies/B (2000)", "Movies/Crime"} {
		if err := os.MkdirAll(abs(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := [][2]string{
		{"Movies/Crime/HeatAlias", "../Heat (1995)"},
		{"Movies/Crime/HeatAbs", abs("Movies/Heat (1995)")},
		{"Movies/LinkFile.mkv", "Heat (1995)/Heat (1995).mkv"},
		{"Movies/Other/linked.mkv", "../Heat (1995)/Heat (1995).mkv"},
		{"Movies/Dangling", abs("nowhere/x")},
		{"Movies/DanglingIntoHeat", "Heat (1995)/sub"},
		{"Movies/Loop1", "Loop2"},
		{"Movies/Loop2", "Loop1"},
		{"Movies/Coll/LinkToHeat", "../Heat (1995)"},
		{"Movies/Coll/Sub/Up", ".."},
	}
	for _, l := range links {
		if err := os.Symlink(l[1], abs(l[0])); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}
	for _, r := range []string{
		"Movies/A (1999)/A (1999).mkv",
		"Movies/B (2000)/B (2000).mkv",
		"Movies/Missing (2001)/Missing (2001).mkv",
		"Movies/Missing (2001)/deeper/x.mkv",
		"Movies/heat (1995)/Heat (1995).mkv",
		"Movies/Heat (1995)",
		"Movies/HEAT (1995)/sub/x.mkv",
		"Movies/Heat (1995)/Heat (1995).mkv",
		"Movies/Heat (1995) 4K/x.mkv",
		"Movies/Crime/HeatAlias/Heat 2 (2026).mkv",
		"Movies/Crime/HeatAlias",
		"Movies/Crime/HeatAbs/poster.jpg",
		"Movies/LinkFile.mkv",
		"Movies/Other/linked.mkv",
		"Movies/Other/real.mkv",
		"Movies/Other/gone.mkv",
		"Movies/Dangling/x.mkv",
		"Movies/Dangling",
		"Movies/DanglingIntoHeat/x.mkv",
		"Movies/Loop1/x.mkv",
		"Movies/plainfile.txt/x.mkv",
		"Movies/Coll/Sub/Deep/x.mkv",
		"Movies/Coll/Sub/Deep/y.mkv",
		"Movies/Coll/Sub/Deep/z.mkv",
		"Movies/Coll/one.mkv",
		"Movies/Coll/LinkToHeat/extras/x.mkv",
		"Movies/Coll/Sub/Up/LinkToHeat/x.mkv",
		"nowhere/at/all.mkv",
	} {
		records = append(records, abs(r))
	}
	records = append(records, abs("Movies/Heat (1995)")+string(filepath.Separator), "", "  ", "relative/x.mkv", "/")
	for _, d := range []string{
		"Movies/Heat (1995)", "Movies/A (1999)", "Movies/B (2000)", "Movies/Crime",
		"Movies/Crime/HeatAlias", "Movies/Coll/Sub", "Movies/Coll/LinkToHeat", "Movies/Other",
		"Movies/Missing (2001)", "Movies/Loop1", "Movies/Dangling", "Movies", "nowhere",
	} {
		dirs = append(dirs, abs(d))
	}
	return top, records, dirs
}

// The probe resolves every record exactly as pathguard.Resolve does, and recordedInto
// and aliasInto answer exactly as they did before, record by record and over whole lists
// in either order.
func TestLinkProbeMatchesFullResolve(t *testing.T) {
	top, records, dirs := probeLayout(t)
	probe := newLinkProbe(records)
	for _, rec := range records {
		want, werr := pathguard.Resolve(rec)
		got, gerr := probe.resolvePath(rec)
		if (werr != nil) != (gerr != nil) || got != want {
			t.Errorf("resolve %q: got %q, %v; want %q, %v", rec, got, gerr, want, werr)
		}
	}
	reversed := make([]string, len(records))
	for i, r := range records {
		reversed[len(records)-1-i] = r
	}
	roots := []string{filepath.Join(top, "Movies"), filepath.Join(top, "Movies", "Crime")}
	for _, dir := range dirs {
		for _, rec := range records {
			if got, want := recordedInto(dir, []string{rec}), recordedIntoFull(dir, []string{rec}); got != want {
				t.Errorf("recordedInto(%s, %q) = %q, want %q", dir, rec, got, want)
			}
		}
		for _, list := range [][]string{records, reversed, records[:len(records)-5]} {
			if got, want := newLinkProbe(list).recordedInto(dir), recordedIntoFull(dir, list); got != want {
				t.Errorf("recordedInto(%s, all) = %q, want %q", dir, got, want)
			}
		}
		// One probe answering every folder in turn, as a plan with several does.
		if got, want := probe.recordedInto(dir), recordedIntoFull(dir, records); got != want {
			t.Errorf("shared probe: recordedInto(%s) = %q, want %q", dir, got, want)
		}
		if got, want := probe.aliasInto(dir, roots), aliasIntoFull(dir, roots); got != want {
			t.Errorf("aliasInto(%s) = %q, want %q", dir, got, want)
		}
	}
}

// countingFS counts every filesystem call a probe makes.
type countingFS struct{ lstat, readDir, resolve, eval, readlink int }

func (c *countingFS) fs() probeFS {
	return probeFS{
		lstat:        func(p string) (fs.FileInfo, error) { c.lstat++; return os.Lstat(p) },
		readDir:      func(p string) ([]fs.DirEntry, error) { c.readDir++; return os.ReadDir(p) },
		resolve:      func(p string) (string, error) { c.resolve++; return pathguard.Resolve(p) },
		evalSymlinks: func(p string) (string, error) { c.eval++; return filepath.EvalSymlinks(p) },
		readlink:     func(p string) (string, error) { c.readlink++; return os.Readlink(p) },
	}
}

func (c *countingFS) total() int { return c.lstat + c.readDir + c.resolve + c.eval + c.readlink }

// probeLibrary lays out folders folders of files files each under top/Movies (the first
// missing folders recorded but not on disk), plus Heat's folder, and returns the records
// and Heat's folder.
func probeLibrary(t testing.TB, top string, folders, files, missing int) (records []string, heat string) {
	t.Helper()
	heat = filepath.Join(top, "Movies", "Heat (1995)")
	probeFile(t, filepath.Join(heat, "Heat (1995).mkv"))
	for f := 0; f < folders; f++ {
		dir := filepath.Join(top, "Movies", fmt.Sprintf("Film %d (2000)", f))
		for i := 0; i < files; i++ {
			p := filepath.Join(dir, fmt.Sprintf("part %d.mkv", i))
			if f >= missing {
				probeFile(t, p)
			}
			records = append(records, p)
		}
	}
	return records, heat
}

// A pass looks at each folder once, not each file: ten times the files per folder
// costs no more calls, and only the root is resolved in full. (A file missing
// from a folder that exists is still Lstat'd on its own: a listing never stands in for
// "missing".)
func TestLinkProbeCallsScaleWithFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("every path is resolved in full on Windows")
	}
	calls := func(folders, files int) countingFS {
		records, heat := probeLibrary(t, t.TempDir(), folders, files, 1)
		var c countingFS
		if o := newLinkProbeFS(c.fs(), records).recordedInto(heat); o != "" {
			t.Fatalf("%s counted as in Heat's folder", o)
		}
		return c
	}
	few, many, more := calls(10, 20), calls(10, 200), calls(20, 20)
	if few.total() != many.total() {
		t.Errorf("10 folders: %d calls for 20 files each, %d for 200 each (%+v vs %+v)", few.total(), many.total(), few, many)
	}
	if few.resolve != 1 || many.resolve != 1 { // the root, once
		t.Errorf("link-free records resolved in full: %d, %d", few.resolve, many.resolve)
	}
	// Each folder adds one listing; the folders above are looked at once.
	if d := more.total() - few.total(); d != 10 {
		t.Errorf("10 more folders cost %d more calls, want 10 (%+v vs %+v)", d, more, few)
	}
	if few.total() > 10+strings.Count(filepath.ToSlash(t.TempDir()), "/")+4 {
		t.Errorf("%d calls for 10 folders: %+v", few.total(), few)
	}
}

// A library reached through a link (/data → /mnt/disk1, as on many NAS setups) is still
// looked at folder by folder: the link is resolved once, not once per record, and the
// answers are the full resolve's.
func TestLinkProbeLinkedLibraryRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("every path is resolved in full on Windows")
	}
	top := t.TempDir()
	disk := filepath.Join(top, "disk1")
	records, _ := probeLibrary(t, disk, 10, 20, 1)
	if err := os.Symlink(disk, filepath.Join(top, "data")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	via := func(p string) string { return filepath.Join(top, "data", strings.TrimPrefix(p, disk)) }
	for i := range records {
		records[i] = via(records[i])
	}
	for _, dir := range []string{
		via(filepath.Join(disk, "Movies", "Heat (1995)")),
		filepath.Join(disk, "Movies", "Film 3 (2000)"), // the real path of a recorded folder
		via(filepath.Join(disk, "Movies", "Film 0 (2000)")),
	} {
		var c countingFS
		got := newLinkProbeFS(c.fs(), records).recordedInto(dir)
		if want := recordedIntoFull(dir, records); got != want {
			t.Errorf("recordedInto(%s) = %q, want %q", dir, got, want)
		}
		if c.resolve > 2 { // the root, the link
			t.Errorf("%s: %d paths resolved in full (%+v)", dir, c.resolve, c)
		}
	}
}

// BenchmarkRecordedInto5000 is one delete-check pass over 5,000 other films' records
// (most present, some missing, some recorded through folder links): every record
// resolved in full (as before linkProbe) against the probe. fscalls/op is the probe's
// filesystem calls; the full resolve makes several lstats per record.
func BenchmarkRecordedInto5000(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("every path is resolved in full on Windows")
	}
	top := b.TempDir()
	records, heat := probeLibrary(b, top, 4500, 1, 250) // 250 whole folders missing
	for i := 250; i < 500; i++ {
		// Files missing from folders that exist.
		records = append(records, filepath.Join(top, "Movies", fmt.Sprintf("Film %d (2000)", i), "gone.mkv"))
	}
	for i := 0; i < 250; i++ {
		alias := filepath.Join(top, "Movies", fmt.Sprintf("Alias %d", i))
		if err := os.Symlink(fmt.Sprintf("Film %d (2000)", 1000+i), alias); err != nil {
			b.Skipf("symlinks unavailable here: %v", err)
		}
		records = append(records, filepath.Join(alias, "part 0.mkv"))
	}
	if len(records) != 5000 {
		b.Fatalf("%d records", len(records))
	}
	if recordedIntoFull(heat, records) != "" || recordedInto(heat, records) != "" {
		b.Fatal("a record counted as in Heat's folder")
	}
	b.Run("full-resolve", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			recordedIntoFull(heat, records)
		}
	})
	b.Run("probe", func(b *testing.B) {
		var c countingFS
		for i := 0; i < b.N; i++ {
			newLinkProbeFS(c.fs(), records).recordedInto(heat)
		}
		b.ReportMetric(float64(c.total())/float64(b.N), "fscalls/op")
		b.ReportMetric(float64(c.resolve)/float64(b.N), "fullresolves/op")
	})
}
