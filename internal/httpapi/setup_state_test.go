package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupStateBody is the part of GET /api/v1/setup the wizard's later steps read.
type setupStateBody struct {
	PlexConnected bool       `json:"plex_connected"`
	PlexServer    string     `json:"plex_server"`
	PlexFamily    plexFamily `json:"plex_family"`
}

func getSetupState(t *testing.T, a *api, query string) (setupStateBody, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleSetupState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/setup"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var b setupStateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b, rec.Body.String()
}

// The Plex step knows whether a server is connected; the token never comes back.
func TestSetupStatePlexConnected(t *testing.T) {
	a := checklistAPI(t)
	ctx := context.Background()
	if b, _ := getSetupState(t, a, ""); b.PlexConnected {
		t.Fatal("fresh install: plex_connected = true")
	}
	tok := "test-plex-token"
	if err := a.deps.Insights.SetConfig(ctx, "http://127.0.0.1:1", &tok, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, raw := getSetupState(t, a, "")
	if !b.PlexConnected {
		t.Error("a URL and token are saved, but plex_connected = false")
	}
	if strings.Contains(raw, tok) {
		t.Error("GET /setup echoed the Plex token")
	}
}

// The wizard's family switches start off on a fresh install — auto-approve included, even
// though an untouched install's default lets movies through — and show an owner's own
// choices when setup is run again.
func TestSetupStatePlexFamily(t *testing.T) {
	a := checklistAPI(t)
	ctx := context.Background()
	if b, _ := getSetupState(t, a, ""); b.PlexFamily.Enabled || b.PlexFamily.AutoApproveTypes != "" {
		t.Errorf("fresh install: family = %+v, want everything off", b.PlexFamily)
	}
	if err := a.deps.Settings.SetBool(ctx, "plex_login_enabled", true); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Settings.Set(ctx, keyPlexAutoApproveTypes, "movie,series"); err != nil {
		t.Fatal(err)
	}
	if b, _ := getSetupState(t, a, ""); !b.PlexFamily.Enabled || b.PlexFamily.AutoApproveTypes != "movie,series" {
		t.Errorf("chosen: family = %+v, want enabled with movie,series", b.PlexFamily)
	}
	// An owner who chose "none" keeps none.
	if err := a.deps.Settings.Set(ctx, keyPlexAutoApproveTypes, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := getSetupState(t, a, ""); b.PlexFamily.AutoApproveTypes != "" {
		t.Errorf("chose none: auto_approve_types = %q", b.PlexFamily.AutoApproveTypes)
	}
}
