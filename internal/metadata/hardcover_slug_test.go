package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// slugServer fakes Hardcover answering the slug query with body (and status), counting
// the requests it gets.
func slugServer(t *testing.T, status int, body string) (*Hardcover, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		q, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(q), "slug") {
			t.Errorf("unexpected query: %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	h := NewHardcoverFunc(func() string { return "testtoken" }, nil)
	h.endpoint = srv.URL
	return h, &n
}

func TestHardcoverBookSlug(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, body, want string
		wantErr          bool
	}{
		{"found", `{"data":{"books":[{"slug":"dune"}]}}`, "dune", false},
		{"no such book", `{"data":{"books":[]}}`, "", false},
		{"null slug", `{"data":{"books":[{"slug":null}]}}`, "", false},
		{"not a string", `{"data":{"books":[{"slug":42}]}}`, "", false},
		{"not a slug", `{"data":{"books":[{"slug":"../admin"}]}}`, "", false},
		{"refused", `{"errors":[{"message":"field 'slug' not found in type: 'books'"}]}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := slugServer(t, http.StatusOK, c.body)
			got, err := h.BookSlug(ctx, "hc:1245")
			if (err != nil) != c.wantErr || got != c.want {
				t.Errorf("BookSlug = %q, %v; want %q (error %v)", got, err, c.want, c.wantErr)
			}
		})
	}
}

// Reopening a book within a day asks Hardcover once.
func TestHardcoverBookSlugCached(t *testing.T) {
	h, n := slugServer(t, http.StatusOK, `{"data":{"books":[{"slug":"dune"}]}}`)
	for i := 0; i < 3; i++ {
		if got, err := h.BookSlug(context.Background(), "hc:1245"); err != nil || got != "dune" {
			t.Fatalf("call %d: %q, %v", i, got, err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// A refused query is reported once (with the API's words) and not retried on every page
// view; when the field itself is unknown, no book asks again until a restart.
func TestHardcoverBookSlugRefusedIsNotRetried(t *testing.T) {
	h, n := slugServer(t, http.StatusOK, `{"errors":[{"message":"field 'slug' not found in type: 'books'"}]}`)
	_, err := h.BookSlug(context.Background(), "hc:1")
	if err == nil || !strings.Contains(err.Error(), "field 'slug' not found") {
		t.Fatalf("err = %v, want the API's message", err)
	}
	if got, err := h.BookSlug(context.Background(), "hc:1"); got != "" || err != nil {
		t.Errorf("second call = %q, %v; want the remembered empty answer", got, err)
	}
	if got, err := h.BookSlug(context.Background(), "hc:2"); got != "" || err != nil {
		t.Errorf("another book = %q, %v; want no request once the field is known to be refused", got, err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// Some refusals come back as an HTTP 400 carrying the errors list: the message survives.
func TestHardcoverRefusalOn400KeepsTheMessage(t *testing.T) {
	h, _ := slugServer(t, http.StatusBadRequest, `{"errors":[{"message":"unexpected field slug"}]}`)
	if _, err := h.BookSlug(context.Background(), "hc:1"); err == nil || !strings.Contains(err.Error(), "unexpected field slug") {
		t.Errorf("err = %v, want the API's message", err)
	}
}

// A transport failure isn't remembered: the next view tries again.
func TestHardcoverBookSlugServerErrorRetries(t *testing.T) {
	h, n := slugServer(t, http.StatusBadGateway, `bad gateway`)
	for i := 0; i < 2; i++ {
		if _, err := h.BookSlug(context.Background(), "hc:1"); err == nil {
			t.Fatal("want an error")
		}
	}
	if got := n.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestHardcoverBookSlugNeedsAKeyAndABookKey(t *testing.T) {
	h, n := slugServer(t, http.StatusOK, `{"data":{"books":[{"slug":"dune"}]}}`)
	if _, err := h.BookSlug(context.Background(), "hc:a:7"); err == nil {
		t.Error("an author key should be refused")
	}
	h.key = func() string { return "" }
	if got, err := h.BookSlug(context.Background(), "hc:1"); got != "" || err != nil {
		t.Errorf("without a key = %q, %v", got, err)
	}
	if got := n.Load(); got != 0 {
		t.Errorf("requests = %d, want 0", got)
	}

	// Through the switch: only Hardcover keys go to Hardcover.
	s := NewBookSources(nil, &stubBooks{})
	if got, err := s.BookSlug(context.Background(), "/works/OL1W"); got != "" || err != nil {
		t.Errorf("Open Library key = %q, %v", got, err)
	}
}
