package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// uaRecorder wraps a handler and records the User-Agent of every request.
type uaRecorder struct {
	mu   sync.Mutex
	uas  []string
	next http.Handler
}

func (u *uaRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.uas = append(u.uas, r.UserAgent())
	u.mu.Unlock()
	u.next.ServeHTTP(w, r)
}

func (u *uaRecorder) all() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.uas...)
}

// SABnzbd is asked with Arrmada's User-Agent.
func TestSABnzbdSendsUserAgent(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	rec := &uaRecorder{next: f}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	dc.URL = srv.URL
	f.setCats("*", "arrmada")
	if err := newSAB().Test(context.Background(), dc); err != nil {
		t.Fatal(err)
	}
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", File: []byte("<nzb/>"), Category: "arrmada"}); err != nil {
		t.Fatal(err)
	}
	uas := rec.all()
	if len(uas) < 4 {
		t.Fatalf("only %d requests", len(uas))
	}
	for _, ua := range uas {
		if ua != httpua.UserAgent() {
			t.Errorf("SABnzbd saw User-Agent %q, want %q", ua, httpua.UserAgent())
		}
	}
}

// qBittorrent's login and every call after it carry Arrmada's User-Agent.
func TestQBittorrentSendsUserAgent(t *testing.T) {
	var calls atomic.Int32
	rec := &uaRecorder{next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid-1", Path: "/"})
			_, _ = fmt.Fprint(w, "Ok.")
			return
		}
		_, _ = fmt.Fprint(w, "[]")
	})}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	dc := Client{ID: 1, Name: "qb", Kind: KindQbittorrent, URL: srv.URL}
	if _, err := NewQBittorrent().List(context.Background(), dc); err != nil {
		t.Fatal(err)
	}
	uas := rec.all()
	if len(uas) < 2 {
		t.Fatalf("only %d requests", len(uas))
	}
	for _, ua := range uas {
		if ua != httpua.UserAgent() {
			t.Errorf("qBittorrent saw User-Agent %q, want %q", ua, httpua.UserAgent())
		}
	}
}
