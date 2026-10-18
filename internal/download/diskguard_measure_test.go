package download

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

// fakeDisks stands in for statfs and st_dev: usage by path (missing = can't be read) and
// a filesystem id by path (missing = can't be read).
type fakeDisks struct {
	used map[string]float64
	dev  map[string]uint64
}

func (f *fakeDisks) install(g *DiskGuard) {
	g.usage = func(p string) (diskspace.Usage, bool) {
		u, ok := f.used[p]
		if !ok {
			return diskspace.Usage{}, false
		}
		return diskspace.Usage{TotalBytes: 1000, FreeBytes: uint64(1000 - u*10), UsedBytes: uint64(u * 10), UsedPct: u}, true
	}
	g.device = func(p string) (uint64, bool) {
		d, ok := f.dev[p]
		return d, ok
	}
}

// On Unraid the downloads folder is a user share whose statfs is the whole array; the
// cache pool is what fills. With the pool mapped in and set as the drive to measure, the
// guard pauses and resumes on the pool's number, not the share's.
func TestGuardMeasuresTheOverride(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{
		{Hash: "aaa", Name: "Active", State: "downloading", ContentPath: "/dl/aaa"},
	})
	g.SetDirFunc(func() string { return "/dl" })
	disks := &fakeDisks{used: map[string]float64{"/dl": 69, "/cache": 91}, dev: map[string]uint64{"/dl": 1, "/cache": 2}}
	disks.install(g)

	// Without the setting, the array's 69% never trips the guard.
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.paused) != 0 {
		t.Fatalf("paused %v on the array's reading", fake.paused)
	}
	if st := g.Status(ctx); st.Override || st.MeasuredPath != "/dl" || st.UsedPct != 69 {
		t.Errorf("status without an override = %+v", st)
	}

	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.paused) != 1 || fake.paused[0] != "aaa" {
		t.Fatalf("paused %v, want the download held on the pool's 91%%", fake.paused)
	}
	st := g.Status(ctx)
	if !st.Override || st.MeasuredPath != "/cache" || st.MeasurePath != "/cache" || st.Path != "/dl" ||
		st.UsedPct != 91 || st.TotalBytes != 1000 || st.FreeBytes != 90 || st.MeasureUnreadable || !g.Engaged(ctx) {
		t.Errorf("status = %+v (engaged %v)", st, g.Engaged(ctx))
	}

	disks.used["/cache"] = 70
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || len(g.held(ctx)) != 0 {
		t.Errorf("resumed %v, held %v — want released once the pool drained", fake.resumed, g.held(ctx))
	}
}

// The saved drive's mount goes away: the guard must not fall back to the downloads folder
// (the very reading the setting replaces), and must not pause or resume on a number it
// can't read. What it holds stays held, the status says why, and once the drive is back
// it carries on.
func TestGuardUnreadableOverrideHoldsAndWarns(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{
		{Hash: "aaa", State: "downloading", ContentPath: "/dl/aaa"},
	})
	g.SetDirFunc(func() string { return "/dl" })
	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{used: map[string]float64{"/dl": 10, "/cache": 95}, dev: map[string]uint64{"/dl": 1, "/cache": 2}}
	disks.install(g)
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.held(ctx)) != 1 {
		t.Fatalf("held %v", g.held(ctx))
	}

	// The pool's mount is removed; the downloads folder reads 10%. A new download starts.
	delete(disks.used, "/cache")
	fake.items = append(fake.items, Item{Hash: "bbb", State: "downloading", ContentPath: "/dl/bbb"})
	for i := 0; i < 3; i++ {
		if err := g.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.resumed) != 0 {
		t.Errorf("resumed %v on the downloads folder's reading", fake.resumed)
	}
	if len(fake.paused) != 1 {
		t.Errorf("paused %v with nothing to measure", fake.paused)
	}
	if h := g.held(ctx); len(h) != 1 || h[0] != "aaa" {
		t.Errorf("held %v, want aaa kept", h)
	}
	st := g.Status(ctx)
	if !st.MeasureUnreadable || st.Measurable || !st.Override || st.MeasuredPath != "/cache" || g.Engaged(ctx) {
		t.Errorf("status = %+v", st)
	}
	if !g.measureLost {
		t.Error("the lost drive wasn't noted")
	}

	// Back, and drained: released.
	disks.used["/cache"] = 50
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || len(g.held(ctx)) != 0 || g.measureLost {
		t.Errorf("resumed %v, held %v, lost %v", fake.resumed, g.held(ctx), g.measureLost)
	}
	if st := g.Status(ctx); st.MeasureUnreadable {
		t.Errorf("status still unreadable: %+v", st)
	}
}

