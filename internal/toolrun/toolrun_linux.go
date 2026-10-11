//go:build linux

package toolrun

import (
	"os"
	"os/exec"
	"syscall"
)

// CanPause reports whether Pause and Resume work on this platform.
const CanPause = true

// ioprio_set(2) values (linux/ioprio.h). The standard library has the syscall number but
// not these.
const (
	ioprioWhoPgrp    = 2
	ioprioClassBE    = 2
	ioprioClassIdle  = 3
	ioprioClassShift = 13
)

// ownGroup puts the tool in its own process group, so niceness, the I/O class, a pause and
// a kill all reach anything it spawns too, and makes cancelling kill that whole group.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Signal(0) goes through the process handle, so it reports a leader that has already
		// been waited for — whose id could then belong to someone else — instead of killing
		// a stranger's group.
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			return os.ErrProcessDone
		}
		// SIGKILL also ends a group frozen by Pause: a stopped process can't catch it.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// lower sets a started group's CPU niceness and I/O class. Called after Start because the
// id isn't known before then. Failures aren't fatal: the tool simply runs at the priority
// it inherited. Raising niceness and the idle I/O class need no privileges.
func lower(pgid int, class Class) {
	var ioprio int
	switch class {
	case Background:
		ioprio = ioprioClassBE<<ioprioClassShift | 7
	case Idle:
		ioprio = ioprioClassIdle << ioprioClassShift
	default:
		return
	}
	_ = syscall.Setpriority(syscall.PRIO_PGRP, pgid, Nice)
	_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoPgrp, uintptr(pgid), uintptr(ioprio))
}

func pauseGroup(pgid int) error  { return syscall.Kill(-pgid, syscall.SIGSTOP) }
func resumeGroup(pgid int) error { return syscall.Kill(-pgid, syscall.SIGCONT) }
