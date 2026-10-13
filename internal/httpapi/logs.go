package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/applog"
)

// handleLogs returns recent application logs for the in-app Logs viewer.
//
//	GET /api/v1/logs?limit=500&level=info&q=ben10&hide=torznab,indexer+search
func (a *api) handleLogs(w http.ResponseWriter, r *http.Request) {
	if a.deps.Logs == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"entries": []applog.Entry{}})
		return
	}
	limit := 500
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		if v > 5000 {
			v = 5000
		}
		limit = v
	}
	entries := a.deps.Logs.Snapshot(applog.Filter{
		Limit: limit,
		Min:   parseLevel(r.URL.Query().Get("level")),
		Query: r.URL.Query().Get("q"),
		Hide:  r.URL.Query().Get("hide"),
	})
	if entries == nil {
		entries = []applog.Entry{}
	}
	// capacity is how many lines the server keeps in memory, so the page's footer can say
	// what it's a window onto without hard-coding a number that may change.
	a.writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "capacity": a.deps.Logs.Cap()})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logLevelView is the Logs page's debug switch: the level now, the install's own level
// (what it returns to), and when a raised level ends (omitted while at the install's).
type logLevelView struct {
	Level    string     `json:"level"`
	EnvLevel string     `json:"env_level"`
	Until    *time.Time `json:"until,omitempty"`
	MaxMins  int        `json:"max_minutes"`
}

func (a *api) logLevelView() logLevelView {
	st := a.deps.LogLevel.State()
	v := logLevelView{Level: applog.LevelName(st.Level), EnvLevel: applog.LevelName(st.EnvLevel), MaxMins: int(applog.MaxDebugFor / time.Minute)}
	if !st.Until.IsZero() {
		u := st.Until.UTC()
		v.Until = &u
	}
	return v
}

// handleGetLogLevel — GET /api/v1/logs/level (admin).
func (a *api) handleGetLogLevel(w http.ResponseWriter, r *http.Request) {
	if a.deps.LogLevel == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the log level can't be changed from the app")
		return
	}
	a.writeJSON(w, http.StatusOK, a.logLevelView())
}

// handleSetLogLevel — PUT /api/v1/logs/level {level, minutes} (admin). Any level other
// than the install's own is temporary: it reverts after minutes (default 30, at most 240),
// and a restart starts back at the install's level. Sending the install's level, or
// {"level":""}, goes back to normal now.
func (a *api) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	if a.deps.LogLevel == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the log level can't be changed from the app")
		return
	}
	var req struct {
		Level   string `json:"level"`
		Minutes int    `json:"minutes"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Level) == "" {
		a.deps.LogLevel.Reset()
		a.deps.Log.Info("log level back to normal", "level", applog.LevelName(a.deps.LogLevel.State().EnvLevel))
		a.writeJSON(w, http.StatusOK, a.logLevelView())
		return
	}
	lvl, ok := applog.ParseLevel(req.Level)
	if !ok {
		a.writeError(w, http.StatusBadRequest, "level must be debug, info, warn or error")
		return
	}
	maxMins := int(applog.MaxDebugFor / time.Minute)
	if req.Minutes < 0 || req.Minutes > maxMins {
		a.writeError(w, http.StatusBadRequest, fmt.Sprintf("minutes must be between 1 and %d (0 for the default)", maxMins))
		return
	}
	if req.Minutes == 0 {
		req.Minutes = int(applog.DefaultDebugFor / time.Minute)
	}
	// Logged before the change, so the line is kept even when the new level is quieter.
	if lvl == a.deps.LogLevel.State().EnvLevel {
		a.deps.Log.Info("log level back to normal", "level", applog.LevelName(lvl))
	} else {
		a.deps.Log.Info("log level changed for a while", "level", applog.LevelName(lvl), "minutes", req.Minutes)
	}
	a.deps.LogLevel.Set(lvl, time.Duration(req.Minutes)*time.Minute)
	a.writeJSON(w, http.StatusOK, a.logLevelView())
}
