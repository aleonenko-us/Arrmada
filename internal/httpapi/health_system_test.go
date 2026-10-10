package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/flaresolverr"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// The panel answers from the registry's cache: a plain GET runs nothing, even when a
// check would hang; ?refresh=1 re-runs the checks, but not twice inside refreshGap.
func TestHandlerServesCachedResults(t *testing.T) {
	reg := health.NewRegistry(nil, nil)
	var runs atomic.Int32
	reg.Register(health.Check{Key: "c", Name: "C", Run: func(context.Context) []health.Finding {
		runs.Add(1)
		return []health.Finding{{Key: "c.bad", Level: health.LevelError, Message: "broken", Fix: health.FixIndexers}}
	}})
	a := &api{deps: Deps{Health: reg}, start: time.Now()}

	get := func(target string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		start := time.Now()
		a.handleSystemHealth(w, httptest.NewRequest(http.MethodGet, target, nil))
		if target == "/" && time.Since(start) > 100*time.Millisecond {
			t.Errorf("a cached GET took %v", time.Since(start))
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding %q: %v", w.Body.String(), err)
		}
		return got
	}

	if got := get("/"); runs.Load() != 0 || got["status"] != "ok" {
		t.Fatalf("plain GET ran a check (runs=%d) or reported %v", runs.Load(), got["status"])
	}
	got := get("/?refresh=1")
	if runs.Load() != 1 || got["status"] != "error" {
		t.Fatalf("refresh: runs=%d status=%v", runs.Load(), got["status"])
	}
	ws, _ := got["warnings"].([]any)
	if len(ws) != 1 {
		t.Fatalf("warnings: %v", got["warnings"])
	}
	w := ws[0].(map[string]any)
	if w["key"] != "c.bad" || w["link"] != "/indexers" || w["link_label"] != "Add an indexer" || w["since"] == nil {
		t.Errorf("warning fields: %v", w)
	}
	if cs, _ := got["checks"].([]any); len(cs) != 1 {
		t.Errorf("checks: %v", got["checks"])
	}
	get("/?refresh=1")
	get("/")
	if runs.Load() != 1 {
		t.Errorf("a second refresh inside the gap re-ran the checks (runs=%d)", runs.Load())
	}
}

// INT-14: a paused tracker and a dead FlareSolverr (with TorrentLeech switched on) show up
// as health warnings that name them and link to their cards on Settings → Connections,
// read from the status tracker and FlareSolverr's cached status — no tracker is asked.
func TestHealthWarnsAboutFailingIntegrations(t *testing.T) {
	reg := health.NewRegistry(nil, nil)
	var ix *indexer.Service
	var tracker *connstatus.Tracker
	newRouteServer(t, func(d *Deps) {
		tracker = connstatus.New(d.Store.DB(), d.Log)
		// Nothing listens on port 1, so FlareSolverr's ping fails at once.
		d.FlareSolverr = flaresolverr.New("http://127.0.0.1:1")
		ix = indexer.NewService(d.Store.DB(), d.Log, d.FlareSolverr)
		ix.SetStatus(tracker)
		d.Indexers = ix
		d.Health = reg
	})
	ctx := context.Background()
	tl, err := ix.Create(ctx, indexer.Indexer{Name: "TorrentLeech", Kind: indexer.KindTorrentLeech, Username: "u", Password: "p", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Create(ctx, indexer.Indexer{Name: "Prowlarr", Kind: indexer.KindTorznab, URL: "http://p/1/api", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tracker.Record(connstatus.KindIndexer, strconv.FormatInt(tl.ID, 10), connstatus.Outcome{Err: errors.New("login failed"), Backoff: true})
	}
	reg.RunNow(ctx, "indexers.status")
	reg.RunNow(ctx, "flaresolverr")

	byKey := map[string]health.Warning{}
	for _, w := range reg.Warnings() {
		byKey[w.Key] = w
	}
	w, ok := byKey[fmt.Sprintf("indexers.paused.%d", tl.ID)]
	if !ok || !strings.Contains(w.Message, "TorrentLeech") || !strings.Contains(w.Message, "login failed") ||
		w.Link != fmt.Sprintf("/settings/connections#indexer-%d", tl.ID) {
		t.Errorf("paused tracker warning: %+v (all: %+v)", w, byKey)
	}
	w, ok = byKey["flaresolverr"]
	if !ok || !strings.Contains(w.Message, "TorrentLeech can't get past Cloudflare") || w.Link != "/settings/connections#flaresolverr" {
		t.Errorf("FlareSolverr warning: %+v (all: %+v)", w, byKey)
	}
}

// No registry wired (tools, old tests): an empty, healthy report rather than a crash.
func TestHandlerWithoutRegistry(t *testing.T) {
	a := &api{deps: Deps{}}
	w := httptest.NewRecorder()
	a.handleSystemHealth(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var got struct {
		Status   string `json:"status"`
		Warnings []any  `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Status != "ok" || got.Warnings == nil {
		t.Errorf("got %s (%v)", w.Body.String(), err)
	}
}
