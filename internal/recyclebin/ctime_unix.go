//go:build linux

package recyclebin

import (
	"os"
	"syscall"
	"time"
)

// changeTime is the inode change time of a file with other names (zero for anything
// else): binning such a file leaves its mtime alone (it's shared with a torrent client's
// copy), but the move into the bin sets its change time.
func changeTime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)) //nolint:unconvert // int32 on some platforms
	}
	return time.Time{}
}
