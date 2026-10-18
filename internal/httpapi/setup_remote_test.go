package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// First-run setup makes the first admin, so on a fresh install reachable from the internet
// whoever got there first would own it. It's LAN-only unless ARRMADA_ALLOW_REMOTE_SETUP is
// set, and /status tells the page so it can explain.
func TestSetupIsLANOnly(t *testing.T) {
	setup := func(s *routeServer, remote string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://arrmada.local/api/v1/auth/setup", strings.NewReader(`{"username":"owner@example.com","password":"long-enough-1"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		return rec
	}
	status := func(s *routeServer, remote string, hdr map[string]string) string {
		r := httptest.NewRequest("GET", "http://arrmada.local/api/v1/status", nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		return rec.Body.String()
	}
	tunnel := map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}

	s := newRouteServer(t, func(d *Deps) { d.Config.ExternalHeader = "Cf-Connecting-Ip" })
	if body := status(s, "203.0.113.50:5000", nil); !strings.Contains(body, `"setup_blocked":true`) {
		t.Errorf("status from outside: %s", body)
	}
	for _, c := range []struct {
		remote string
		hdr    map[string]string
	}{{"203.0.113.50:5000", nil}, {"172.18.0.4:5000", tunnel}} {
		if rec := setup(s, c.remote, c.hdr); rec.Code != http.StatusForbidden {
			t.Errorf("setup from outside (%s %v): HTTP %d %s, want 403", c.remote, c.hdr, rec.Code, rec.Body)
		}
	}
	if n, _ := s.auth.CountAdmins(t.Context()); n != 0 {
		t.Fatalf("an admin was made from outside")
	}
	if body := status(s, "192.168.1.20:5000", nil); !strings.Contains(body, `"setup_blocked":false`) {
		t.Errorf("status on the LAN: %s", body)
	}
	if rec := setup(s, "192.168.1.20:5000", nil); rec.Code != http.StatusCreated {
		t.Fatalf("setup on the LAN: HTTP %d %s", rec.Code, rec.Body)
	}
	// Done: nothing to block any more, wherever the visitor is.
	if body := status(s, "203.0.113.50:5000", nil); !strings.Contains(body, `"setup_blocked":false`) {
		t.Errorf("status once set up: %s", body)
	}

	// The escape hatch: ARRMADA_ALLOW_REMOTE_SETUP=1.
	open := newRouteServer(t, func(d *Deps) {
		d.Config.ExternalHeader = "Cf-Connecting-Ip"
		d.Config.AllowRemoteSetup = true
	})
	if body := status(open, "172.18.0.4:5000", tunnel); !strings.Contains(body, `"setup_blocked":false`) {
		t.Errorf("status with remote setup allowed: %s", body)
	}
	if rec := setup(open, "172.18.0.4:5000", tunnel); rec.Code != http.StatusCreated {
		t.Errorf("setup through the tunnel with remote setup allowed: HTTP %d %s", rec.Code, rec.Body)
	}
}
