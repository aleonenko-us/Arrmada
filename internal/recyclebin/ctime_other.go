//go:build !linux

package recyclebin

import (
	"os"
	"time"
)

// changeTime isn't read here: the mtime is all the age there is.
func changeTime(os.FileInfo) time.Time { return time.Time{} }
