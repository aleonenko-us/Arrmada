package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
)

// setupStateBody is the part of GET /api/v1/setup the wizard's later steps read.
type setupStateBody struct {
	PlexConnected     bool       `json:"plex_connected"`
	PlexServer        string     `json:"plex_server"`
	PlexFamily        plexFamily `json:"plex_family"`
	JellyfinConnected bool       `json:"jellyfin_connected"`
	JellyfinServer    string     `json:"jellyfin_server"`
	JellyfinFamily    plexFamily `json:"jellyfin_family"`
	IndexersEnabled   int        `json:"indexers_enabled"`
	ProwlarrAvailable *bool      `json:"prowlarr_available"`
}

// The Indexers step sees how many indexers searches can use, and — only when it asks —
// whether a Prowlarr answers at the saved address.
func TestSetupStateIndexerFields(t *testing.T) {
	a := checklistAPI(t)
	ctx := context.Background()
	b, _ := getSetupState(t, a, "")
	if b.IndexersEnabled != 0 || b.ProwlarrAvailable != nil {
		t.Fatalf("fresh install: indexers_enabled=%d prowlarr_available=%v, want 0 and not asked", b.IndexersEnabled, b.ProwlarrAvailable)
	}
	if _, err := a.deps.Indexers.Create(ctx, indexer.Indexer{Name: "Test", Kind: indexer.KindTorznab, URL: "http://127.0.0.1:1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := getSetupState(t, a, ""); b.IndexersEnabled != 1 {
		t.Errorf("one enabled indexer: indexers_enabled = %d", b.IndexersEnabled)
	}

	// No Prowlarr anywhere: asked, and the answer is no.
	if b, _ := getSetupState(t, a, "?prowlarr=1"); b.ProwlarrAvailable == nil || *b.ProwlarrAvailable {
		t.Errorf("no Prowlarr: prowlarr_available = %v, want false", b.ProwlarrAvailable)
	}
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			fmt.Fprint(w, `{"status":"OK"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(prowlarr.Close)
	if err := a.deps.Settings.Set(ctx, keyProwlarrURL, prowlarr.URL); err != nil {
		t.Fatal(err)
	}
	if b, _ := getSetupState(t, a, "?prowlarr=1"); b.ProwlarrAvailable == nil || !*b.ProwlarrAvailable {
		t.Errorf("Prowlarr answering: prowlarr_available = %v, want true", b.ProwlarrAvailable)
	}
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

// The Media server step's Jellyfin half: connected once an address and key are saved (the
// key never comes back), the server's name, and Sign in with Jellyfin's own family choices
// — off, nothing ticked, until the owner chooses.
func TestSetupStateJellyfin(t *testing.T) {
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	a := checklistAPI(t)
	ctx := context.Background()
	// Without the Jellyfin service wired the fields still answer, as not connected.
	if b, raw := getSetupState(t, a, ""); b.JellyfinConnected || b.JellyfinServer != "" || b.JellyfinFamily.Enabled || !strings.Contains(raw, `"jellyfin_family"`) {
		t.Fatalf("no Jellyfin service: %+v", b)
	}
	a.deps.Jellyfin = jellyfinsvc.New(jellyfinsvc.Options{Settings: a.deps.Settings, Keys: a.deps.APIKeys, Version: "test", Log: a.deps.Log})
	const key = "test-jellyfin-secret-9"
	for k, v := range map[string]string{jellyfinsvc.KeyURL: "http://127.0.0.1:1", jellyfinsvc.KeyServerName: "Attic"} {
		if err := a.deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := getSetupState(t, a, ""); b.JellyfinConnected {
		t.Error("an address without a key reads as connected")
	}
	if err := a.deps.APIKeys.Set(ctx, apikeys.Jellyfin, key); err != nil {
		t.Fatal(err)
	}
	b, raw := getSetupState(t, a, "")
	if !b.JellyfinConnected || b.JellyfinServer != "Attic" {
		t.Errorf("connected: %+v", b)
	}
	if strings.Contains(raw, key) {
		t.Error("GET /setup echoed the Jellyfin key")
	}
	if b.JellyfinFamily.Enabled || b.JellyfinFamily.AutoApproveTypes != "" {
		t.Errorf("fresh family = %+v, want everything off", b.JellyfinFamily)
	}
	if err := a.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, true); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Settings.Set(ctx, keyJellyfinAutoApproveTypes, "movie,series"); err != nil {
		t.Fatal(err)
	}
	b, _ = getSetupState(t, a, "")
	if !b.JellyfinFamily.Enabled || b.JellyfinFamily.AutoApproveTypes != "movie,series" {
		t.Errorf("chosen family = %+v", b.JellyfinFamily)
	}
	// Plex's choices are its own.
	if b.PlexFamily.Enabled {
		t.Error("Jellyfin's switch turned Plex's on")
	}
}
