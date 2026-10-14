package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A typed Hardcover key is tested with one "me" request carrying that key (Bearer added),
// never the saved one; a rejected key is an error that doesn't repeat the key.
func TestHardcoverVerifyKeyTestsTheTypedKey(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "me {") {
			t.Errorf("unexpected query %s", body)
		}
		if r.Header.Get("Authorization") != "Bearer typed-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"me":[{"username":"reader"}]}}`)
	}))
	defer srv.Close()
	h := NewHardcoverFunc(func() string { return "saved" }, nil)
	h.endpoint = srv.URL
	ctx := context.Background()

	got, err := h.VerifyKey(ctx, "  typed-good ")
	if err != nil || got != "OK: Hardcover accepted the key (signed in as reader)." {
		t.Errorf("good typed key: %q, %v", got, err)
	}
	if len(auths) != 1 || auths[0] != "Bearer typed-good" {
		t.Errorf("requests carried %q, want one with the typed key", auths)
	}

	_, err = h.VerifyKey(ctx, "Bearer typed-bad-secret")
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("bad typed key: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "typed-bad-secret") {
		t.Errorf("the error repeats the key: %v", err)
	}
	if h.key() != "saved" {
		t.Error("testing a typed key changed the saved one")
	}
}
