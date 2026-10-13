package indexer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The wizard offers a one-click sync only when a Prowlarr really answers its /ping.
func TestProwlarrReachable(t *testing.T) {
	ctx := context.Background()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"status":"OK"}`)
	}))
	defer up.Close()
	if !ProwlarrReachable(ctx, up.URL+"/") {
		t.Error("a Prowlarr answering /ping reads as unreachable")
	}

	notProwlarr := httptest.NewServer(http.NotFoundHandler())
	defer notProwlarr.Close()
	if ProwlarrReachable(ctx, notProwlarr.URL) {
		t.Error("a server with no /ping reads as Prowlarr")
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	for _, base := range []string{deadURL, "", "ftp://example.invalid", "not a url"} {
		if ProwlarrReachable(ctx, base) {
			t.Errorf("%q reads as reachable", base)
		}
	}
}
