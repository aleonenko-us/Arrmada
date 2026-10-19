//go:build unix

package automation

import (
	"os"
	"syscall"
)

// nlink is how many names a file has.
func nlink(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
