package download

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Against a real, throwaway SABnzbd whose page has no login yet, as the first Compose
// config left it (ARRMADA_SAB_LOGIN_IT=<its URL>, ARRMADA_SAB_LOGIN_KEY=<its API key>):
// EnsureWebLogin gives it one, its page then asks for it, the API key still works, and a
// second run changes nothing.
func TestSABnzbdEnsureWebLoginLive(t *testing.T) {
	base, key := os.Getenv("ARRMADA_SAB_LOGIN_IT"), os.Getenv("ARRMADA_SAB_LOGIN_KEY")
	if base == "" || key == "" {
		t.Skip("set ARRMADA_SAB_LOGIN_IT and ARRMADA_SAB_LOGIN_KEY to a throwaway SABnzbd with no page login to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dc := Client{ID: 1, Kind: KindSABnzbd, URL: base, Password: key}
	sab := NewSABnzbd()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := func() string {
		t.Helper()
		resp, err := noRedirect.Get(strings.TrimRight(base, "/") + "/")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.Header.Get("Location")
	}
	if loc := page(); strings.Contains(loc, "login") {
		t.Fatalf("precondition: the page already asks for a login (%s)", loc)
	}
	given, err := sab.EnsureWebLogin(ctx, dc)
	if err != nil || !given {
		t.Fatalf("EnsureWebLogin = %v, %v", given, err)
	}
	if loc := page(); !strings.Contains(loc, "/login") {
		t.Errorf("the page doesn't ask for the login (Location %q)", loc)
	}
	if _, err := sab.Categories(ctx, dc); err != nil {
		t.Errorf("the API key stopped working: %v", err)
	}
	if given, err := sab.EnsureWebLogin(ctx, dc); err != nil || given {
		t.Errorf("second run = %v, %v", given, err)
	}
}
