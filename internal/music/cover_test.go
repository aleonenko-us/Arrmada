package music

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	relMBID = "0a8e97fd-4566-4a8b-a6bc-5bb0e9d1a7f2"
	rgMBID  = "3c1d6b4e-8d0c-4a59-9e57-6f3e4f1d2a10"
)

func TestCAAFront(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/release/" + relMBID + "/front-500":
			http.NotFound(w, r) // this release has no art of its own
		case "/release-group/" + rgMBID + "/front-500":
			_, _ = w.Write(jpeg)
		case "/release/" + relMBID + "/front-1200":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/release-group/" + rgMBID + "/front":
			_, _ = w.Write([]byte("<html>not an image</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &CAA{Base: srv.URL, Client: srv.Client()}
	ctx := context.Background()

	// A release with no art falls back to its release group's.
	if b, err := c.Front(ctx, relMBID, rgMBID, 500); err != nil || string(b) != string(jpeg) {
		t.Fatalf("500: %v %v", b, err)
	}
	// An outage isn't "no cover": no fallback, and the error says so.
	calls.Store(0)
	if _, err := c.Front(ctx, relMBID, rgMBID, 1200); err == nil || errors.Is(err, ErrNoCoverArt) || calls.Load() != 1 {
		t.Errorf("1200: %v after %d calls", err, calls.Load())
	}
	// Only images come back.
	if _, err := c.Front(ctx, "", rgMBID, 0); err == nil || !strings.Contains(err.Error(), "not a JPEG") {
		t.Errorf("html: %v", err)
	}
	// No ids, or ids that aren't MusicBrainz's, ask nothing.
	calls.Store(0)
	if _, err := c.Front(ctx, "deezer:1", "", 500); !errors.Is(err, ErrNoCoverArt) || calls.Load() != 0 {
		t.Errorf("no ids: %v after %d calls", err, calls.Load())
	}
	if !IsJPEG(jpeg) || IsJPEG([]byte("\x89PNG")) {
		t.Error("IsJPEG")
	}
}
