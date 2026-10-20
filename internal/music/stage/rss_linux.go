//go:build linux

package stage

import (
	"os"
	"syscall"
)

// maxRSSKB is the most memory a finished process used, in kilobytes (Linux reports it so).
func maxRSSKB(ps *os.ProcessState) int64 {
	if ps == nil {
		return 0
	}
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
		return int64(ru.Maxrss)
	}
	return 0
}
