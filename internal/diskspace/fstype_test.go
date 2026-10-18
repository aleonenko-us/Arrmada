package diskspace

import (
	"runtime"
	"testing"
)

// /proc is always procfs; the rest is the name table.
func TestFSType(t *testing.T) {
	for ft, want := range map[uint64]string{
		TypeTmpfs: "tmpfs", TypeOverlay: "overlay", TypeRamfs: "ramfs", TypeProc: "proc", TypeSysfs: "sysfs",
		TypeCgroup: "cgroup", TypeCgroup2: "cgroup", TypeZFS: "", 0xEF53: "", // ext4
	} {
		if got := NotADisk(ft); got != want {
			t.Errorf("NotADisk(%#x) = %q, want %q", ft, got, want)
		}
	}
	if runtime.GOOS != "linux" {
		if _, ok := FSType(t.TempDir()); ok {
			t.Fatal("off Linux the type must be unknown")
		}
		t.Skip("filesystem types are only read on Linux")
	}
	if ft, ok := FSType("/proc"); !ok || NotADisk(ft) != "proc" {
		t.Errorf("/proc: type %#x ok=%v", ft, ok)
	}
	if _, ok := FSType("/no/such/path"); ok {
		t.Error("a missing path has no type")
	}
}
