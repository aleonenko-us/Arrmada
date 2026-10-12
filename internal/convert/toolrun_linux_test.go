//go:build linux

package convert

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// pidStat reads a process's state letter and niceness from /proc/<pid>/stat ("" when gone).
func pidStat(t *testing.T, pid int) (state string, nice int) {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	nice, _ = strconv.Atoi(f[16])
	return f[0], nice
}

func waitPidState(t *testing.T, pid int, want func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := pidStat(t, pid)
		if st == "" || want(st) || time.Now().After(deadline) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A side step started while the job is paused is frozen straight away, runs at nice 19 in
// the idle I/O class, continues when the pause lifts, and is forgotten once it ends.
func TestSideStepIsNicedIdleAndPausable(t *testing.T) {
	s := newTestService(t)
	var watching atomic.Bool
	s.SetWatching(watching.Load, func() bool { return true })
	job := &Job{ID: 1, Title: "Test", State: StateVerifying, Requested: true}
	s.mu.Lock()
	s.jobs = append(s.jobs, job)
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(withJob(context.Background(), job))
	defer cancel()
	watching.Store(true)
	s.applyPauses(ctx)
	if !s.isPaused(job) {
		t.Fatal("watching Plex should pause the job")
	}

	cmd := s.tool(ctx, nil, toolrun.Idle, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if st := waitPidState(t, pid, func(s string) bool { return s == "T" }); st != "T" {
		t.Fatalf("started while paused: state %q, want T (stopped)", st)
	}
	if _, nice := pidStat(t, pid); nice != toolrun.Nice {
		t.Errorf("nice %d, want %d", nice, toolrun.Nice)
	}
	const whoProcess, classShift, classIdle = 1, 13, 3
	r, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_GET, whoProcess, uintptr(pid), 0)
	if errno != 0 || int(r)>>classShift != classIdle {
		t.Errorf("I/O priority %#x (%v), want the idle class", r, errno)
	}

	watching.Store(false)
	s.applyPauses(ctx)
	if st := waitPidState(t, pid, func(s string) bool { return s != "T" }); st == "T" {
		t.Fatal("still stopped after the pause lifted")
	}

	cancel()
	_ = cmd.Wait()
	s.mu.Lock()
	left := len(job.procs)
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d process groups still registered after the step ended", left)
	}
}
