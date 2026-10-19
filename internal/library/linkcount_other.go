//go:build !unix

package library

import "os"

// linkCount can't be read here; treat every file as having one name.
func linkCount(os.FileInfo) uint64 { return 1 }
