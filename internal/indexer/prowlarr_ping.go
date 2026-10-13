package indexer

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// prowlarrPingTimeout bounds the reachability check: the setup wizard waits on it.
const prowlarrPingTimeout = 3 * time.Second

// ProwlarrReachable reports whether a Prowlarr answers at base, by its unauthenticated
// /ping. It says nothing about the API key — the sync checks that — only that the setup
// wizard can offer a one-click sync instead of asking for a tracker. Only http(s)
// addresses are tried.
func ProwlarrReachable(ctx context.Context, base string) bool {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, prowlarrPingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ping", nil)
	if err != nil {
		return false
	}
	// No redirects: a /ping that sends us elsewhere isn't Prowlarr answering.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}
