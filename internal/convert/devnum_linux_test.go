//go:build linux

package convert

import "testing"

// /dev/null is character device 1:3 on every Linux; a folder isn't a device at all.
func TestCharDevNum(t *testing.T) {
	if n, ok := charDevNum("/dev/null"); !ok || n != "1:3" {
		t.Errorf("charDevNum(/dev/null) = %q, %v; want 1:3", n, ok)
	}
	if _, ok := charDevNum("/"); ok {
		t.Error("a folder isn't a character device")
	}
	if _, ok := charDevNum("/dev/dri/does-not-exist"); ok {
		t.Error("a missing node has no numbers")
	}
}
