//go:build unix

package music

import (
	"os"
	"strconv"
	"syscall"
)

// FileIdentity is a file's device and inode ("dev:ino"), which a rename keeps: the library
// scan follows a renamed album folder by it instead of reading the files as gone and new.
// "" when the platform doesn't say.
func FileIdentity(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(uint64(st.Ino), 10)
	}
	return ""
}
