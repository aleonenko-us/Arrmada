// Package applog captures Arrmada's structured logs into an in-memory ring buffer so
// they can be viewed inside the app (the Logs page), in addition to stdout/docker logs.
package applog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Entry is one captured log record, flattened for the API.
type Entry struct {
	TimeMS  int64  `json:"time_ms"`
	Level   string `json:"level"` // DEBUG | INFO | WARN | ERROR
	Message string `json:"msg"`
	Attrs   string `json:"attrs,omitempty"` // "key=value key2=value2"
	// Seq numbers the entries of one run in the order they were captured: the Logs page's
	// Live view asks only for entries after the last one it has. Lines read back from disk
	// at startup are numbered afresh, and the number isn't written to disk.
	Seq uint64 `json:"seq,omitempty"`
	// Module is the part of the app that wrote the line ("convert", "indexer", …), from
	// the logger it was handed in main; empty for the app's own core lines.
	Module string `json:"module,omitempty"`
}

// Modules are the module tags main hands out, for the Logs page's filter. Keep in step
// with the log.With("module", …) calls in cmd/arrmada.
var Modules = []string{
	"api", "audioserver", "automation", "books", "convert", "download", "filecheck", "import",
	"indexer", "insights", "jellyfin", "emby", "movies", "music", "notify", "requests", "scheduler",
	"series", "subtitles",
}

// moduleKey is the logger attribute that tags a module.
const moduleKey = "module"

// Ring is a fixed-capacity, concurrency-safe buffer of the most recent log entries.
// With Persist attached it also tees each entry to disk — see persist.go.
type Ring struct {
	mu   sync.RWMutex
	buf  []Entry
	next int
	full bool
	max  int
	seq  uint64  // the last Seq handed out
	sink *writer // nil until Persist is called
	path string  // the active log file, once Persist is called
}

// NewRing builds a ring holding up to max entries (min 100).
func NewRing(max int) *Ring {
	if max < 100 {
		max = 100
	}
	return &Ring{buf: make([]Entry, max), max: max}
}

// Cap is how many entries the ring holds before the oldest drop off.
func (r *Ring) Cap() int { return r.max }

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	r.buf[r.next] = e
	r.next = (r.next + 1) % r.max
	if r.next == 0 {
		r.full = true
	}
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.offer(e) // never blocks; see writer.offer
	}
}

// Filter selects which captured entries a Snapshot returns.
type Filter struct {
	Limit int        // newest N after filtering; 0 = no cap
	Min   slog.Level // entries below this level are dropped
	Query string     // keep only entries whose message/attrs contain this
	// Hide drops entries whose message/attrs contain any of these comma-separated
	// terms. The routine chatter — per-page indexer tracing especially — can outnumber
	// everything else by an order of magnitude, and a keep-only filter can't help when
	// you don't yet know what you're looking for. Applied after Query, so a term can
	// narrow first and then subtract the noise from what's left.
	Hide string
	// AfterSeq keeps only entries captured after this one (0 = from the start). It's the
	// Live view's cursor: each poll carries only what's new, not the whole window again.
	AfterSeq uint64
	// Module keeps only one module's lines ("" = every module).
	Module string
}

// Snapshot returns the most-recent matching entries, oldest first.
func (r *Ring) Snapshot(f Filter) []Entry {
	return r.Read(f).Entries
}

// Page is one answer from Read.
type Page struct {
	Entries []Entry
	// LastSeq is the newest entry the ring held when it was read: the cursor to send as
	// AfterSeq next time, whether or not that entry matched the filter.
	LastSeq uint64
	// Reset means AfterSeq was ahead of the ring — the cursor came from before a restart —
	// so it was ignored and Entries is a fresh window rather than an increment.
	Reset bool
}

