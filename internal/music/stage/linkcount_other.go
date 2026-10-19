//go:build !unix

package stage

import "os"

// linkCount can't be read here; a fresh copy has one name.
func linkCount(os.FileInfo) uint64 { return 1 }
