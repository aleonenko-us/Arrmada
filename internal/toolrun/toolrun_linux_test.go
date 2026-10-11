//go:build linux

package toolrun

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// procStat reads a process's state letter and niceness from /proc/<pid>/stat.
func procStat(t *testing.T, pid int) (state string, nice int, ok bool) {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	s := string(b)
	// The command name is in parentheses and may hold spaces; fields count from after it.
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(f) < 17 {
		t.Fatalf("short stat line: %q", s)
	}
	n, err := strconv.Atoi(f[16]) // field 19, nice
	if err != nil {
		t.Fatal(err)
	}
	return f[0], n, true
}

// ioprio returns a process's I/O scheduling class and level.
func ioprio(t *testing.T, pid int) (class, level int) {
	t.Helper()
	const whoProcess = 1
	r, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_GET, whoProcess, uintptr(pid), 0)
	if errno != 0 {
		t.Fatalf("ioprio_get: %v", errno)
	}
	return int(r) >> ioprioClassShift, int(r) & (1<<ioprioClassShift - 1)
}

func waitState(t *testing.T, pid int, want func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _, ok := procStat(t, pid)
		if !ok {
			return "" // gone
		}
		if want(st) || time.Now().After(deadline) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestClassesLowerCPUAndIO(t *testing.T) {
	for _, tc := range []struct {
		class            Class
		ioClass, ioLevel int
	}{
		{Idle, ioprioClassIdle, 0},
		{Background, ioprioClassBE, 7},
	} {
		t.Run(tc.class.String(), func(t *testing.T) {
			cmd := Command(context.Background(), tc.class, "sleep", "2")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			pid := cmd.Process.Pid
			if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
				t.Fatalf("process group %d (%v), want its own (%d)", pgid, err, pid)
			}
			if _, nice, _ := procStat(t, pid); nice != Nice {
				t.Errorf("nice %d, want %d", nice, Nice)
			}
			if c, l := ioprio(t, pid); c != tc.ioClass || l != tc.ioLevel {
				t.Errorf("I/O class %d level %d, want %d level %d", c, l, tc.ioClass, tc.ioLevel)
			}
		})
	}
}

func TestNormalKeepsPriority(t *testing.T) {
	cmd := Command(context.Background(), Normal, "sleep", "2")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_, mine, _ := procStat(t, os.Getpid())
	if _, nice, _ := procStat(t, cmd.Process.Pid); nice != mine {
		t.Errorf("nice %d, want the parent's %d", nice, mine)
	}
}

// Cancelling kills the whole group, including what the tool started itself.
func TestCancelKillsTheWholeGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, Idle, "sh", "-c", "sleep 60 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("a cancelled tool reported success")
	}
	// Gone, or a zombie waiting for its new parent to reap it — either way not running.
	st := waitState(t, child, func(s string) bool { return s == "Z" })
	if st != "" && st != "Z" {
		t.Fatalf("the tool's own child is still %q after cancel", st)
	}
}

// A paused tool is frozen (state T) and continues on Resume; cancelling a frozen tool
// still kills it.
func TestPauseResumeAndCancelWhilePaused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started, exited int
	cmd := Command(ctx, Idle, "sleep", "30")
	cmd.OnStart = func(pgid int) { started = pgid }
	cmd.OnExit = func(pgid int) { exited = pgid }
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if started != pid {
		t.Fatalf("OnStart got %d, want %d", started, pid)
	}
	if err := Pause(pid); err != nil {
		t.Fatal(err)
	}
	if st := waitState(t, pid, func(s string) bool { return s == "T" }); st != "T" {
		t.Fatalf("state %q after Pause, want T", st)
	}
	if err := Resume(pid); err != nil {
		t.Fatal(err)
	}
	if st := waitState(t, pid, func(s string) bool { return s != "T" }); st == "T" {
		t.Fatal("still stopped after Resume")
	}
	_ = Pause(pid)
	waitState(t, pid, func(s string) bool { return s == "T" })
	cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee interface{ ExitCode() int }
		if err == nil || (!errors.As(err, &ee) && !errors.Is(err, context.Canceled)) {
			t.Fatalf("cancelled while paused: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a paused tool survived its cancel")
	}
	if exited != pid {
		t.Fatalf("OnExit got %d, want %d", exited, pid)
	}
}

func TestOutputHelpers(t *testing.T) {
	ctx := context.Background()
	out, err := Command(ctx, Normal, "sh", "-c", "echo out; echo err >&2").Output()
	if err != nil || strings.TrimSpace(string(out)) != "out" {
		t.Fatalf("Output = %q, %v", out, err)
	}
	both, err := Command(ctx, Background, "sh", "-c", "echo out; echo err >&2").CombinedOutput()
	if err != nil || !strings.Contains(string(both), "out") || !strings.Contains(string(both), "err") {
		t.Fatalf("CombinedOutput = %q, %v", both, err)
	}
	_, err = Command(ctx, Normal, "sh", "-c", "echo bad >&2; exit 3").Output()
	var ee interface{ ExitCode() int }
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("exit status lost: %v", err)
	}
}
