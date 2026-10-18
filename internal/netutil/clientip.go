// Package netutil works out who a request really came from.
//
// Arrmada usually sits behind something: a Cloudflare tunnel (cloudflared in its own
// container, so a private Docker address is the TCP peer) or a reverse proxy on the
// same box. Those stamp the visitor's address into Cf-Connecting-Ip, X-Forwarded-For
// or Forwarded. From anyone else those headers are just text an attacker can set, so
// they're believed only when the request reached us from a loopback or private
// address. Login throttling, the LAN/outside verdict and the audiobook server all ask
// here, so they can't disagree about an address.
package netutil

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// cfTrust is how far Cf-Connecting-Ip is believed (ARRMADA_TRUST_CF_HEADER, read once at
// start): "1" from any local proxy (the old rule), "0" never, anything else the automatic
// rule in ForwardedClientIP.
var cfTrust = parseCFTrust(os.Getenv("ARRMADA_TRUST_CF_HEADER"))

type cfMode int

const (
	cfAuto cfMode = iota
	cfAlways
	cfNever
)

func parseCFTrust(v string) cfMode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return cfAlways
	case "0", "false", "no", "off":
		return cfNever
	}
	return cfAuto
}

// ClientIP is the address to hold a request to (rate limits): the forwarded visitor
// when a local proxy vouches for an internet address, otherwise the TCP peer.
//
// A forwarded private address is not taken. Any machine on the LAN is a "private peer",
// so one that made up a fresh X-Forwarded-For: 10.x.y.z on every sign-in attempt would
// otherwise get a fresh limit each time. LAN clients behind a local proxy share the
// proxy's address instead, which only makes their limit stricter.
func ClientIP(r *http.Request) string {
	if ip := ForwardedClientIP(r); IsPublic(ip) {
		return ip.String()
	}
	host, _ := peer(r)
	return host
}

// ForwardedClientIP returns the original client a local proxy stamped on r, or nil
// when the peer isn't a local proxy or it stamped nothing usable.
//
// X-Forwarded-For is read right to left, taking the first hop that isn't a private
// address: each proxy appends the address it saw, so the right-hand end is what our own
// proxies vouch for and anything further left may be forged by the client (the leftmost
// entry is used only when every hop is private, a LAN client behind a local proxy). Then
// the Forwarded header's for= by the same rule.
//
// Cf-Connecting-Ip no longer comes first. It used to win from any local proxy, but nginx,
// Caddy and Traefik pass it through untouched, so a visitor behind one of them could type
// in any address: a LAN one to look like they were at home, or a new one on every try to
// slip the per-address limits. Now it counts only when
//   - the forwarding chain names no internet address at all: cloudflared talking to us
//     directly, or a local proxy that records only cloudflared's own address. A proxy that
//     records a visitor's real address puts it in the chain, and that wins;
//   - or the nearest internet hop in the chain is Cloudflare's edge (an orange-cloud site
//     in front of a local proxy), which overwrites the header.
//
// The first case isn't proof that Cloudflare set it: a LAN device or container reaching
// the port directly (or rootless Docker, where every peer looks local) can still send the
// header, as it always could X-Forwarded-For. What's closed is the internet visitor behind
// a proxy that records their real address. Cloudflare's edge also adds the visitor to
// X-Forwarded-For, so through a tunnel the two agree. A Cf-Ray header proves nothing: it's
// as easy to type as Cf-Connecting-Ip.
// ARRMADA_TRUST_CF_HEADER=1 brings back believing the header from any local proxy; =0
// never believes it.
func ForwardedClientIP(r *http.Request) net.IP {
	if !FromLocalProxy(r) {
		return nil
	}
	cf := parseHost(r.Header.Get("Cf-Connecting-Ip"))
	if cf != nil && cfTrust == cfAlways {
		return cf
	}
	hop, public := pickHop(splitList(r.Header.Values("X-Forwarded-For")))
	if hop == nil {
		hop, public = pickHop(forwardedFor(r.Header.Values("Forwarded")))
	}
	if cf != nil && cfTrust == cfAuto && (!public || isCloudflare(hop)) {
		return cf
	}
	return hop
}

// FromLocalProxy reports whether r reached us from a loopback or private address — the
// only peers whose forwarding headers (X-Forwarded-Host and friends) are believed.
func FromLocalProxy(r *http.Request) bool {
	_, p := peer(r)
	return p != nil && (p.IsLoopback() || p.IsPrivate())
}

// IsPublic reports whether ip is an internet address: not private, loopback or
// link-local.
func IsPublic(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

// peer is the TCP peer's host and parsed address (nil when it isn't an IP).
func peer(r *http.Request) (string, net.IP) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host, net.ParseIP(host)
}

// pickHop walks hops right to left and returns the first public address (public true),
// or the leftmost parseable one when none is public. Unparseable entries ("unknown",
// obfuscated ids) are skipped.
func pickHop(hops []string) (ip net.IP, public bool) {
	var leftmost net.IP
	for i := len(hops) - 1; i >= 0; i-- {
		ip := parseHost(hops[i])
		if ip == nil {
			continue
		}
		if IsPublic(ip) {
			return ip, true
		}
		leftmost = ip
	}
	return leftmost, false
}

// cloudflareNets are Cloudflare's published edge ranges (cloudflare.com/ips). They change
// rarely, and a range missing here only means an orange-cloud visitor behind a local proxy
// is held to Cloudflare's address instead of their own, never that a typed header is
// believed.
var cloudflareNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}()

// isCloudflare reports whether ip is one of Cloudflare's edge addresses.
func isCloudflare(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range cloudflareNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// splitList flattens comma-separated header values (a header may also repeat).
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// forwardedFor pulls the for= values out of RFC 7239 Forwarded headers, in order.
func forwardedFor(values []string) []string {
	var out []string
	for _, elem := range splitList(values) {
		for _, pair := range strings.Split(elem, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "for") {
				out = append(out, strings.TrimSpace(v))
			}
		}
	}
	return out
}

// parseHost reads an address that may carry quotes, IPv6 brackets or a port:
// 203.0.113.9, "192.168.1.5:443", "[2001:db8::1]:4711", 2001:db8::1.
func parseHost(s string) net.IP {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if s == "" {
		return nil
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return net.ParseIP(h)
	}
	return net.ParseIP(strings.Trim(s, "[]"))
}
