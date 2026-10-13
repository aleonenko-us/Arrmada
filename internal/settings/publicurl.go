package settings

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// KeyPublicURL is the address people reach Arrmada at from outside the server, e.g.
// https://arrmada.example.com (Settings → System). Alerts link back to the app with it,
// and the calendar feed's link is built from it. Arrmada is served at the root of its
// own hostname or port, so it's an origin: a scheme, a host and an optional port —
// nothing after.
const KeyPublicURL = "app_public_url"

// NormalizePublicURL checks an address typed into Settings and returns it as stored
// ("" clears it). It must be http(s) with a host and nothing past it: no path (sub-path
// hosting isn't supported), no query, no fragment and no user name or password.
func NormalizePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return "", errors.New("the address should look like https://arrmada.example.com")
	}
	if u.User != nil {
		return "", errors.New("the address mustn't contain a user name or password")
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "#") {
		return "", errors.New("give just the address, like https://arrmada.example.com — Arrmada is served at the root, not under a path")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

// PublicURL is the saved outside address, or "" when none is set (or what's stored no
// longer passes the check).
func (s *Service) PublicURL(ctx context.Context) string {
	v, err := NormalizePublicURL(s.Get(ctx, KeyPublicURL, ""))
	if err != nil {
		return ""
	}
	return v
}
