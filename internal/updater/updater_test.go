package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
)

func TestFromEnvNeedsBoth(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, m := range map[string]map[string]string{
		"nothing":     {},
		"no token":    {"ARRMADA_UPDATER_URL": "http://arrmada-updater:8080"},
		"no url":      {"ARRMADA_UPDATER_TOKEN": "t"},
		"not http":    {"ARRMADA_UPDATER_URL": "file:///etc/passwd", "ARRMADA_UPDATER_TOKEN": "t"},
		"no host":     {"ARRMADA_UPDATER_URL": "http://", "ARRMADA_UPDATER_TOKEN": "t"},
		"blank token": {"ARRMADA_UPDATER_URL": "http://arrmada-updater:8080", "ARRMADA_UPDATER_TOKEN": "  "},
	} {
		if c := FromEnv(env(m)); c != nil || c.Available() {
			t.Errorf("%s: got a client, want none", name)
		}
	}
	c := FromEnv(env(map[string]string{"ARRMADA_UPDATER_URL": "http://arrmada-updater:8080/", "ARRMADA_UPDATER_TOKEN": "t"}))
	if !c.Available() {
		t.Fatal("a URL and a token should make a client")
	}
}

// fakeUpdater answers like docker/updater/updater.cgi.
func fakeUpdater(t *testing.T, token string, apply func() (int, string), status func() (int, string)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/updater" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var code int
		var body string
		switch r.Method + ":" + r.URL.RawQuery {
		case "POST:apply":
			code, body = apply()
		case "GET:status":
			code, body = status()
		default:
			code, body = http.StatusNotFound, `{"error":"not found"}`
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, token, srv.Client())
}

func TestApplyAndProgress(t *testing.T) {
	ctx := context.Background()
	c := fakeUpdater(t, "tok",
		func() (int, string) { return http.StatusAccepted, `{"state":"running"}` },
		func() (int, string) {
			return http.StatusOK, `{"state":"running","started_at":"2026-10-20T03:00:00Z","log":"backing up"}`
		})
	if err := c.Apply(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	p, err := c.Progress(ctx)
	if err != nil || p.State != StateRunning || p.Log != "backing up" || p.StartedAt == "" {
		t.Fatalf("progress: %+v, %v", p, err)
	}
}

func TestApplyBusyAndRefused(t *testing.T) {
	ctx := context.Background()
	busy := fakeUpdater(t, "tok",
		func() (int, string) { return http.StatusConflict, `{"error":"an update is already running"}` },
		func() (int, string) { return http.StatusOK, `{"state":"running"}` })
	if err := busy.Apply(ctx); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: got %v", err)
	}

	wrong := fakeUpdater(t, "right",
		func() (int, string) { return http.StatusAccepted, `{}` },
		func() (int, string) { return http.StatusOK, `{"state":"idle"}` })
	wrong.token = "wrong"
	if err := wrong.Apply(ctx); !errors.Is(err, ErrRefused) {
		t.Errorf("wrong token on apply: got %v", err)
	}
	if _, err := wrong.Progress(ctx); !errors.Is(err, ErrRefused) {
		t.Errorf("wrong token on progress: got %v", err)
	}

	broken := fakeUpdater(t, "tok",
		func() (int, string) {
			return http.StatusInternalServerError, `{"error":"update.sh isn't in the install folder"}`
		},
		func() (int, string) { return http.StatusOK, `not json` })
	if err := broken.Apply(ctx); err == nil || errors.Is(err, ErrBusy) {
		t.Errorf("a 500 should be an error that says why, got %v", err)
	}
	if _, err := broken.Progress(ctx); err == nil {
		t.Error("unreadable progress should be an error")
	}
}

func TestUnreachableUpdater(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := New(url, "tok", nil)
	if err := c.Apply(context.Background()); err == nil {
		t.Error("an updater that isn't there should be an error")
	}
}

func TestNilClientIsHarmless(t *testing.T) {
	var c *Client
	if c.Available() {
		t.Error("nil client reported available")
	}
	if err := c.Apply(context.Background()); err == nil {
		t.Error("apply on no client should fail")
	}
	if _, err := c.Progress(context.Background()); err == nil {
		t.Error("progress on no client should fail")
	}
}

func TestAutoDecision(t *testing.T) {
	// 03:30 local on 21 Oct: inside the quiet hours.
	now := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
	good := AutoInput{
		Enabled: true, Configured: true, Now: now,
		Status: updatecheck.Status{
			Enabled: true, Channel: updatecheck.ChannelRelease, Available: true,
			Latest: &updatecheck.Release{Tag: "v0.3.3", PublishedAt: now.Add(-30 * time.Hour)},
		},
	}
	if ok, why := AutoDecision(good); !ok {
		t.Fatalf("the plain case should update, said %q", why)
	}

	cases := map[string]func(in *AutoInput){
		"off":                  func(in *AutoInput) { in.Enabled = false },
		"no updater":           func(in *AutoInput) { in.Configured = false },
		"unraid":               func(in *AutoInput) { in.Status.Host = updatecheck.HostUnraid },
		"follows main":         func(in *AutoInput) { in.Status.Channel = updatecheck.ChannelMain },
		"dev build":            func(in *AutoInput) { in.Status.Channel = updatecheck.ChannelDev },
		"check off":            func(in *AutoInput) { in.Status.Enabled = false },
		"nothing newer":        func(in *AutoInput) { in.Status.Available = false },
		"no release known":     func(in *AutoInput) { in.Status.Latest = nil },
		"already started":      func(in *AutoInput) { in.LastTag = "v0.3.3" },
		"unknown release date": func(in *AutoInput) { in.Status.Latest.PublishedAt = time.Time{} },
		"too new":              func(in *AutoInput) { in.Status.Latest.PublishedAt = now.Add(-2 * time.Hour) },
		"before quiet hours":   func(in *AutoInput) { in.Now = time.Date(2026, 10, 21, 1, 59, 0, 0, time.UTC) },
		"after quiet hours":    func(in *AutoInput) { in.Now = time.Date(2026, 10, 21, 6, 0, 0, 0, time.UTC) },
		"midday":               func(in *AutoInput) { in.Now = time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC) },
	}
	for name, mutate := range cases {
		in := good
		rel := *good.Status.Latest // a copy: some cases change the release
		in.Status.Latest = &rel
		mutate(&in)
		if ok, why := AutoDecision(in); ok || why == "" {
			t.Errorf("%s: updated=%v why=%q, want a reason to wait", name, ok, why)
		}
	}

	// A commit build that follows the releases is eligible too: update.sh moves it once a
	// release includes its code, and the check says so through Available.
	in := good
	in.Status.Channel = updatecheck.ChannelBuild
	if ok, why := AutoDecision(in); !ok {
		t.Errorf("a build following the releases should update, said %q", why)
	}
}
