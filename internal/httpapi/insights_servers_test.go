package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// PUT /jellyfin/monitoring saves Jellyfin's monitoring switch and interval (clamped) and
// answers with the connection, never the key; GET /insights/servers then lists Jellyfin
// connected and recording.
func TestJellyfinMonitoringAndServers(t *testing.T) {
	a := dashAPI(t)
	ctx := t.Context()
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	// One settings service for both, as in main.
	set := settings.NewService(a.deps.Store.DB())
	a.deps.Insights = insights.NewService(a.deps.Store.DB(), set, nil, nil, a.deps.Log)
	keys := apikeys.NewStore(set)
	if err := keys.Set(ctx, apikeys.Jellyfin, "jf-secret-key"); err != nil {
		t.Fatal(err)
	}
	a.deps.Jellyfin = jellyfinsvc.New(jellyfinsvc.Options{Settings: set, Keys: keys, Version: "test", Log: a.deps.Log})
	if err := set.Set(ctx, jellyfinsvc.KeyURL, "http://jellyfin.lan:8096"); err != nil {
		t.Fatal(err)
	}

	// Before Insights watches Jellyfin, the switch has nowhere to go.
	rec := httptest.NewRecorder()
	a.handleJellyfinMonitoring(rec, httptest.NewRequest("PUT", "/api/v1/jellyfin/monitoring", strings.NewReader(`{"monitoring_enabled":true}`)))
	if rec.Code != 503 {
		t.Fatalf("unwired = %d %s", rec.Code, rec.Body)
	}

	a.deps.Insights.SetJellyfin(a.deps.Jellyfin)
	rec = httptest.NewRecorder()
	a.handleJellyfinMonitoring(rec, httptest.NewRequest("PUT", "/api/v1/jellyfin/monitoring", strings.NewReader(`{"monitoring_enabled":true,"poll_seconds":1}`)))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "jf-secret-key") {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	var cfg jellyfinsvc.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.MonitoringEnabled || cfg.PollSeconds != 2 {
		t.Fatalf("saved = %+v (the interval is clamped to 2–60 s)", cfg)
	}

	rec = httptest.NewRecorder()
	a.handleInsightsServers(rec, httptest.NewRequest("GET", "/api/v1/insights/servers", nil))
	var out struct {
		Servers []insights.ServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Servers) != 2 || out.Servers[0].Server != "plex" || out.Servers[0].Configured ||
		out.Servers[1].Server != "jellyfin" || !out.Servers[1].Configured || !out.Servers[1].Monitoring || out.Servers[1].Status != insights.StatusRecording {
		t.Fatalf("servers = %+v", out.Servers)
	}
}