// With several downloads folders, the override stands in for every downloads folder on
// the Downloads folder's filesystem (all of /mnt/user on Unraid is one), while a folder on
// a disk of its own keeps its own reading.
func TestGuardOverrideAppliesToTheDownloadsFilesystem(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{
		{Hash: "main", State: "downloading", ContentPath: "/mnt/user/dl/main"},
		{Hash: "kids", State: "downloading", ContentPath: "/mnt/user/kids-dl/kids"},
		{Hash: "disk3", State: "downloading", ContentPath: "/mnt/disk3/dl/disk3"},
	})
	g.SetDirFunc(func() string { return "/mnt/user/dl" })
	g.SetDirsFunc(func() []string { return []string{"/mnt/user/dl", "/mnt/user/kids-dl", "/mnt/disk3/dl"} })
	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{
		used: map[string]float64{"/mnt/user/dl": 60, "/mnt/user/kids-dl": 60, "/mnt/disk3/dl": 50, "/cache": 91},
		dev:  map[string]uint64{"/mnt/user/dl": 7, "/mnt/user/kids-dl": 7, "/mnt/disk3/dl": 3, "/cache": 9},
	}
	disks.install(g)

	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), fake.paused...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "kids" || got[1] != "main" {
		t.Fatalf("paused %v, want the two on the share (measured on the pool)", got)
	}
	st := g.Status(ctx)
	if len(st.Volumes) != 3 || !st.Volumes[0].Override || !st.Volumes[1].Override || st.Volumes[1].MeasuredPath != "/cache" ||
		st.Volumes[2].Override || st.Volumes[2].MeasuredPath != "/mnt/disk3/dl" || st.MeasuredPath != "/cache" {
		t.Errorf("volumes = %+v", st.Volumes)
	}

	// Disk 3 fills on its own reading.
	disks.used["/mnt/disk3/dl"] = 92
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.held(ctx)) != 3 {
		t.Fatalf("held %v, want all three", g.held(ctx))
	}
	// The pool drains: the share's two go, disk 3's stays.
	disks.used["/cache"] = 60
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	res := append([]string(nil), fake.resumed...)
	sort.Strings(res)
	if len(res) != 2 || res[0] != "kids" || res[1] != "main" {
		t.Fatalf("resumed %v", res)
	}
	if h := g.held(ctx); len(h) != 1 || h[0] != "disk3" {
		t.Errorf("held %v", h)
	}

	// The pool's mount goes while disk 3 is fine: disk 3 is judged as before, the share's
	// folders hold and release nothing.
	fake.items[0].State, fake.items[1].State = "downloading", "downloading" // resumed
	disks.used["/cache"] = 95
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	delete(disks.used, "/cache")
	disks.used["/mnt/disk3/dl"] = 40
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	h := g.held(ctx)
	sort.Strings(h)
	if len(h) != 2 || h[0] != "kids" || h[1] != "main" {
		t.Errorf("held %v, want the share's two kept while the pool can't be read", h)
	}
	if st := g.Status(ctx); !st.MeasureUnreadable || !st.Measurable || st.MeasuredPath != "/mnt/disk3/dl" {
		t.Errorf("status = %+v", st)
	}
}

// A download held for a library folder's own downloads folder that is then cleared,
// while that folder shares the Downloads folder's filesystem: it's judged on the drive the
// guard measures, and an unreadable one keeps it held.
func TestGuardStrayFollowsTheOverride(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{
		{Hash: "kids", State: "downloading", ContentPath: "/mnt/user/kids-dl/kids"},
	})
	dirs := []string{"/mnt/user/dl", "/mnt/user/kids-dl"}
	g.SetDirFunc(func() string { return "/mnt/user/dl" })
	g.SetDirsFunc(func() []string { return dirs })
	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{
		used: map[string]float64{"/mnt/user/dl": 30, "/mnt/user/kids-dl": 30, "/cache": 91},
		dev:  map[string]uint64{"/mnt/user/dl": 7, "/mnt/user/kids-dl": 7, "/cache": 9},
	}
	disks.install(g)
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.held(ctx)) != 1 {
		t.Fatalf("held %v", g.held(ctx))
	}
	dirs = []string{"/mnt/user/dl"} // Kids' downloads folder cleared
	delete(disks.used, "/cache")
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 0 || len(g.held(ctx)) != 1 {
		t.Fatalf("resumed %v on the share's 30%% with the pool unreadable", fake.resumed)
	}
	disks.used["/cache"] = 60
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || len(g.held(ctx)) != 0 {
		t.Errorf("resumed %v, held %v — want released once the pool drained", fake.resumed, g.held(ctx))
	}
}

func TestCheckMeasurePath(t *testing.T) {
	g, _, _, _ := guardFixture(t, nil)
	dl, cache, same := t.TempDir(), t.TempDir(), t.TempDir()
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadable := t.TempDir()
	g.SetDirFunc(func() string { return dl })
	disks := &fakeDisks{
		used: map[string]float64{dl: 50, cache: 40, same: 50},
		dev:  map[string]uint64{dl: 1, cache: 2, same: 1, unreadable: 3},
	}
	disks.install(g)

	if p, err := g.CheckMeasurePath("  "); err != nil || p != "" {
		t.Errorf("empty = %q, %v; want cleared", p, err)
	}
	if p, err := g.CheckMeasurePath(cache + "/"); err != nil || p != cache {
		t.Errorf("good path = %q, %v", p, err)
	}
	for _, c := range []struct{ in, want string }{
		{"cache", "full path starting with /"},
		{filepath.Join(cache, "not-there"), "doesn't exist"},
		{file, "isn't a folder"},
		{unreadable, "can't read how full"},
		{same, "already what the guard measures"},
	} {
		if _, err := g.CheckMeasurePath(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.in, err, c.want)
		}
	}
}
