//go:build !unix

package music

import "os"

// FileIdentity is "" where the platform gives no inode: a renamed file is then found
// again by the matcher, as a new file, instead of being followed.
func FileIdentity(os.FileInfo) string { return "" }
