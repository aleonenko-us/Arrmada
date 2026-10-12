package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

func newSubtitlesServer(t *testing.T) *routeServer {
	t.Helper()
	return newRouteServer(t, func(d *Deps) {
		d.APIKeys = apikeys.NewStore(d.Settings)
		prov := subtitles.NewOpenSubtitles("", "", "")
		d.Subtitles = subtitles.NewService(d.Store.DB(), nil, nil, d.Settings, prov, "ffmpeg", "ffprobe", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
}

// Saving an OpenSubtitles credential lets the next sweep retry what a rejected login
// parked; other parked files keep waiting.
func TestSavingOpenSubtitlesCredsClearsAuthBackoff(t *testing.T) {
	s := newSubtitlesServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	ctx := t.Context()
	l := s.deps.Subtitles.Ledger()
	for _, b := range []subtitles.Backoff{
		{MediaKey: "m:1", Lang: "en", VideoPath: "/a.mkv", PrimaryReason: subtitles.ResultAuthFailed, Count: 1, ClearOn: []string{subtitles.EventCreds}},
		{MediaKey: "m:2", Lang: "en", VideoPath: "/b.mkv", PrimaryReason: subtitles.ResultModelMissing, Count: 1, ClearOn: []string{subtitles.EventModel}},
	} {
		if err := l.SaveBackoff(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if rec := s.doBody("PUT", "/api/v1/apikeys/opensubtitles_password", `{"value":"new-password"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("save: HTTP %d %s", rec.Code, rec.Body)
	}
	left, err := l.LoadBackoffs(ctx)
	if err != nil || len(left) != 1 {
		t.Fatalf("backoff after saving the password = %v (%v), want only the model-parked row", left, err)
	}
	if _, ok := left[subtitles.BackoffKey("m:2", "en", "")]; !ok {
		t.Errorf("the model-parked row went too: %v", left)
	}
	// The password is never echoed back, and the settings name the provider's real state.
	if rec := s.do("GET", "/api/v1/subtitles/settings", admin); rec.Code != http.StatusOK {
		t.Fatalf("settings: HTTP %d", rec.Code)
	} else {
		var st map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &st)
		if st["provider_state"] != subtitles.ProviderNotConfigured {
			t.Errorf("provider_state = %v, want not_configured (no API key yet)", st["provider_state"])
		}
	}
}

// The attempt history pages newest first and survives as rows in the database.
func TestSubtitleHistoryPages(t *testing.T) {
	s := newSubtitlesServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleRequester)
	l := s.deps.Subtitles.Ledger()
	for _, run := range []string{"a", "b", "c"} {
		if err := l.RecordAttempts(t.Context(), run, "m:7", "/m.mkv", []subtitles.Outcome{
			{Lang: "en", Rung: subtitles.RungOpenSubtitles, Result: subtitles.ResultAuthFailed, Detail: "OpenSubtitles login rejected (401)"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	var page struct {
		Attempts   []subtitles.Attempt `json:"attempts"`
		NextBefore int64               `json:"next_before"`
	}
	rec := s.do("GET", "/api/v1/subtitles/history?media_key=m:7&limit=2", mgr)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		t.Fatalf("history: HTTP %d %s", rec.Code, rec.Body)
	}
	if len(page.Attempts) != 2 || page.Attempts[0].RunID != "c" || page.NextBefore == 0 || page.Attempts[0].Outcome != subtitles.ResultAuthFailed {
		t.Fatalf("page 1 = %+v", page)
	}
	rec = s.do("GET", "/api/v1/subtitles/history?media_key=m:7&limit=2&before="+strconv.FormatInt(page.NextBefore, 10), mgr)
	if json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Attempts) != 1 || page.Attempts[0].RunID != "a" || page.NextBefore != 0 {
		t.Errorf("page 2 = %+v", page)
	}
	if rec := s.do("GET", "/api/v1/subtitles/history?limit=x", mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit: HTTP %d", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/subtitles/history", viewer); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
}
