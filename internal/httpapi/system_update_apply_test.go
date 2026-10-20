package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
	"github.com/tristenlammi/arrmada/internal/updater"
)

// updateAPI is an api whose update check has found v1.3.0 while running v1.2.0.
func updateAPI(t *testing.T) *api {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://github.com/tristenlammi/Arrmada/releases/tag/v1.3.0","body":"* Faster"}`))
	}))
	t.Cleanup(gh.Close)
	a := settingsAPI(t)
	a.deps.Log = slog.New(slog.DiscardHandler)
	a.deps.Updates = updatecheck.New(updatecheck.Options{
		Client: gh.Client(), BaseURL: gh.URL, Version: "v1.2.0", Commit: "abc1234",
		Enabled: func(ctx context.Context) bool { return a.deps.Settings.GetBool(ctx, updatecheck.KeyEnabled, true) },
	})
	a.deps.Updates.CheckNow(context.Background())
	return a
}

// fakeUpdaterClient answers like the updater container; apply is what POST ?apply gets.
func fakeUpdaterClient(t *testing.T, applyCode int, hits *atomic.Int32) *updater.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch r.Method + ":" + r.URL.RawQuery {
		case "POST:apply":
			hits.Add(1)
			w.WriteHeader(applyCode)
			_, _ = w.Write([]byte(`{"state":"running","error":"an update is already running"}`))
		case "GET:status":
			_, _ = w.Write([]byte(`{"state":"running","started_at":"2026-10-21T03:00:00Z","log":"backing up"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return updater.New(srv.URL, "tok", srv.Client())
}

func callUpdate(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
	return w
}

func statusOf(t *testing.T, a *api) updatecheck.Status {
	t.Helper()
	st := a.updateStatus(context.Background())
	if st == nil {
		t.Fatal("no update status")
	}
	return *st
}

// With no updater set up, Status says so, Update now and the automatic switch refuse, and
// nothing is installed.
func TestUpdateApplyNeedsTheUpdater(t *testing.T) {
	a := updateAPI(t)
	if st := statusOf(t, a); st.Apply != "needs_setup" || st.AutoUpdate {
		t.Fatalf("status: apply=%q auto=%v", st.Apply, st.AutoUpdate)
	}
	if w := callUpdate(a.handleUpdateApply, http.MethodPost, ""); w.Code != http.StatusConflict {
		t.Errorf("apply without an updater: %d %s", w.Code, w.Body.String())
	}
	if w := callUpdate(a.handleUpdateProgress, http.MethodGet, ""); w.Code != http.StatusConflict {
		t.Errorf("progress without an updater: %d", w.Code)
	}
	if w := callUpdate(a.handleSetAutoUpdate, http.MethodPut, `{"enabled":true}`); w.Code != http.StatusConflict {
		t.Errorf("turning automatic updates on without an updater: %d", w.Code)
	}
	if a.deps.Settings.GetBool(context.Background(), updater.KeyAutoEnabled, false) {
		t.Error("the refused switch was saved anyway")
	}
	// Turning it off is always allowed.
	if w := callUpdate(a.handleSetAutoUpdate, http.MethodPut, `{"enabled":false}`); w.Code != http.StatusOK {
		t.Errorf("turning automatic updates off: %d", w.Code)
	}
}

func TestUpdateApplyStartsTheUpdate(t *testing.T) {
	var hits atomic.Int32
	a := updateAPI(t)
	a.deps.Updater = fakeUpdaterClient(t, http.StatusAccepted, &hits)

	if st := statusOf(t, a); st.Apply != "ready" {
		t.Fatalf("status apply = %q, want ready", st.Apply)
	}
	w := callUpdate(a.handleUpdateApply, http.MethodPost, "")
	if w.Code != http.StatusAccepted || hits.Load() != 1 {
		t.Fatalf("apply: %d %s (updater hit %d times)", w.Code, w.Body.String(), hits.Load())
	}
	var p updater.Progress
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.State != updater.StateRunning {
		t.Errorf("apply answered %q (%v), want a running state", w.Body.String(), err)
	}

	w = callUpdate(a.handleUpdateProgress, http.MethodGet, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "backing up") {
		t.Errorf("progress: %d %s", w.Code, w.Body.String())
	}

	// The switch can be turned on now, and Status carries it.
	if w := callUpdate(a.handleSetAutoUpdate, http.MethodPut, `{"enabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("automatic updates on: %d %s", w.Code, w.Body.String())
	}
	if st := statusOf(t, a); !st.AutoUpdate {
		t.Error("status doesn't show automatic updates on")
	}
	if w := callUpdate(a.handleSetAutoUpdate, http.MethodPut, `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("a switch with no value: %d", w.Code)
	}
}

func TestUpdateApplyRefusals(t *testing.T) {
	var hits atomic.Int32

	// An update already running is a conflict, not an error.
	a := updateAPI(t)
	a.deps.Updater = fakeUpdaterClient(t, http.StatusConflict, &hits)
	if w := callUpdate(a.handleUpdateApply, http.MethodPost, ""); w.Code != http.StatusConflict {
		t.Errorf("a second apply: %d %s", w.Code, w.Body.String())
	}

	// Already on the newest release: nothing to install, and the updater isn't asked.
	hits.Store(0)
	b := updateAPI(t)
	b.deps.Updater = fakeUpdaterClient(t, http.StatusAccepted, &hits)
	b.deps.Updates = updatecheck.New(updatecheck.Options{Version: "v1.3.0", Commit: "abc1234", BaseURL: "http://127.0.0.1:1"})
	if w := callUpdate(b.handleUpdateApply, http.MethodPost, ""); w.Code != http.StatusConflict || hits.Load() != 0 {
		t.Errorf("apply with nothing newer: %d, updater hit %d times", w.Code, hits.Load())
	}

	// Unraid updates the container from its own Docker page.
	hits.Store(0)
	c := updateAPI(t)
	c.deps.Updater = fakeUpdaterClient(t, http.StatusAccepted, &hits)
	c.deps.Updates = updatecheck.New(updatecheck.Options{Version: "v1.2.0", Commit: "abc1234", Host: updatecheck.HostUnraid, BaseURL: "http://127.0.0.1:1"})
	if st := statusOf(t, c); st.Apply != "" {
		t.Errorf("on Unraid apply = %q, want none", st.Apply)
	}
	if w := callUpdate(c.handleUpdateApply, http.MethodPost, ""); w.Code != http.StatusConflict || hits.Load() != 0 {
		t.Errorf("apply on Unraid: %d, updater hit %d times", w.Code, hits.Load())
	}

	// An updater that doesn't take our token is a bad gateway that says why.
	d := updateAPI(t)
	d.deps.Updater = updater.New("http://127.0.0.1:1", "x", nil)
	if w := callUpdate(d.handleUpdateApply, http.MethodPost, ""); w.Code != http.StatusBadGateway {
		t.Errorf("apply with an unreachable updater: %d", w.Code)
	}
}
