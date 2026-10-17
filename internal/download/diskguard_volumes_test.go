package download

import (
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

// With a library folder's own downloads folder on another disk (GitHub #11), the guard
// watches each volume on its own: only downloads saving onto a full volume are held, and
// they're released when THAT volume drains, whatever the other one is doing.
func TestGuardJudgesEachDownloadsVolume(t *testing.T) {
	g, fake, _, ctx := guardFixture(t, []Item{
		{Hash: "main1", Name: "On the main drive", State: "downloading", ContentPath: "/dl/main1"},
		{Hash: "disk2a", Name: "On disk 2", State: "downloading", ContentPath: "/mnt/disk2/downloads/disk2a"},
		{Hash: "disk2s", Name: "Seeding on disk 2", State: "seeding", ContentPath: "/mnt/disk2/downloads/disk2s"},
		{Hash: "nopath", Name: "Magnet still fetching", State: "downloading"},
	})
	g.SetDirFunc(func() string { return "/dl" })
	g.SetDirsFunc(func() []string { return []string{"/dl", "/mnt/disk2/downloads", "/dl"} })
	used := map[string]float64{"/dl": 50, "/mnt/disk2/downloads": 91}
	g.usage = func(p string) (diskspace.Usage, bool) {
		u, ok := used[p]
		return diskspace.Usage{UsedPct: u}, ok
	}

	// Disk 2 is full: only its download is held; the main drive's keeps going, and
	// seeding is left alone.
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.paused) != 1 || fake.paused[0] != "disk2a" {
		t.Fatalf("paused %v, want only the download on the full disk", fake.paused)
	}
	if h := g.held(ctx); len(h) != 1 || h[0] != "disk2a" {
		t.Fatalf("held %v", h)
	}
	st := g.Status(ctx)
	if st.Path != "/mnt/disk2/downloads" || st.UsedPct != 91 || len(st.Volumes) != 2 || !g.Engaged(ctx) {
		t.Errorf("status = %+v (engaged %v), want the fullest volume and both listed", st, g.Engaged(ctx))
	}

	// The main drive filling up holds its downloads too — a download with no path yet
	// counts as the main drive's, where the client saves by default.
	used["/dl"] = 88
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	got := append([]string(nil), fake.paused...)
	sort.Strings(got)
	if want := []string{"disk2a", "main1", "nopath"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("paused %v, want %v", got, want)
	}

	// Disk 2 drains while the main drive is still full: only disk 2's is released.
	used["/mnt/disk2/downloads"] = 70
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 || fake.resumed[0] != "disk2a" {
		t.Fatalf("resumed %v, want only the download on the drained disk", fake.resumed)
	}
	h := g.held(ctx)
	sort.Strings(h)
	if len(h) != 2 || h[0] != "main1" || h[1] != "nopath" {
		t.Fatalf("still held %v, want the main drive's two", h)
	}

	// A volume that can't be measured releases nothing.
	delete(used, "/dl")
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumed) != 1 {
		t.Errorf("resumed %v with the main drive unmeasurable", fake.resumed)
	}
	// It drains: everything is released.
	used["/dl"] = 60
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(g.held(ctx)) != 0 || len(fake.resumed) != 3 {
		t.Errorf("held %v, resumed %v — want all released", g.held(ctx), fake.resumed)
	}
}

// One downloads folder (or every library folder using the Downloads folder): the guard
// behaves exactly as it always has, without a per-volume status.
func TestGuardWithOneDownloadsFolderIsUnchanged(t *testing.T) {
	g, fake, _, ctx := guardFixture(t, []Item{{Hash: "a", State: "downloading", ContentPath: "/elsewhere/a"}})
	g.SetDirFunc(func() string { return "/dl" })
	g.SetDirsFunc(func() []string { return []string{"/dl"} })
	usageAt(g, 90)
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.paused) != 1 {
		t.Fatalf("paused %v, want the download held whatever its path", fake.paused)
	}
	if st := g.Status(ctx); st.Volumes != nil || st.Path != "/dl" {
		t.Errorf("status = %+v", st)
	}
}

func TestFolderOf(t *testing.T) {
	dirs := []string{"/dl", "/mnt/disk2/downloads", "/dl/kids"}
	for content, want := range map[string]string{
		"/dl/x":                        "/dl",
		"/dl/kids/x":                   "/dl/kids", // the longest holding folder
		"/mnt/disk2/downloads/a/b.mkv": "/mnt/disk2/downloads",
		"/mnt/disk2/downloads-old/a":   "/dl", // not on a separator boundary
		"/somewhere/else":              "/dl",
		"":                             "/dl",
		"relative/path":                "/dl",
	} {
		if got := folderOf(content, dirs); got != want {
			t.Errorf("folderOf(%q) = %q, want %q", content, got, want)
		}
	}
}
