package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

func prowlarrInfo(t *testing.T, a *api) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	a.handleProwlarrInfo(w, httptest.NewRequest(http.MethodGet, "/api/v1/indexers/prowlarr", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("info returned %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The panel learns the host port Prowlarr's UI is published on (9696 unless compose says
// otherwise), a saved UI address, and how the last sync went — never the key.
func TestProwlarrInfoPortAndLastSync(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	a := &api{deps: Deps{Settings: set}}
	ctx := context.Background()

	t.Setenv("ARRMADA_PROWLARR_HOST_PORT", "")
	if got := prowlarrInfo(t, a); got["ui_port"] != "9696" || got["ui_url"] != "" || got["last_sync"] != nil {
		t.Fatalf("defaults = %+v", got)
	}

	t.Setenv("ARRMADA_PROWLARR_HOST_PORT", "9697")
	_ = set.Set(ctx, indexer.SettingProwlarrKey, "prowlarr-secret-key")
	_ = set.Set(ctx, indexer.SettingProwlarrUIURL, "https://prowlarr.example.test")
	_ = set.Set(ctx, indexer.SettingProwlarrLastSync, `{"at":"2026-10-11T14:00:00Z","trigger":"schedule","added":2,"updated":0,"disabled":1,"reenabled":0}`)
	got := prowlarrInfo(t, a)
	if got["ui_port"] != "9697" || got["ui_url"] != "https://prowlarr.example.test" || got["has_key"] != true {
		t.Fatalf("info = %+v", got)
	}
	ls, _ := got["last_sync"].(map[string]any)
	if ls["added"] != float64(2) || ls["disabled"] != float64(1) || ls["at"] != "2026-10-11T14:00:00Z" {
		t.Fatalf("last_sync = %+v", ls)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "prowlarr-secret-key") {
		t.Fatal("the key reached the response")
	}
}

// Only an http(s) address is saved as Prowlarr's UI link; blank clears it.
func TestProwlarrSettingsUIURL(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	a := &api{deps: Deps{Settings: set}}
	put := func(body string) int {
		w := httptest.NewRecorder()
		a.handleProwlarrSettings(w, httptest.NewRequest(http.MethodPut, "/api/v1/indexers/prowlarr", strings.NewReader(body)))
		return w.Code
	}
	for _, bad := range []string{`{"ui_url":"javascript:alert(1)"}`, `{"ui_url":"ftp://x.test"}`, `{"ui_url":"http://user:pw@x.test"}`, `{"ui_url":"http://"}`} {
		if code := put(bad); code != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400", bad, code)
		}
	}
	if code := put(`{"ui_url":" http://192.168.1.10:9697/ "}`); code != http.StatusOK {
		t.Fatalf("good address → %d", code)
	}
	if v := set.Get(context.Background(), indexer.SettingProwlarrUIURL, ""); v != "http://192.168.1.10:9697" {
		t.Fatalf("saved %q", v)
	}
	if code := put(`{"ui_url":""}`); code != http.StatusOK {
		t.Fatalf("clear → %d", code)
	}
	if v := set.Get(context.Background(), indexer.SettingProwlarrUIURL, "x"); v != "" {
		t.Fatalf("after clearing: %q", v)
	}
}