// Read returns the most-recent matching entries, oldest first, with the cursor for the
// next incremental read.
func (r *Ring) Read(f Filter) Page {
	r.mu.RLock()
	last := r.seq
	reset := f.AfterSeq > last
	after := f.AfterSeq
	if reset {
		after = 0
	}
	// Reconstruct chronological order. With a cursor, walk back from the newest entry only
	// as far as the cursor, so a poll costs what's new rather than the whole ring.
	var ordered []Entry
	if after > 0 {
		n := r.next // entries held
		if r.full {
			n = r.max
		}
		for i := 0; i < n; i++ {
			e := r.buf[(r.next-1-i+r.max)%r.max]
			if e.Seq <= after {
				break
			}
			ordered = append(ordered, e)
		}
		for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
	} else {
		if r.full {
			ordered = append(ordered, r.buf[r.next:]...)
		}
		ordered = append(ordered, r.buf[:r.next]...)
	}
	r.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(f.Query))
	hide := hideTerms(f.Hide)
	out := make([]Entry, 0, len(ordered))
	for _, e := range ordered {
		if levelValue(e.Level) < f.Min {
			continue
		}
		if f.Module != "" && e.Module != f.Module {
			continue
		}
		if q != "" && !matches(e, q) {
			continue
		}
		if hidden(e, hide) {
			continue
		}
		out = append(out, e)
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return Page{Entries: out, LastSeq: last, Reset: reset}
}

func matches(e Entry, lowerTerm string) bool {
	return strings.Contains(strings.ToLower(e.Message), lowerTerm) ||
		strings.Contains(strings.ToLower(e.Attrs), lowerTerm)
}

// hideTerms splits a comma-separated exclude list into lowercase terms, dropping blanks
// so a trailing comma doesn't turn into a term that matches everything.
func hideTerms(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if t := strings.ToLower(strings.TrimSpace(part)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func hidden(e Entry, terms []string) bool {
	for _, t := range terms {
		if matches(e, t) {
			return true
		}
	}
	return false
}

func levelValue(s string) slog.Level {
	switch s {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Handler is an slog.Handler that tees each record into the ring while delegating to a
// base handler (stdout). Install it via NewHandler around a text/JSON handler.
type Handler struct {
	base  slog.Handler
	ring  *Ring
	attrs []slog.Attr
	group string
	// module is the logger's "module" tag (log.With("module", "convert")); it goes in
	// Entry.Module rather than the attrs, so the Logs page can filter on it.
	module string
}

// NewHandler wraps base so every emitted record is also appended to ring.
func NewHandler(base slog.Handler, ring *Ring) *Handler {
	return &Handler{base: base, ring: ring}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.base.Enabled(ctx, l) }

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	for _, a := range h.attrs {
		appendAttr(&sb, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&sb, h.group, a)
		return true
	})
	h.ring.add(Entry{TimeMS: r.Time.UnixMilli(), Level: r.Level.String(), Message: scrubQuery(r.Message), Attrs: strings.TrimSpace(sb.String()), Module: h.module})
	return h.base.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.base = h.base.WithAttrs(attrs) // stdout keeps module=… on every line
	cp.attrs = append([]slog.Attr{}, h.attrs...)
	for _, a := range attrs {
		if a.Key == moduleKey && h.group == "" {
			cp.module = a.Value.String()
			continue
		}
		cp.attrs = append(cp.attrs, a)
	}
	return &cp
}

func (h *Handler) WithGroup(name string) slog.Handler {
	cp := *h
	cp.base = h.base.WithGroup(name)
	if name != "" {
		if cp.group == "" {
			cp.group = name
		} else {
			cp.group = cp.group + "." + name
		}
	}
	return &cp
}

func appendAttr(sb *strings.Builder, group string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	if redactKey(key) {
		fmt.Fprintf(sb, "%s=%s ", key, redacted)
		return
	}
	fmt.Fprintf(sb, "%s=%v ", key, scrubQuery(fmt.Sprintf("%v", a.Value.Any())))
}
