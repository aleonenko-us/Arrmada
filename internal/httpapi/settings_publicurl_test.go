package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The outside address is the owner's call (admin only), must be a plain http(s) origin
// (Arrmada is served at the root), and is stored tidied.
func TestPublicURLSetting(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "admin", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr", auth.RoleManager)

	if rec := s.doJSON("PUT", "/api/v1/settings", mgr, `{"app_public_url":"https://arrmada.example.com"}`); rec.Code != http.StatusForbidden {
		t.Errorf("manager: HTTP %d, want 403", rec.Code)
	}
	for _, bad := range []string{"arrmada.example.com", "https://arrmada.example.com/arrmada", "javascript:alert(1)", "https://u:p@x.example.com"} {
		if rec := s.doJSON("PUT", "/api/v1/settings", admin, `{"app_public_url":"`+bad+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: HTTP %d, want 400", bad, rec.Code)
		}
	}
	if rec := s.doJSON("PUT", "/api/v1/settings", admin, `{"app_public_url":" HTTPS://Arrmada.Example.com/ "}`); rec.Code != http.StatusOK {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := s.deps.Settings.Get(context.Background(), settings.KeyPublicURL, ""); got != "https://arrmada.example.com" {
		t.Errorf("stored %q", got)
	}
	// A manager saving the whole page back unchanged still saves.
	if rec := s.doJSON("PUT", "/api/v1/settings", mgr, `{"app_public_url":"https://arrmada.example.com","search_on_add":true}`); rec.Code != http.StatusOK {
		t.Errorf("manager echo: HTTP %d %s", rec.Code, rec.Body)
	}
}
