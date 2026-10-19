package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The MusicBrainz card is the owner's (admin only); the address must be http(s) but may be
// a LAN mirror; the rate is bounded; the contact is one email address.
func TestMusicBrainzSettings(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "admin", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	ctx := context.Background()

	for _, call := range []struct{ method, path, body string }{
		{"GET", "/api/v1/settings/musicbrainz", ""},
		{"PUT", "/api/v1/settings/musicbrainz", `{"base_url":"http://192.168.1.20:5000"}`},
		{"POST", "/api/v1/settings/musicbrainz/test", `{}`},
	} {
		if rec := s.doJSON(call.method, call.path, mgr, call.body); rec.Code != http.StatusForbidden {
			t.Errorf("manager %s %s: HTTP %d, want 403", call.method, call.path, rec.Code)
		}
	}
	if got := s.deps.Settings.Get(ctx, settings.KeyMusicBrainzURL, ""); got != "" {
		t.Fatalf("a manager's PUT saved %q", got)
	}

	for _, bad := range []string{
		`{"base_url":"ftp://mirror.lan"}`, `{"base_url":"mirror.lan"}`, `{"base_url":"javascript:alert(1)"}`,
		`{"base_url":"http://user:pass@mirror.lan"}`, `{"mirror_rate":0}`, `{"mirror_rate":51}`,
		`{"contact":"not an email"}`, `{"contact":"Owner <owner@example.com>"}`,
	} {
		if rec := s.doJSON("PUT", "/api/v1/settings/musicbrainz", admin, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", bad, rec.Code)
		}
	}
	// A private mirror is fine: that's what a self-hosted one is.
	rec := s.doJSON("PUT", "/api/v1/settings/musicbrainz", admin,
		`{"base_url":" http://192.168.1.20:5000/ ","mirror_rate":20,"contact":"owner@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	if got := s.deps.Settings.Get(ctx, settings.KeyMusicBrainzURL, ""); got != "http://192.168.1.20:5000" {
		t.Errorf("stored url %q", got)
	}
	var v musicBrainzView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.MirrorRate != 20 || v.Contact != "owner@example.com" {
		t.Errorf("view = %+v (%v)", v, err)
	}
	// Setting the public address back stores the default, not the URL.
	if rec := s.doJSON("PUT", "/api/v1/settings/musicbrainz", admin, `{"base_url":"https://musicbrainz.org/"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: HTTP %d", rec.Code)
	}
	if got := s.deps.Settings.Get(ctx, settings.KeyMusicBrainzURL, "x"); got != "" {
		t.Errorf("public address stored as %q, want the default", got)
	}
}

// The contact stays out of the support bundle and is blanked from its logs; the address
// (a URL) too.
func TestMusicBrainzSettingsStayOutOfTheBundle(t *testing.T) {
	split := bundleSettings(map[string]string{
		settings.KeyMusicBrainzContact:    "owner@example.com",
		settings.KeyMusicBrainzURL:        "http://192.168.1.20:5000",
		settings.KeyMusicBrainzMirrorRate: "20",
	})
	for _, k := range []string{settings.KeyMusicBrainzContact, settings.KeyMusicBrainzURL} {
		if _, ok := split.doc.Included[k]; ok {
			t.Errorf("%s is in the bundle", k)
		}
	}
	if !strings.Contains(strings.Join(split.secretValues, " "), "owner@example.com") {
		t.Error("the contact must be blanked from the bundle's logs")
	}
}
