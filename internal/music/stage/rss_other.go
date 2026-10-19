//go:build !linux

package stage

import "os"

// maxRSSKB is unknown off Linux (the deployment target): 0.
func maxRSSKB(*os.ProcessState) int64 { return 0 }
