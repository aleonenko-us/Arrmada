package indexer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ProwlarrStatus sends the key as a header (never in the URL), returns the version, says
// a refused key plainly, and asks nothing when the URL or key is missing.
func TestProwlarrStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/status" || r.URL.RawQuery != "" {
			t.Errorf("asked %s", r.URL)
		}
		if r.Header.Get("X-Api-Key") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"appName":"Prowlarr","version":"1.24.3.4754"}`))
	}))
	defer srv.Close()
	ctx := context.Background()

	if v, err := ProwlarrStatus(ctx, srv.URL+"/", "good"); err != nil || v != "1.24.3.4754" {
		t.Errorf("good key: %q, %v", v, err)
	}
	if _, err := ProwlarrStatus(ctx, srv.URL, "bad"); err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("bad key: %v", err)
	}
	if _, err := ProwlarrStatus(ctx, "", "good"); !errors.Is(err, ErrProwlarrNoURL) {
		t.Errorf("no URL: %v", err)
	}
	if _, err := ProwlarrStatus(ctx, srv.URL, ""); !errors.Is(err, ErrProwlarrNoKey) {
		t.Errorf("no key: %v", err)
	}
}
