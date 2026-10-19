//go:build unix

package library

import (
	"os"
	"syscall"
)

// linkCount is how many names a file has (1 when it can't be told).
func linkCount(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
