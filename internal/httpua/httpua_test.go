package httpua

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
)

func withBuild(t *testing.T, version, commit string) {
	t.Helper()
	v, c := buildinfo.Version, buildinfo.Commit
	buildinfo.Version, buildinfo.Commit = version, commit
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit = v, c })
}

func TestUserAgent(t *testing.T) {
	for _, tc := range []struct{ version, commit, want string }{
		{"0.2.0", "abc1234", "Arrmada/0.2.0 (+https://github.com/tristenlammi/Arrmada)"},
		{"0.0.0-dev", "abc1234", "Arrmada/0.0.0-dev+abc1234 (+https://github.com/tristenlammi/Arrmada)"},
		{"0.0.0-dev", "unknown", "Arrmada/0.0.0-dev (+https://github.com/tristenlammi/Arrmada)"},
		{"", "", "Arrmada/0.0.0-dev (+https://github.com/tristenlammi/Arrmada)"},
	} {
		withBuild(t, tc.version, tc.commit)
		if got := UserAgent(); got != tc.want {
			t.Errorf("UserAgent(%q, %q) = %q, want %q", tc.version, tc.commit, got, tc.want)
		}
	}
}

// The transport puts the header on every request, redirects included, leaves a request's
// own User-Agent alone, and never edits the caller's request.
func TestTransport(t *testing.T) {
	withBuild(t, "0.2.0", "abc1234")
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+" "+r.UserAgent())
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusMovedPermanently)
		}
	}))
	defer srv.Close()

	c := Client(0)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if req.Header.Get("User-Agent") != "" {
		t.Error("the caller's request was changed")
	}
	want := UserAgent()
	if len(got) != 2 || got[0] != "/start "+want || got[1] != "/end "+want {
		t.Errorf("requests = %q, want both with %q", got, want)
	}

	got = nil
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/own", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 something")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(got) != 1 || !strings.HasSuffix(got[0], "Mozilla/5.0 something") {
		t.Errorf("own User-Agent replaced: %q", got)
	}

	// Wrapping twice doesn't stack.
	if _, ok := Transport(Transport(nil)).(transport); !ok {
		t.Error("Transport(Transport(nil)) isn't the wrapper")
	}
	if tr := Transport(Transport(nil)).(transport); func() bool { _, ok := tr.next.(transport); return ok }() {
		t.Error("the wrapper wrapped itself")
	}
}

func TestSet(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	Set(req)
	if req.Header.Get("User-Agent") != UserAgent() {
		t.Errorf("Set: %q", req.Header.Get("User-Agent"))
	}
	req.Header.Set("User-Agent", "x")
	Set(req)
	if req.Header.Get("User-Agent") != "x" {
		t.Error("Set replaced an existing User-Agent")
	}
}
