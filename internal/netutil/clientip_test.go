package netutil

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		name, remote string
		headers      map[string]string
		want         string
	}{
		{"public peer, forged XFF", "198.51.100.7:5000", map[string]string{"X-Forwarded-For": "6.6.6.6"}, "198.51.100.7"},
		{"public peer, forged Cf header", "198.51.100.7:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}, "198.51.100.7"},
		{"no headers", "198.51.100.7:5000", nil, "198.51.100.7"},
		{"cloudflared container", "172.18.0.4:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9", "X-Forwarded-For": "6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"loopback proxy, Cf header", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}, "203.0.113.9"},
		{"XFF right to left", "10.0.0.2:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"XFF skips our private proxies", "10.0.0.2:5000", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.9, 10.0.0.5"}, "203.0.113.9"},
		{"XFF all private → the peer", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "192.168.1.20, 10.0.0.5"}, "127.0.0.1"},
		{"LAN machine making up a private XFF", "192.168.1.30:5000", map[string]string{"X-Forwarded-For": "10.9.9.9"}, "192.168.1.30"},
		{"LAN machine making up a private Cf header", "192.168.1.30:5000", map[string]string{"Cf-Connecting-Ip": "10.9.9.9"}, "192.168.1.30"},
		{"XFF junk skipped", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "unknown, 203.0.113.9"}, "203.0.113.9"},
		{"bad Cf header falls through", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "nope", "X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"Forwarded with port and quotes", "127.0.0.1:5000", map[string]string{"Forwarded": `for="198.51.100.5:443";proto=https`}, "198.51.100.5"},
		{"Forwarded IPv6 in brackets", "127.0.0.1:5000", map[string]string{"Forwarded": `for="[2606:4700::1]:4711"`}, "2606:4700::1"},
		{"Forwarded right to left", "127.0.0.1:5000", map[string]string{"Forwarded": `for=6.6.6.6, for=203.0.113.9;proto=https`}, "203.0.113.9"},
		{"private peer, no headers", "192.168.1.30:5000", nil, "192.168.1.30"},
		{"IPv6 loopback peer", "[::1]:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}, "203.0.113.9"},
		// Behind nginx/Caddy/Traefik the header passes through as the visitor typed it:
		// the address the proxy recorded wins, so a new made-up one per try buys nothing.
		{"local proxy, typed Cf header, real XFF", "172.18.0.9:5000", map[string]string{"Cf-Connecting-Ip": "6.6.6.6", "X-Forwarded-For": "198.51.100.23"}, "198.51.100.23"},
		{"local proxy, typed LAN Cf header, real XFF", "172.18.0.9:5000", map[string]string{"Cf-Connecting-Ip": "192.168.1.5", "X-Forwarded-For": "198.51.100.23"}, "198.51.100.23"},
		{"local proxy, typed Cf header, Forwarded", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "6.6.6.6", "Forwarded": "for=198.51.100.23"}, "198.51.100.23"},
		// cloudflared behind a proxy that records only cloudflared's own address.
		{"tunnel through a local proxy", "172.18.0.9:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9", "X-Forwarded-For": "172.18.0.4"}, "203.0.113.9"},
		// Orange-cloud Cloudflare in front of a local proxy: the nearest internet hop is
		// Cloudflare's edge, which set the header.
		{"Cloudflare edge in front of a local proxy", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9", "X-Forwarded-For": "203.0.113.9, 162.158.1.2"}, "203.0.113.9"},
		{"Cloudflare edge, IPv6", "127.0.0.1:5000", map[string]string{"Cf-Connecting-Ip": "203.0.113.9", "X-Forwarded-For": "2606:4700:10::1"}, "203.0.113.9"},
		{"Cloudflare edge but no Cf header", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "162.158.1.2"}, "162.158.1.2"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := ClientIP(r); got != c.want {
			t.Errorf("%s: ClientIP = %s, want %s", c.name, got, c.want)
		}
	}
}

// ARRMADA_TRUST_CF_HEADER: 1 believes the header from any local proxy (the old rule), 0
// never does.
func TestCloudflareTrustSetting(t *testing.T) {
	defer func(m cfMode) { cfTrust = m }(cfTrust)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.9:5000"
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	r.Header.Set("X-Forwarded-For", "198.51.100.23")
	for _, c := range []struct {
		env, noXFF, withXFF string
	}{
		{"", "203.0.113.9", "198.51.100.23"},
		{"1", "203.0.113.9", "203.0.113.9"},
		{"0", "172.18.0.9", "198.51.100.23"},
	} {
		cfTrust = parseCFTrust(c.env)
		if got := ClientIP(r); got != c.withXFF {
			t.Errorf("ARRMADA_TRUST_CF_HEADER=%q with XFF: %s, want %s", c.env, got, c.withXFF)
		}
		bare := r.Clone(r.Context())
		bare.Header.Del("X-Forwarded-For")
		if got := ClientIP(bare); got != c.noXFF {
			t.Errorf("ARRMADA_TRUST_CF_HEADER=%q without XFF: %s, want %s", c.env, got, c.noXFF)
		}
	}
}

func TestForwardedClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	if ForwardedClientIP(r) != nil {
		t.Error("no forward header → nil")
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if ip := ForwardedClientIP(r); ip == nil || ip.String() != "203.0.113.9" {
		t.Errorf("got %v, want 203.0.113.9", ip)
	}
	// All-private chain: the leftmost, a LAN client behind a local proxy (what the
	// LAN/outside verdict needs).
	r.Header.Set("X-Forwarded-For", "192.168.1.20, 10.0.0.5")
	if ip := ForwardedClientIP(r); ip == nil || ip.String() != "192.168.1.20" {
		t.Errorf("all-private chain: got %v, want 192.168.1.20", ip)
	}
	r.Header.Del("X-Forwarded-For")
	r.Header.Set("Forwarded", `for="192.168.1.5:443";proto=https`)
	if ip := ForwardedClientIP(r); ip == nil || ip.String() != "192.168.1.5" {
		t.Errorf("Forwarded for: got %v, want 192.168.1.5", ip)
	}
	r.Header.Del("Forwarded")
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.RemoteAddr = "203.0.113.50:5000"
	if ip := ForwardedClientIP(r); ip != nil {
		t.Errorf("a public peer's headers were believed: %v", ip)
	}
}
