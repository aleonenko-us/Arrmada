//go:build linux

package aio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Supervisor runs the all-in-one container's processes. It is deliberately small: start
// each service, prefix a companion's output with its name in the container log, restart
// a companion that stops (waiting longer each time it fails quickly), and on a stop
// signal ask every process to finish, then kill what's left once Grace has passed.
// Zombie reaping is left to tini, which runs as PID 1 in front of it.
type Supervisor struct {
	Services []Service
	Out      io.Writer // the container log

	Grace       time.Duration // how long a stop waits before killing (default 25s)
	MinBackoff  time.Duration // first restart delay (default 1s)
	MaxBackoff  time.Duration // longest restart delay (default 1m)
	StableAfter time.Duration // a run this long resets the delay (default 2m)

	outMu   sync.Mutex
	mu      sync.Mutex
	running map[string]*exec.Cmd
	stopped bool
}

// Run starts every service and returns the exit code the container should end with:
// Arrmada's. It returns when Arrmada exits, or when ctx is done (a stop signal), after
// every service has been stopped.
func (s *Supervisor) Run(ctx context.Context) int {
	s.defaults()
	s.running = map[string]*exec.Cmd{}
	mainDone := make(chan int, 1)
	var wg sync.WaitGroup
	loopCtx, stopLoops := context.WithCancel(context.Background())
	defer stopLoops()
	for _, svc := range s.Services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.loop(loopCtx, svc, mainDone)
		}()
	}

	code := 0
	select {
	case code = <-mainDone:
		s.logf("Arrmada exited (code %d); stopping the bundled services", code)
	case <-ctx.Done():
		s.logf("stopping")
	}
	stopLoops()
	s.stopAll()
	wg.Wait()
	select {
	case c := <-mainDone: // Arrmada stopped as part of the shutdown
		code = c
	default:
	}
	return code
}

func (s *Supervisor) defaults() {
	if s.Out == nil {
		s.Out = os.Stdout
	}
	if s.Grace <= 0 {
		s.Grace = 25 * time.Second
	}
	if s.MinBackoff <= 0 {
		s.MinBackoff = time.Second
	}
	if s.MaxBackoff <= 0 {
		s.MaxBackoff = time.Minute
	}
	if s.StableAfter <= 0 {
		s.StableAfter = 2 * time.Minute
	}
}

// loop runs one service until ctx is done. A main service runs once; a companion is
// started again whenever it stops.
func (s *Supervisor) loop(ctx context.Context, svc Service, mainDone chan<- int) {
	delay := s.MinBackoff
	for {
		started := time.Now()
		code, err := s.runOnce(svc)
		if svc.Main {
			if err != nil {
				s.logf("couldn't start %s: %v", svc.Name, err)
				code = 1
			}
			mainDone <- code
			return
		}
		if ctx.Err() != nil {
			return
		}
		ran := time.Since(started)
		if ran >= s.StableAfter {
			delay = s.MinBackoff
		}
		if err != nil {
			s.logf("couldn't start %s: %v; trying again in %s", svc.Name, err, delay)
		} else {
			s.logf("%s stopped (code %d) after %s; starting it again in %s", svc.Name, code, ran.Round(time.Second), delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, s.MaxBackoff)
	}
}

// runOnce starts the service and waits for it. code is its exit code (128+n when a
// signal ended it); err means it couldn't be started at all.
func (s *Supervisor) runOnce(svc Service) (code int, err error) {
	cmd := exec.Command(svc.Path, svc.Args...)
	cmd.Dir = svc.Dir
	cmd.Env = svc.Env
	// Its own process group, so a stop reaches what it started too (FlareSolverr's
	// Chromium and Xvfb).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if svc.UID != nil && svc.GID != nil {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(*svc.UID), Gid: uint32(*svc.GID), Groups: []uint32{}}
	}
	var pw *prefixWriter
	if svc.Main {
		// The container's own stdout is handed over as it is; anything else (a test's
		// buffer) is written under the same lock as the rest.
		var out io.Writer = &lockedWriter{w: s.Out, mu: &s.outMu}
		if f, ok := s.Out.(*os.File); ok {
			out = f
		}
		cmd.Stdout, cmd.Stderr = out, out
	} else {
		pw = &prefixWriter{prefix: "[" + svc.Name + "] ", out: s.Out, mu: &s.outMu}
		cmd.Stdout, cmd.Stderr = pw, pw
		// A child that outlives the service (a browser) can hold the output pipe open;
		// don't wait on it for ever once the service itself has exited.
		cmd.WaitDelay = 2 * time.Second
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return 0, errStopping
	}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	s.running[svc.Name] = cmd
	s.mu.Unlock()

	err = cmd.Wait()
	if pw != nil {
		pw.flush()
	}
	s.mu.Lock()
	delete(s.running, svc.Name)
	s.mu.Unlock()
	return exitCode(cmd, err), nil
}

var errStopping = errors.New("stopping")

// stopAll asks every running service's process group to stop (SIGTERM, which qBittorrent
// answers by saving its torrents' state), waits up to Grace, then kills what's left.
func (s *Supervisor) stopAll() {
	s.mu.Lock()
	s.stopped = true
	pgids := make([]int, 0, len(s.running))
	for name, cmd := range s.running {
		pgids = append(pgids, cmd.Process.Pid)
		s.logf("stopping %s", name)
	}
	s.mu.Unlock()
	for _, pg := range pgids {
		_ = syscall.Kill(-pg, syscall.SIGTERM)
	}
	deadline := time.Now().Add(s.Grace)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.running)
		s.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.mu.Lock()
	for name, cmd := range s.running {
		s.logf("%s didn't stop within %s; killing it", name, s.Grace)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	s.mu.Unlock()
}

func (s *Supervisor) logf(format string, args ...any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintf(s.Out, "[aio] "+format+"\n", args...)
}

// exitCode is a finished command's exit code, 128+signal when a signal ended it (as a
// shell reports it).
func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		if err != nil {
			return 1
		}
		return 0
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return cmd.ProcessState.ExitCode()
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// prefixWriter writes each line it's given to out with the service's name in front, so
// the container log says which process said what. Lines from different services never
// interleave mid-line.
type prefixWriter struct {
	prefix string
	out    io.Writer
	mu     *sync.Mutex
	buf    []byte
}

// maxLine bounds a line held back waiting for its end; a longer one is written in parts.
const maxLine = 16 << 10

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) >= maxLine {
				w.emit(w.buf)
				w.buf = w.buf[:0]
			}
			return len(p), nil
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
}

func (w *prefixWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = nil
	}
}

func (w *prefixWriter) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintf(w.out, "%s%s\n", w.prefix, line)
}
