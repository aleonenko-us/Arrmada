package httpapi

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/applog"
)

// handleLogs returns recent application logs for the in-app Logs viewer.
//
//	GET /api/v1/logs?limit=500&level=info&q=ben10&hide=torznab,indexer+search&module=convert&after_seq=1234
//
// after_seq makes it incremental: only lines captured after that one come back, and
// last_seq is the cursor for the next call. reset=true says the cursor was from before a
// restart, so entries is a fresh window and the page should start over.
func (a *api) handleLogs(w http.ResponseWriter, r *http.Request) {
	if a.deps.Logs == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"entries": []applog.Entry{}, "modules": applog.Modules})
		return
	}
	q := r.URL.Query()
	limit := 500
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		if v > 5000 {
			v = 5000
		}
		limit = v
	}
	var after uint64
	if v := q.Get("after_seq"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, "after_seq must be a line number")
			return
		}
		after = n
	}
	page := a.deps.Logs.Read(applog.Filter{
		Limit:    limit,
		Min:      parseLevel(q.Get("level")),
		Query:    q.Get("q"),
		Hide:     q.Get("hide"),
		Module:   strings.TrimSpace(q.Get("module")),
		AfterSeq: after,
	})
	entries := page.Entries
	if entries == nil {
		entries = []applog.Entry{}
	}
	// capacity is how many lines the server keeps in memory, so the page's footer can say
	// what it's a window onto without hard-coding a number that may change.
	// run names this run of the server: line numbers restart with it, and the lines read
	// back from disk at startup can number past an old cursor, so a page that sees run
	// change starts over rather than appending lines it already has.
	a.writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "last_seq": page.LastSeq, "reset": page.Reset, "run": a.start.UnixMilli(),
		"modules": applog.Modules, "capacity": a.deps.Logs.Cap(),
	})
}

// handleLogsDownload — GET /api/v1/logs/download (admin). The whole log history on disk —
// the active file and its rotated predecessors, oldest first — as one zip, written
// straight to the response. It's the admin's own copy, as the Logs page shows it (secrets
// already withheld at capture); the support bundle is the one meant for sharing.
func (a *api) handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	if a.deps.Logs == nil {
		a.writeError(w, http.StatusServiceUnavailable, "logging isn't captured in this build")
		return
	}
	a.deps.Logs.Flush(2 * time.Second)
	files := a.deps.Logs.Files()

	stamp := time.Now().UTC().Format("20060102-150405")
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="arrmada-logs-`+stamp+`.zip"`)
	h.Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	var err error
	if len(files) == 0 {
		// Not persisted (the log folder couldn't be opened): the memory window is all there is.
		err = writeZipEntries(zw, "arrmada.log.jsonl", a.deps.Logs.Snapshot(applog.Filter{Min: slog.LevelDebug}), nil)
	}
	for _, name := range files {
		if err != nil {
			break
		}
		err = copyFileToZip(zw, name, filepath.Base(name))
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		// Headers are gone already; the browser sees a cut-off download.
		a.deps.Log.Warn("log download didn't finish", "err", err)
	}
}

// copyFileToZip adds one file under name, copying only the bytes it held when opened: the
// active log keeps growing while it's read.
func copyFileToZip(zw *zip.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // rotated away between the listing and now
		}
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: st.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(zf, io.LimitReader(f, st.Size()))
	return err
}

// writeZipEntries adds entries as one JSON Lines file, passing each through scrub first
// (nil = as they are; scrub returning false drops the line).
func writeZipEntries(zw *zip.Writer, name string, entries []applog.Entry, scrub func(applog.Entry) (applog.Entry, bool)) error {
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(zf, 64<<10)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for _, e := range entries {
		e.Seq = 0
		if scrub != nil {
			var keep bool
			if e, keep = scrub(e); !keep {
				continue
			}
		}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return bw.Flush()
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
