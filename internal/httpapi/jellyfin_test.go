package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

const secretJellyfinKey = "jf-secret-4e2f9a"

// fakeJellyfinServer answers the calls the connection makes, for one key.
func fakeJellyfinServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info/Public" {
			_, _ = w.Write([]byte(`{"Id":"f00dfeedf00dfeedf00dfeedf00dfeed","ServerName":"Attic","Version":"12.2.0","StartupWizardCompleted":true}`))
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), `Token="`+secretJellyfinKey+`"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"Id":"f00dfeedf00dfeedf00dfeedf00dfeed","ServerName":"Attic","Version":"12.2.0"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"Name":"Movies","CollectionType":"movies","ItemId":"aaaa","Locations":["/media/Movies"]}]`))
		case "/Library/Media/Updated":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newJellyfinRouteServer(t *testing.T) *routeServer {
	t.Helper()
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	return newRouteServer(t, func(d *Deps) {
		d.APIKeys = apikeys.NewStore(d.Settings)
		d.ConnStatus = connstatus.New(d.Store.DB(), d.Log)
		d.Jellyfin = jellyfinsvc.New(jellyfinsvc.Options{Settings: d.Settings, Keys: d.APIKeys, Version: "test", Log: d.Log})
		d.JellyfinScan = d.Jellyfin.NewScanner(d.Settings, nil, d.Log)
	})
}

