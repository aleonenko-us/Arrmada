package netsafe

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestSameEndpoint(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"http://192.168.1.10:9696", "http://192.168.1.10:9696/", true},
		{"HTTP://Prowlarr:9696", "http://prowlarr:9696", true},
		{"http://plex:80/", "http://plex", true},
		{"https://plex:443", "https://plex", true},
		{"http://plex:32400?x=1#y", "http://plex:32400", true},
		{" http://plex:32400 ", "http://plex:32400", true},
		{"", "", true},
		{"http://plex:32400", "", false},
		{"http://plex:32400", "https://plex:32400", false},
		{"http://plex:32400", "http://plex:32401", false},
		{"http://plex:32400", "http://evil.example:32400", false},
		{"http://host/sab", "http://host/other", false},
		{"http://host/sab", "http://host", false},
		{"http://a@host", "http://host", false},
		{"http://[::1]:80", "http://[::1]", true},
		{"not a url", "not a url", true},
		{"not a url", "http://host", false},
	} {
		if got := SameEndpoint(tc.a, tc.b); got != tc.want {
			t.Errorf("SameEndpoint(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.17.0.2": false,
		"169.254.169.254": false, "0.0.0.0": false, "::1": false, "fd00::1": false,
		"::ffff:10.0.0.1": false, "100.64.0.1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// The transport refuses to connect to a loopback server, whatever the URL said.
func TestPublicTransportRefusesLoopback(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit = true }))
	defer srv.Close()
	c := &http.Client{Transport: PublicTransport()}
	_, err := c.Get(srv.URL)
	if err == nil || !errors.Is(err, ErrNotPublic) {
		t.Fatalf("err = %v, want ErrNotPublic", err)
	}
	if hit {
		t.Fatal("the loopback server was reached")
	}
}
