// Package diskspace reports space on the filesystem holding a path. The
// real implementation is platform-specific (build-tagged); on unsupported
// platforms it reports "unknown" so callers can skip disk-based decisions rather
// than guess.
package diskspace

import (
	"os"
	"path/filepath"
)

// Usage describes a filesystem's capacity. Free is what an unprivileged user can
// actually write, which on a reserved-block filesystem is less than Total-Used.
type Usage struct {
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	// UsedPct is 0-100, computed against the space visible to us (used+free)
	// rather than Total, so the bar matches the numbers printed beside it.
	UsedPct float64 `json:"used_pct"`
}

// Of returns the usage of the filesystem containing path, and whether it could be
// measured (false on platforms without support, or for a path that doesn't exist).
func Of(path string) (Usage, bool) {
	total, free, ok := stat(path)
	if !ok || total == 0 {
		return Usage{}, false
	}
	u := Usage{TotalBytes: total, FreeBytes: free}
	if free <= total {
		u.UsedBytes = total - free
	}
	if visible := u.UsedBytes + free; visible > 0 {
		u.UsedPct = float64(u.UsedBytes) / float64(visible) * 100
	}
	return u, true
}

// Device returns the id of the filesystem holding path (st_dev), and whether it could be
// read. Two paths with the same id are on one filesystem: unlike comparing free space,
// that answer doesn't wobble while a download is writing between the two readings.
func Device(path string) (uint64, bool) {
	return device(path)
}

// FSType returns the type of the filesystem holding path (statfs f_type, a magic
// number), and whether it could be read (false off Linux, or for a path that doesn't
// exist).
func FSType(path string) (uint64, bool) {
	return fsType(path)
}

// Filesystem types, as statfs reports them (linux/magic.h).
const (
	TypeOverlay = 0x794c7630
	TypeTmpfs   = 0x01021994 // devtmpfs reports this too
	TypeRamfs   = 0x858458f6
	TypeProc    = 0x9fa0
	TypeSysfs   = 0x62656572
	TypeCgroup  = 0x0027e0eb
	TypeCgroup2 = 0x63677270
	TypeZFS     = 0x2fc12fc1
)

// NotADisk names a filesystem type that lives in memory or belongs to the container
// rather than a disk ("tmpfs", "overlay"…), or returns "" for anything else. A folder on
// one of these can't be the drive downloads fill: on Unraid, a bind mount whose host path
// doesn't exist (a pool name typed wrong) is created by Docker in the RAM root, and reads
// as one of these.
func NotADisk(t uint64) string {
	switch t {
	case TypeOverlay:
		return "overlay"
	case TypeTmpfs:
		return "tmpfs"
	case TypeRamfs:
		return "ramfs"
	case TypeProc:
		return "proc"
	case TypeSysfs:
		return "sysfs"
	case TypeCgroup, TypeCgroup2:
		return "cgroup"
	}
	return ""
}

// FreeGB returns the free space in GB on the filesystem containing path, and
// whether it could be measured (false on platforms without support).
func FreeGB(path string) (float64, bool) {
	u, ok := Of(path)
	if !ok {
		return 0, false
	}
	return float64(u.FreeBytes) / (1024 * 1024 * 1024), true
}

// SameDevice reports whether a and b are on one filesystem by their filesystem ids, and
// whether that could be told at all (ok is false off Linux, or when a path has no
// existing parent). A folder that doesn't exist yet is judged at its nearest existing
// parent, which is where it would be created.
//
// On Unraid every /mnt/user share is one shfs filesystem, so two shares can report the
// same id while their files sit on different disks: "same" there is the best this can
// say, and callers treat a "different" answer as advisory.
func SameDevice(a, b string) (same, ok bool) {
	pa, okA := existingParent(a)
	pb, okB := existingParent(b)
	if !okA || !okB {
		return false, false
	}
	da, okA := device(pa)
	db, okB := device(pb)
	if !okA || !okB {
		return false, false
	}
	return da == db, true
}

// existingParent returns path, or its nearest parent that exists.
func existingParent(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	p := filepath.Clean(path)
	for {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", false
		}
		p = parent
	}
}
