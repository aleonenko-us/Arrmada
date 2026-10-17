//go:build !linux

package convert

// charDevNum has no device numbers to read off Linux; the lookup falls back to the node's
// name (production runs in the Linux container).
func charDevNum(path string) (string, bool) { return "", false }
