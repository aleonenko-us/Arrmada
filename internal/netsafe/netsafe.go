// Package netsafe holds the small rules Arrmada's outbound requests share so they can't
// be turned against the network Arrmada sits on:
//
//   - PublicAddr / PublicTransport: a fetch whose address someone else chose (a cover
//     image URL, a requester's notification link) may only reach the public internet,
//     checked on the address actually dialled, so a redirect or a DNS answer that
//     changes after the first look can't slip through to the LAN or the Docker network.
//   - SameEndpoint: a saved key or password is only ever sent back to the address it was
//     saved for. Changing the address means typing the secret again.
package netsafe

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// blockedPrefixes are the ranges an outside-chosen address may never reach: loopback,
// private and unique-local, link-local (the cloud metadata address 169.254.169.254 among
// them), carrier-grade NAT, "this network", benchmarking, reserved, the old 6to4 relay,
// and every IPv6 form that carries an IPv4 address inside it: IPv4-compatible (::a.b.c.d),
// SIIT (::ffff:0:a.b.c.d), NAT64 (well-known and local-use), 6to4 and Teredo. Those are
// refused outright rather than unwrapped: a public server is never reached that way.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "240.0.0.0/4",
		"::/96", "::ffff:0:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2002::/16",
		"fc00::/7", "fe80::/10", "fec0::/10",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether ip is an ordinary public unicast address.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("") // ::ffff:10.0.0.1 is 10.0.0.1; a zone would hide it from the prefixes
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ErrNotPublic is a refused connection to an address on a private network.
var ErrNotPublic = errors.New("refused to connect to an address on a private network")

// publicDialControl is a net.Dialer Control: it runs on the resolved address just before
// each connection, redirects and every DNS answer included, so what's checked is what's
// dialled.
func publicDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrNotPublic
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !PublicAddr(ip) {
		return ErrNotPublic
	}
	return nil
}

// PublicTransport is an http.Transport that only ever connects to public addresses. It
// ignores HTTP_PROXY and friends on purpose: through a proxy the address check would
// see only the proxy, and the proxy would then fetch whatever it was asked.
func PublicTransport() *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: publicDialControl}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = d.DialContext
	return t
}

// SameEndpoint reports whether two service addresses name the same place: the same
// scheme, user info, host, port and path once normalised (case of the scheme and host,
// a default port written out or not, a trailing slash; the query and fragment don't
// count). A saved key or password is reused only when the address it would go to is
// the SameEndpoint as the one it was saved with. Anything that can't be read as a URL
// is only the same as exactly itself.
func SameEndpoint(a, b string) bool {
	na, okA := normalize(a)
	nb, okB := normalize(b)
	if !okA || !okB {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return na == nb
}

func normalize(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	user := ""
	if u.User != nil {
		user = u.User.String() + "@"
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + user + host + strings.TrimRight(u.EscapedPath(), "/"), true
}
