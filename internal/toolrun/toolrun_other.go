//go:build !linux

package toolrun

import "os/exec"

// CanPause reports whether Pause and Resume work on this platform. Off Linux they don't:
// the deployment target is a Linux container, and dev machines don't need tools paused.
const CanPause = false

// ownGroup is a no-op off Linux: cancelling kills the started process only (exec's
// default), not anything it spawned.
func ownGroup(cmd *exec.Cmd) {}

// lower is a no-op off Linux; tools run at the priority they inherit.
func lower(pgid int, class Class) {}

// pauseGroup and resumeGroup are no-ops off Linux; the tool simply keeps running.
func pauseGroup(pgid int) error  { return nil }
func resumeGroup(pgid int) error { return nil }
