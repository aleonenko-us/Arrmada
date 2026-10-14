package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
)

// fakeJellyfin stands in for a Jellyfin server's sign-in calls: password sign-in, Quick
// Connect and logout. It records what it was sent, so tests can check Arrmada never
// presents a key on a sign-in call and logs every session it opened out again.
type fakeJellyfin struct {
	mu        sync.Mutex
	srv       *httptest.Server
	users     map[string]fakeJFUser // lower-case name → user
	qcOff     bool
	nextQC    int
	qc        map[string]*fakeQC // secret → request
	issued    map[string]bool    // token → still valid (false once logged out)
	authCalls int                // AuthenticateByName calls
	qcAuths   int                // AuthenticateWithQuickConnect calls
	keyedAuth int                // sign-in calls that carried a Token
	failWith  int                // answer every AuthenticateByName with this status (0 = normal)
	nextToken int
}

type fakeJFUser struct {
	pw   string
	user jellyfin.User
}

type fakeQC struct {
	code     string
	approved string // lower-case user name once approved
}

func newFakeJellyfin(t *testing.T) *fakeJellyfin {
	t.Helper()
	f := &fakeJellyfin{users: map[string]fakeJFUser{}, qc: map[string]*fakeQC{}, issued: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJellyfin) user(name, pw, id string, admin, disabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[strings.ToLower(name)] = fakeJFUser{pw: pw, user: jellyfin.User{ID: id, Name: name, Policy: jellyfin.UserPolicy{IsAdministrator: admin, IsDisabled: disabled}}}
}

// approve is someone typing the code into a Jellyfin app signed in as name.
func (f *fakeJellyfin) approve(code, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.qc {
		if q.code == code {
			q.approved = strings.ToLower(name)
			return true
		}
	}
	return false
}

// live is how many tokens it issued that were never logged out.
func (f *fakeJellyfin) live() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, ok := range f.issued {
		if ok {
			n++
		}
	}
	return n
}

func (f *fakeJellyfin) counts() (auths, qcAuths, keyed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authCalls, f.qcAuths, f.keyedAuth
}

func (f *fakeJellyfin) secrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for s := range f.qc {
		out = append(out, s)
	}
	return out
}

// token pulls Token="…" out of the MediaBrowser header.
func jfHeaderToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	i := strings.Index(h, `Token="`)
	if i < 0 {
		return ""
	}
	h = h[i+len(`Token="`):]
	return h[:strings.Index(h, `"`)]
}

func (f *fakeJellyfin) signedIn(u jellyfin.User) map[string]any {
	f.nextToken++
	tok := "jf-token-" + strconv.Itoa(f.nextToken)
	f.issued[tok] = true
	return map[string]any{"User": u, "AccessToken": tok, "ServerId": "srv1"}
}

func (f *fakeJellyfin) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	tok := jfHeaderToken(r)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/Users/AuthenticateByName":
		f.authCalls++
		if tok != "" {
			f.keyedAuth++
		}
		raw, _ := io.ReadAll(r.Body)
		if f.failWith != 0 {
			// An unhelpful server that echoes the request in its error.
			w.WriteHeader(f.failWith)
			_, _ = w.Write(raw)
			return
		}
		var body struct{ Username, Pw string }
		_ = json.Unmarshal(raw, &body)
		u, ok := f.users[strings.ToLower(body.Username)]
		if !ok || u.pw != body.Pw {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Error processing request."))
			return
		}
		_ = json.NewEncoder(w).Encode(f.signedIn(u.user))
	case r.Method == http.MethodPost && r.URL.Path == "/QuickConnect/Initiate":
		if tok != "" {
			f.keyedAuth++
		}
		if f.qcOff {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.nextQC++
		secret := "qc-secret-" + strconv.Itoa(f.nextQC) + "-abcdef"
		code := strconv.Itoa(100000 + f.nextQC)
		f.qc[secret] = &fakeQC{code: code}
		_ = json.NewEncoder(w).Encode(map[string]any{"Secret": secret, "Code": code, "Authenticated": false})
	case r.Method == http.MethodGet && r.URL.Path == "/QuickConnect/Connect":
		q, ok := f.qc[r.URL.Query().Get("secret")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": q.code, "Authenticated": q.approved != ""})
	case r.Method == http.MethodPost && r.URL.Path == "/Users/AuthenticateWithQuickConnect":
		f.qcAuths++
		if tok != "" {
			f.keyedAuth++
		}
		var body struct{ Secret string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		q, ok := f.qc[body.Secret]
		if !ok || q.approved == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Like 12.2: an approved secret keeps signing in until it expires.
		_ = json.NewEncoder(w).Encode(f.signedIn(f.users[q.approved].user))
	case r.Method == http.MethodPost && r.URL.Path == "/Sessions/Logout":
		if !f.issued[tok] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.issued[tok] = false
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// jellyfinLoginServer is a route server connected to a fake Jellyfin with sign-in on and
// two users: kid (a plain user) and boss (a Jellyfin administrator). Its log is kept.
func jellyfinLoginServer(t *testing.T) (*routeServer, *fakeJellyfin, *bytes.Buffer) {
	t.Helper()
	f := newFakeJellyfin(t)
	f.user("kid", "kid-password-1", "jf-kid", false, false)
	f.user("boss", "boss-password-1", "jf-boss", true, false)
	var logs lockedBuffer
	s := newRouteServer(t, func(d *Deps) {
		d.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		d.Jellyfin = jellyfinsvc.New(jellyfinsvc.Options{Settings: d.Settings, Version: "test", Log: d.Log})
	})
	ctx := context.Background()
	_ = s.deps.Settings.Set(ctx, jellyfinsvc.KeyURL, f.srv.URL)
	_ = s.deps.Settings.SetBool(ctx, keyJellyfinLoginEnabled, true)
	return s, f, &logs.buf
}

// lockedBuffer is a log sink that handlers can write to concurrently.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// doFrom sends a request from ip carrying the cookies given.
func (s *routeServer) doFrom(method, path, body, ip string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://arrmada.local"+path, strings.NewReader(body))
	r.RemoteAddr = ip + ":5000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

// cookieNamed is the response's cookie called name, or nil.
func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}
