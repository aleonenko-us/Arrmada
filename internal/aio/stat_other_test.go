//go:build !linux

package aio

// statOwner is linux-only (supervisor_linux_test.go); the check using it runs as root.
func statOwner(string) ([2]int, bool) { return [2]int{}, false }
