package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/embysvc"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

const testEmbyKey = "emby-test-secret"

func testEmbyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/emby/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/emby")
		}
		if r.URL.Path == "/System/Info/Public" {
			_, _ = w.Write([]byte(`{"Id":"f00dfeedf00dfeedf00dfeedf00dfeed","ServerName":"Attic","Version":"4.9.0.43"}`))
			return
		}
		if r.Header.Get("X-Emby-Token") != testEmbyKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"Id":"f00dfeedf00dfeedf00dfeedf00dfeed","ServerName":"Attic","Version":"4.9.0.43"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"Name":"Movies","CollectionType":"movies","ItemId":"movies","Locations":["/media/Movies"]}]`))
		case "/Library/Media/Updated":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newEmbyRouteServer(t *testing.T) *routeServer {
	t.Helper()
	t.Setenv("ARRMADA_EMBY_API_KEY", "")
	return newRouteServer(t, func(d *Deps) {
		d.APIKeys = apikeys.NewStore(d.Settings)
		d.ConnStatus = connstatus.New(d.Store.DB(), d.Log)
		d.Emby = embysvc.New(embysvc.Options{Settings: d.Settings, Keys: d.APIKeys, Version: "test", Log: d.Log})
		d.EmbyScan = d.Emby.NewScanner(d.Settings, nil, d.Log)
	})
}

func TestEmbyConnectionAndLibraryScanRoutes(t *testing.T) {
	s := newEmbyRouteServer(t)
	emby := testEmbyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)

	if rec := s.do("GET", "/api/v1/emby", kid); rec.Code != http.StatusForbidden {
		t.Fatalf("requester GET: HTTP %d", rec.Code)
	}
	body := `{"url":"` + emby.URL + `","public_url":"https://watch.example.com","api_key":"` + testEmbyKey + `"}`
	if rec := s.doJSON("PUT", "/api/v1/emby", mgr, body); rec.Code != http.StatusForbidden {
		t.Fatalf("manager PUT: HTTP %d", rec.Code)
	}
	rec := s.doJSON("PUT", "/api/v1/emby", admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: HTTP %d %s", rec.Code, rec.Body)
	}
	var cfg embysvc.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.KeySet || cfg.URL != emby.URL || cfg.PublicURL != "https://watch.example.com" || cfg.ServerName != "Attic" || cfg.Version != "4.9.0.43" {
		t.Fatalf("config = %+v", cfg)
	}
	if strings.Contains(rec.Body.String(), testEmbyKey) {
		t.Fatal("connection response exposed the API key")
	}

	rec = s.do("GET", "/api/v1/connections", mgr)
	var conns connList
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &conns) != nil {
		t.Fatalf("connections: HTTP %d %s", rec.Code, rec.Body)
	}
	it, ok := conns.item("emby", "default")
	if !ok || !it.Configured || it.Card != "emby" || it.State != connstatus.PhaseOK || it.Subtitle != emby.URL || !it.Testable {
		t.Fatalf("Emby card = %+v", it)
	}
	rec = s.do("POST", "/api/v1/connections/emby/default/test", mgr)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Emby (Attic) is answering") {
		t.Fatalf("connection test: HTTP %d %s", rec.Code, rec.Body)
	}

	rec = s.doJSON("PUT", "/api/v1/emby/scan", mgr, `{"enabled":false,"path_map":[{"from":"/movies","to":"/media/Movies"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("scan save: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := s.deps.Settings.Get(t.Context(), embysvc.KeyPathMap, ""); got == "" {
		t.Fatal("Emby path mapping was not saved")
	}
	if got := s.deps.Settings.Get(t.Context(), plexscan.KeyPathMap, ""); got != "" {
		t.Fatalf("Emby mapping leaked to Plex settings: %q", got)
	}

	if rec := s.do("DELETE", "/api/v1/emby", mgr); rec.Code != http.StatusForbidden {
		t.Fatalf("manager DELETE: HTTP %d", rec.Code)
	}
	if rec := s.do("DELETE", "/api/v1/emby", admin); rec.Code != http.StatusOK {
		t.Fatalf("admin DELETE: HTTP %d %s", rec.Code, rec.Body)
	}
}
