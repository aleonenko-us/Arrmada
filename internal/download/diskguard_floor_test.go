package download

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

// The free-space limit pauses on its own (a ZFS pool's percentage reads near 0% however
// full it is), and releases only once there's a margin above it: the floor plus 10%.
func TestGuardFreeSpaceFloor(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{{Hash: "aaa", State: "downloading", ContentPath: "/dl/aaa"}})
	g.SetDirFunc(func() string { return "/dl" })
	_ = set.Set(ctx, KeyDiskGuardPauseFree, "100")
	disks := &fakeDisks{used: map[string]float64{"/dl": 3}, free: map[string]float64{"/dl": 99}, dev: map[string]uint64{"/dl": 1}}
	disks.install(g)

	// 3% "full", 99 GB free: under the floor, so held.
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.paused) != 1 || !g.Engaged(ctx) {
		t.Fatalf("paused %v (engaged %v), want held under the free-space floor", fake.paused, g.Engaged(ctx))
	}
	st := g.Status(ctx)
	if st.PauseFreeBytes != 100*gib || st.ResumeFreeBytes != 110*gib {
		t.Errorf("limits in status: %d / %d", st.PauseFreeBytes, st.ResumeFreeBytes)
	}

	// Back over the floor but inside the margin: still held.
	disks.free["/dl"] = 105
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 0 || !g.Engaged(ctx) {
		t.Fatalf("resumed %v at 105 GB free; the margin is to 110", fake.resumed)
	}
	// Past the margin: released.
	disks.free["/dl"] = 111
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || len(g.held(ctx)) != 0 {
		t.Fatalf("resumed %v, held %v", fake.resumed, g.held(ctx))
	}

	// The percentage still works with the floor on: 90% full with plenty free pauses, and
	// plenty free doesn't release it while the percentage is over the resume line.
	fake.items[0].State = "downloading"
	disks.used["/dl"], disks.free["/dl"] = 90, 400
	_ = g.Check(ctx)
	if len(g.held(ctx)) != 1 {
		t.Fatalf("held %v at 90%%", g.held(ctx))
	}
	disks.used["/dl"] = 82
	_ = g.Check(ctx)
	if len(g.held(ctx)) != 1 {
		t.Errorf("released at 82%% (resume is 80%%)")
	}
}

func TestFreeFloorMargin(t *testing.T) {
	for _, c := range []struct {
		stored string
		floor  uint64
		resume uint64
	}{
		{"", 0, 0}, {"0", 0, 0}, {"abc", 0, 0}, {"-5", 0, 0},
		{"5", 5 * gib, 6 * gib},       // at least 1 GB of margin
		{"100", 100 * gib, 110 * gib}, // 10%
	} {
		l := limits{pause: 85, resume: 80, floor: FreeFloorBytes(c.stored)}
		if l.floor != c.floor || l.resumeFree() != c.resume {
			t.Errorf("%q: floor %d resume %d, want %d / %d", c.stored, l.floor, l.resumeFree(), c.floor, c.resume)
		}
		// Never full and drained at once.
		for _, u := range []diskspace.Usage{{UsedPct: 79, FreeBytes: c.floor}, {UsedPct: 79, FreeBytes: c.resume}, {UsedPct: 85, FreeBytes: c.resume}} {
			if l.full(u) && l.drained(u) {
				t.Errorf("%q: %+v is both full and drained", c.stored, u)
			}
		}
	}
}

// A saved drive that has turned into a RAM or container folder (the mount went, and
// Docker made an empty folder in its place) is as good as unreadable: nothing is paused
// or released on it, and the status says what it reads as.
func TestGuardOverrideOnRAMIsUnreadable(t *testing.T) {
	g, fake, set, ctx := guardFixture(t, []Item{{Hash: "aaa", State: "downloading", ContentPath: "/dl/aaa"}})
	g.SetDirFunc(func() string { return "/dl" })
	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{used: map[string]float64{"/dl": 10, "/cache": 95}, dev: map[string]uint64{"/dl": 1, "/cache": 2}}
	disks.install(g)
	_ = g.Check(ctx)
	if len(g.held(ctx)) != 1 {
		t.Fatalf("held %v", g.held(ctx))
	}
	disks.fs = map[string]uint64{"/cache": diskspace.TypeTmpfs}
	disks.used["/cache"] = 1 // an empty folder in RAM
	_ = g.Check(ctx)
	if len(fake.resumed) != 0 || len(g.held(ctx)) != 1 {
		t.Errorf("resumed %v on a RAM folder's reading", fake.resumed)
	}
	st := g.Status(ctx)
	if !st.MeasureUnreadable || st.MeasureNotDisk != "tmpfs" || st.Measurable || g.Engaged(ctx) {
		t.Errorf("status = %+v", st)
	}
	if got := g.MeasuredPathFor(ctx, "/dl"); got != "/dl" {
		t.Errorf("free-space check would read %s, want the downloads folder while the drive is unusable", got)
	}
}

// With several downloads folders, a hand resume is only refused for a download held on a
// volume the guard can read and that's still over the line — and it names that volume.
func TestHeldNowJudgesEachDownloadByItsFolder(t *testing.T) {
	g, _, set, ctx := guardFixture(t, []Item{
		{Hash: "main", State: "downloading", ContentPath: "/mnt/user/dl/main"},
		{Hash: "disk3", State: "downloading", ContentPath: "/mnt/disk3/dl/disk3"},
	})
	g.SetDirFunc(func() string { return "/mnt/user/dl" })
	g.SetDirsFunc(func() []string { return []string{"/mnt/user/dl", "/mnt/disk3/dl"} })
	_ = set.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{
		used: map[string]float64{"/mnt/user/dl": 40, "/mnt/disk3/dl": 92, "/cache": 91},
		dev:  map[string]uint64{"/mnt/user/dl": 7, "/mnt/disk3/dl": 3, "/cache": 9},
		fs:   map[string]uint64{"/cache": diskspace.TypeZFS},
	}
	disks.install(g)
	_ = g.Check(ctx)
	now := g.HeldNow(ctx)
	if len(now) != 2 || now["main"].MeasuredPath != "/cache" || now["disk3"].MeasuredPath != "/mnt/disk3/dl" {
		t.Fatalf("held now = %+v", now)
	}
	if st := g.Status(ctx); !st.Volumes[0].ZFS || st.Volumes[1].ZFS {
		t.Errorf("zfs flags: %+v", st.Volumes)
	}

	// The pool's mount goes: the share's download can be resumed by hand; disk 3's can't.
	delete(disks.used, "/cache")
	now = g.HeldNow(ctx)
	if _, ok := now["main"]; ok || len(now) != 1 || now["disk3"].UsedPct != 92 {
		t.Errorf("held now with the pool gone = %+v", now)
	}
	if !g.Engaged(ctx) {
		t.Error("disk 3 is still over the line")
	}
}
