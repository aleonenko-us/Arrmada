package applog

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LevelSwitch is the log level an admin can raise for a while from the Logs page. The
// install's level (ARRMADA_LOG_LEVEL, "info" in the shipped compose file) is the floor it
// always returns to: debug logs every request and every indexer page, so left on it
// buries the log and fills the disk files within hours. A raised level therefore always
// carries a deadline, and lives only in memory — a restart comes back at the install's
// level, so nothing can leave debug on by accident.
type LevelSwitch struct {
	v   slog.LevelVar
	env slog.Level

	mu    sync.Mutex
	until time.Time   // zero while at the install's level
	timer *time.Timer // the pending revert, nil while at the install's level
	gen   uint64      // bumped on every change, so a revert that lost a race does nothing
}

// Debug durations: the button's default, and the longest an admin may ask for.
const (
	DefaultDebugFor = 30 * time.Minute
	MaxDebugFor     = 240 * time.Minute
)

// NewLevelSwitch starts at the install's level.
func NewLevelSwitch(env slog.Level) *LevelSwitch {
	s := &LevelSwitch{env: env}
	s.v.Set(env)
	return s
}

// Leveler is what the log handlers read on every record (HandlerOptions.Level).
func (s *LevelSwitch) Leveler() slog.Leveler { return &s.v }

// Set raises (or lowers) the level for d, after which it reverts to the install's level
// by itself. d is clamped to (0, MaxDebugFor]; zero or less means DefaultDebugFor. Asking
// for the install's own level is the same as Reset. It returns when the change ends.
func (s *LevelSwitch) Set(l slog.Level, d time.Duration) time.Time {
	if l == s.env {
		s.Reset()
		return time.Time{}
	}
	if d <= 0 {
		d = DefaultDebugFor
	}
	if d > MaxDebugFor {
		d = MaxDebugFor
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.gen++
	gen := s.gen
	s.v.Set(l)
	s.until = time.Now().Add(d)
	s.timer = time.AfterFunc(d, func() { s.revert(gen) })
	return s.until
}

// Reset goes back to the install's level now.
func (s *LevelSwitch) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

// revert is the timer's Reset: it only applies when nothing has changed since the timer
// was set (Stop can't recall a callback that has already started).
func (s *LevelSwitch) revert(gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return
	}
	s.resetLocked()
}

func (s *LevelSwitch) resetLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.gen++
	s.v.Set(s.env)
	s.until = time.Time{}
}

// LevelState is what the Logs page shows: the level now, the install's level, and when a
// raised level ends (zero while at the install's level).
type LevelState struct {
	Level    slog.Level
	EnvLevel slog.Level
	Until    time.Time
}

// State reports the current level and its deadline.
func (s *LevelSwitch) State() LevelState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return LevelState{Level: s.v.Level(), EnvLevel: s.env, Until: s.until}
}

// ParseLevel reads a level name ("debug", "info", "warn"/"warning", "error"). ok is false
// for anything else, so a caller can refuse it rather than guess.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// LevelName is the lower-case name the API and the Logs page use.
func LevelName(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l <= slog.LevelInfo:
		return "info"
	case l <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}
