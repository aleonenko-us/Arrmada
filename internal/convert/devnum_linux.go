//go:build linux

package convert

import (
	"fmt"
	"syscall"
)

// charDevNum is a character device's "major:minor", the name /sys/dev/char knows it by;
// false when path isn't a character device.
func charDevNum(path string) (string, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return "", false
	}
	dev := uint64(st.Rdev) // its type differs between architectures
	major := (dev>>8)&0xfff | (dev>>32)&^uint64(0xfff)
	minor := dev&0xff | (dev>>12)&^uint64(0xff)
	return fmt.Sprintf("%d:%d", major, minor), true
}