// The connection's routes: managers read and test, only an admin saves the key, and the
// key never comes back in any answer.
func TestJellyfinConnectionRoutes(t *testing.T) {
	s := newJellyfinRouteServer(t)
	jf := fakeJellyfinServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)

	if rec := s.do("GET", "/api/v1/jellyfin", kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester GET: HTTP %d", rec.Code)
	}
	rec := s.do("GET", "/api/v1/jellyfin", mgr)
	var cfg map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &cfg) != nil {
		t.Fatalf("GET: HTTP %d %s", rec.Code, rec.Body)
	}
	for _, k := range []string{"url", "public_url", "key_set", "server_name", "server_id", "version", "monitoring_enabled", "poll_seconds"} {
		if _, ok := cfg[k]; !ok {
			t.Errorf("GET /jellyfin lacks %q: %s", k, rec.Body)
		}
	}
	if cfg["key_set"] != false || cfg["poll_seconds"] != float64(10) {
		t.Errorf("empty config = %s", rec.Body)
	}

	body := `{"url":"` + jf.URL + `","public_url":"https://jf.example.com","api_key":"` + secretJellyfinKey + `"}`
	if rec := s.doJSON("PUT", "/api/v1/jellyfin", mgr, body); rec.Code != http.StatusForbidden {
		t.Errorf("manager PUT: HTTP %d, want 403 (the key is a credential)", rec.Code)
	}
	if rec := s.doJSON("PUT", "/api/v1/jellyfin", admin, `{"url":"not a url","api_key":"k"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad url: HTTP %d %s", rec.Code, rec.Body)
	}
	rec = s.doJSON("PUT", "/api/v1/jellyfin", admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: HTTP %d %s", rec.Code, rec.Body)
	}
	var saved jellyfinsvc.Config
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	if !saved.KeySet || saved.URL != jf.URL || saved.PublicURL != "https://jf.example.com" || saved.ServerName != "Attic" || saved.Version != "12.2.0" || saved.ServerID == "" {
		t.Errorf("saved = %+v", saved)
	}
	// A save without a key keeps it.
	if rec := s.doJSON("PUT", "/api/v1/jellyfin", admin, `{"url":"`+jf.URL+`","public_url":""}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"key_set":true`) {
		t.Errorf("save keeping the key: HTTP %d %s", rec.Code, rec.Body)
	}

	// Test: typed values, the saved ones, a wrong key; never the key in an answer.
	var answers []string
	for _, b := range []string{`{}`, `{"url":"` + jf.URL + `","api_key":"wrong-key-0"}`, `{"url":"http://127.0.0.1:1"}`} {
		rec := s.doJSON("POST", "/api/v1/jellyfin/test", mgr, b)
		if rec.Code != http.StatusOK {
			t.Fatalf("test %s: HTTP %d", b, rec.Code)
		}
		answers = append(answers, rec.Body.String())
	}
	if !strings.Contains(answers[0], `"ok":true`) || !strings.Contains(answers[0], `"server_name":"Attic"`) {
		t.Errorf("saved test = %s", answers[0])
	}
	if !strings.Contains(answers[1], `"ok":false`) || !strings.Contains(answers[1], "rejected the API key") || strings.Contains(answers[1], "wrong-key-0") {
		t.Errorf("wrong key test = %s", answers[1])
	}
	if !strings.Contains(answers[2], `"ok":false`) {
		t.Errorf("unreachable test = %s", answers[2])
	}

	// The Connections card: in the alerts-and-apps section (the page lifts it into Media
	// servers), working, with what it's used for.
	rec = s.do("GET", "/api/v1/connections", mgr)
	var list connList
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		t.Fatalf("connections: HTTP %d", rec.Code)
	}
	var other connectionSection
	for _, sec := range list.Sections {
		if sec.ID == "other" {
			other = sec
		}
	}
	if other.Title != "Alerts and apps" {
		t.Errorf("other section title = %q", other.Title)
	}
	it, ok := list.item("jellyfin", "default")
	if !ok || it.Card != "jellyfin" || !it.Configured || !it.Testable || it.State != connstatus.PhaseOK || it.Subtitle != jf.URL ||
		!slices.Equal(it.UsedFor, []string{"watch", "library"}) || it.FixPath != "/settings/connections#jellyfin" {
		t.Errorf("jellyfin card = %+v", it)
	}
	rec = s.do("POST", "/api/v1/connections/jellyfin/default/test", mgr)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Jellyfin (Attic) is answering.") {
		t.Errorf("card test: HTTP %d %s", rec.Code, rec.Body)
	}
	answers = append(answers, rec.Body.String())

	// Library updates switched off: the card stops saying so.
	rec = s.doJSON("PUT", "/api/v1/jellyfin/scan", mgr, `{"scan_on_import":false,"path_map":[{"from":"/movies","to":"/media/Movies"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) || !strings.Contains(rec.Body.String(), `"to":"/media/Movies"`) {
		t.Fatalf("scan save: HTTP %d %s", rec.Code, rec.Body)
	}
	if s.deps.Settings.Get(t.Context(), jellyfinsvc.KeyPathMap, "") == "" || s.deps.Settings.Get(t.Context(), plexscan.KeyPathMap, "") != "" {
		t.Error("the Jellyfin mappings went to the wrong setting")
	}
	rec = s.do("GET", "/api/v1/connections", mgr)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if it, _ := list.item("jellyfin", "default"); !slices.Equal(it.UsedFor, []string{"watch"}) {
		t.Errorf("used_for with scans off = %v", it.UsedFor)
	}
	if rec := s.do("GET", "/api/v1/jellyfin/scan", mgr); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Errorf("scan view: HTTP %d %s", rec.Code, rec.Body)
	}
	answers = append(answers, rec.Body.String())
	for _, a := range answers {
		if strings.Contains(a, secretJellyfinKey) {
			t.Fatalf("an answer carries the key: %s", a)
		}
	}
}

// A refused key shows on the card and as a health error with the Jellyfin Fix link.
func TestJellyfinRefusedKeyOnCard(t *testing.T) {
	s := newJellyfinRouteServer(t)
	jf := fakeJellyfinServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	if rec := s.doJSON("PUT", "/api/v1/jellyfin", admin, `{"url":"`+jf.URL+`","api_key":"revoked-key-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("save: HTTP %d %s", rec.Code, rec.Body)
	}
	rec := s.do("GET", "/api/v1/connections", admin)
	var list connList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	it, _ := list.item("jellyfin", "default")
	if it.State != connstatus.PhaseFailing || !strings.Contains(it.Detail, "rejected") || strings.Contains(rec.Body.String(), "revoked-key-1") {
		t.Fatalf("card = %+v", it)
	}
}

// Disconnect (admin only) forgets the address, the key and the identity, and keeps how
// Arrmada used Jellyfin — library updates and sign-in — for a reconnect.
func TestJellyfinDisconnect(t *testing.T) {
	s := newJellyfinRouteServer(t)
	jf := fakeJellyfinServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	ctx := t.Context()

	body := `{"url":"` + jf.URL + `","public_url":"https://jf.example.com","api_key":"` + secretJellyfinKey + `"}`
	if rec := s.doJSON("PUT", "/api/v1/jellyfin", admin, body); rec.Code != http.StatusOK {
		t.Fatalf("save: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/jellyfin/scan", mgr, `{"enabled":false,"path_map":[{"from":"/movies","to":"/media/Movies"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("scan save: HTTP %d %s", rec.Code, rec.Body)
	}
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, true)
	a := &api{deps: s.deps}
	if !a.jellyfinLoginEnabled(ctx) {
		t.Fatal("sign-in not offered while connected")
	}

	for name, c := range map[string]*http.Cookie{"requester": kid, "manager": mgr} {
		if rec := s.do("DELETE", "/api/v1/jellyfin", c); rec.Code != http.StatusForbidden {
			t.Errorf("%s DELETE: HTTP %d, want 403", name, rec.Code)
		}
	}
	if !s.deps.Jellyfin.Configured(ctx) {
		t.Fatal("a refused disconnect still disconnected")
	}

	rec := s.do("DELETE", "/api/v1/jellyfin", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin DELETE: HTTP %d %s", rec.Code, rec.Body)
	}
	var cfg jellyfinsvc.Config
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	if cfg.URL != "" || cfg.PublicURL != "" || cfg.KeySet || cfg.ServerName != "" || cfg.ServerID != "" || cfg.Version != "" {
		t.Errorf("after disconnect = %+v", cfg)
	}
	if s.deps.Jellyfin.Configured(ctx) || s.deps.APIKeys.Value(ctx, apikeys.Jellyfin) != "" {
		t.Error("the key or the address survived the disconnect")
	}
	if strings.Contains(rec.Body.String(), secretJellyfinKey) {
		t.Fatalf("the answer carries the key: %s", rec.Body)
	}
	// Sign in with Jellyfin leaves the login page while nothing is connected, but the
	// switch and the path mappings wait for a reconnect.
	if a.jellyfinLoginEnabled(ctx) {
		t.Error("sign-in still offered with no server")
	}
	if !s.deps.Settings.GetBool(ctx, keyJellyfinLoginEnabled, false) || !strings.Contains(s.deps.Settings.Get(ctx, jellyfinsvc.KeyPathMap, ""), "/media/Movies") {
		t.Error("disconnect forgot how Jellyfin was used")
	}
	// The card goes back to "Not connected".
	rec = s.do("GET", "/api/v1/connections", admin)
	var list connList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if it, ok := list.item("jellyfin", "default"); ok && it.Configured {
		t.Errorf("card after disconnect = %+v", it)
	}
	// Disconnecting again is harmless.
	if rec := s.do("DELETE", "/api/v1/jellyfin", admin); rec.Code != http.StatusOK {
		t.Errorf("second DELETE: HTTP %d", rec.Code)
	}
}
