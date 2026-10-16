//go:build linux

package aio

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a container log the test can read while the supervisor writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func sh(name, script string, main bool) Service {
	return Service{Name: name, Path: "/bin/sh", Args: []string{"-c", script}, Main: main}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Arrmada exiting ends the container with its code, and the companions are stopped
// (gracefully: their TERM handler runs) rather than left behind.
func TestMainExitStopsCompanions(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "saved")
	out := &syncBuffer{}
	s := &Supervisor{Out: out, Grace: 5 * time.Second, Services: []Service{
		sh("arrmada", "sleep 0.5; echo app says hi; exit 3", true),
		sh("qbittorrent", "trap 'echo saved > "+marker+"; exit 0' TERM; echo ready; while :; do sleep 0.05; done", false),
	}}
	code := s.Run(context.Background())
	if code != 3 {
		t.Errorf("exit code %d, want Arrmada's 3", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the companion wasn't stopped with SIGTERM")
	}
	log := out.String()
	if !strings.Contains(log, "app says hi\n") || strings.Contains(log, "] app says hi") {
		t.Errorf("Arrmada's output should pass through unprefixed:\n%s", log)
	}
	if !strings.Contains(log, "[qbittorrent] ready\n") {
		t.Errorf("companion output not prefixed:\n%s", log)
	}
}

// A stop signal stops every service, Arrmada included, and the container exits with
// Arrmada's code (0 after a clean shutdown).
func TestStopSignal(t *testing.T) {
	dir := t.TempDir()
	out := &syncBuffer{}
	s := &Supervisor{Out: out, Grace: 5 * time.Second, Services: []Service{
		sh("arrmada", "trap 'exit 0' TERM; touch "+filepath.Join(dir, "a")+"; while :; do sleep 0.05; done", true),
		sh("qbittorrent", "trap 'touch "+filepath.Join(dir, "q-stopped")+"; exit 0' TERM; touch "+filepath.Join(dir, "q")+"; while :; do sleep 0.05; done", false),
		sh("flaresolverr", "trap 'exit 0' TERM; touch "+filepath.Join(dir, "f")+"; while :; do sleep 0.05; done", false),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, "all three to start", func() bool {
		for _, f := range []string{"a", "q", "f"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				return false
			}
		}
		return true
	})
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d after a clean stop", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run didn't return after the stop signal")
	}
	if _, err := os.Stat(filepath.Join(dir, "q-stopped")); err != nil {
		t.Error("qBittorrent didn't get SIGTERM")
	}
	if strings.Contains(out.String(), "starting it again") {
		t.Errorf("a service was restarted during the stop:\n%s", out.String())
	}
}

// A service that ignores SIGTERM is killed once the grace period is over, together with
// what it started (its process group).
func TestStopKillsAfterGrace(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.pid")
	out := &syncBuffer{}
	s := &Supervisor{Out: out, Grace: 300 * time.Millisecond, Services: []Service{
		sh("arrmada", "trap 'exit 0' TERM; while :; do sleep 0.05; done", true),
		sh("flaresolverr", "trap '' TERM; (trap '' TERM; while :; do sleep 0.05; done) & echo $! > "+child+"; while :; do sleep 0.05; done", false),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, "the child to start", func() bool { _, err := os.Stat(child); return err == nil })
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run didn't return")
	}
	if !strings.Contains(out.String(), "flaresolverr didn't stop within") {
		t.Errorf("no kill logged:\n%s", out.String())
	}
	raw, _ := os.ReadFile(child)
	pid := strings.TrimSpace(string(raw))
	waitFor(t, "the grandchild to die", func() bool {
		st, err := os.ReadFile("/proc/" + pid + "/stat")
		// Gone, or a zombie waiting for its reaper (tini, in the image).
		return err != nil || strings.Contains(string(st), ") Z ")
	})
}

// A companion that keeps dying is started again, waiting longer each time, and never
// takes Arrmada down with it.
func TestCompanionRestartsWithBackoff(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	out := &syncBuffer{}
	s := &Supervisor{Out: out, Grace: time.Second, MinBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond, Services: []Service{
		sh("arrmada", "trap 'exit 0' TERM; while :; do sleep 0.05; done", true),
		sh("qbittorrent", "echo x >> "+count+"; exit 1", false),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, "four starts", func() bool {
		raw, _ := os.ReadFile(count)
		return strings.Count(string(raw), "x") >= 4
	})
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("exit code %d", code)
	}
	log := out.String()
	for _, want := range []string{"qbittorrent stopped (code 1)", "starting it again in 50ms", "starting it again in 100ms", "starting it again in 200ms"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A companion that can't be started at all (a missing binary) is retried and logged;
// Arrmada keeps running.
func TestCompanionMissingBinary(t *testing.T) {
	out := &syncBuffer{}
	s := &Supervisor{Out: out, Grace: time.Second, MinBackoff: 20 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, Services: []Service{
		sh("arrmada", "sleep 0.3; exit 0", true),
		{Name: "qbittorrent", Path: "/nonexistent/qbittorrent-nox"},
	}}
	if code := s.Run(context.Background()); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if !strings.Contains(out.String(), "couldn't start qbittorrent") {
		t.Errorf("log:\n%s", out.String())
	}
}

func TestPrefixWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &prefixWriter{prefix: "[q] ", out: &buf, mu: &sync.Mutex{}}
	_, _ = w.Write([]byte("one\r\ntw"))
	_, _ = w.Write([]byte("o\nthree"))
	w.flush()
	if got := buf.String(); got != "[q] one\n[q] two\n[q] three\n" {
		t.Errorf("got %q", got)
	}
	buf.Reset()
	_, _ = w.Write(bytes.Repeat([]byte("a"), maxLine+10))
	if strings.Count(buf.String(), "[q] ") != 1 {
		t.Errorf("an over-long line wasn't written out: %d bytes", buf.Len())
	}
}
