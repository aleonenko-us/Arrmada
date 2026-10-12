package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/mail/mailtest"
)

// lockedBuf is a log sink safe to write from background work.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// newMailServer is a route server with the key store and a log the test can read.
func newMailServer(t *testing.T, tweak func(*Deps)) (*routeServer, *lockedBuf) {
	t.Helper()
	logs := &lockedBuf{}
	s := newRouteServer(t, func(d *Deps) {
		d.Log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		d.APIKeys = apikeys.NewStore(d.Settings)
		if tweak != nil {
			tweak(d)
		}
	})
	return s, logs
}

func startMailFake(t *testing.T, o mailtest.Options) *mailtest.Server {
	t.Helper()
	fake, err := mailtest.Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	return fake
}

// saveSMTP points the saved setup at the fake server (no TLS: it's on 127.0.0.1).
func saveSMTP(t *testing.T, s *routeServer, c *http.Cookie, fake *mailtest.Server, user, pass string) {
	t.Helper()
	body := fmt.Sprintf(`{"host":%q,"port":%d,"security":"none","username":%q,"password":%q,"from":"Arrmada <books@example.com>"}`,
		fake.Host, fake.Port, user, pass)
	if rec := s.doJSON("PUT", "/api/v1/settings/smtp", c, body); rec.Code != http.StatusOK {
		t.Fatalf("save SMTP: HTTP %d %s", rec.Code, rec.Body)
	}
}

// BOOK-23: the owner saves the mail setup; reading it back never returns the server, the
// login or the password, and no log line carries them. The Test button sends through it,
// and a wrong password shows the server's own reply.
func TestSMTPSettingsSaveTestAndRedact(t *testing.T) {
	s, logs := newMailServer(t, nil)
	_, admin := s.user(t, "admin-one", auth.RoleAdmin)
	fake := startMailFake(t, mailtest.Options{User: "mailbox-login-xyz", Pass: "sup3r-s3cret-pw"})
	saveSMTP(t, s, admin, fake, "mailbox-login-xyz", "sup3r-s3cret-pw")

	rec := s.do("GET", "/api/v1/settings/smtp", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: HTTP %d", rec.Code)
	}
	for _, secret := range []string{"sup3r-s3cret-pw", "mailbox-login-xyz", fake.Host} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("GET /settings/smtp returns %q in clear: %s", secret, rec.Body)
		}
	}
	var v smtpView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Configured || !v.HostSet || !v.UsernameSet || !v.PasswordSet || v.Port != fake.Port || v.Security != "none" || v.From != "Arrmada <books@example.com>" {
		t.Errorf("view = %+v", v)
	}
	// The API keys list doesn't show the mail credentials, and its routes can't touch them.
	if rec := s.do("GET", "/api/v1/apikeys", admin); strings.Contains(rec.Body.String(), "smtp") {
		t.Errorf("API keys list mentions the mail login: %s", rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/apikeys/smtp_password", admin, `{"value":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("PUT /apikeys/smtp_password: HTTP %d, want 404", rec.Code)
	}
	if rec := s.do("DELETE", "/api/v1/apikeys/smtp_host", admin); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE /apikeys/smtp_host: HTTP %d, want 404", rec.Code)
	}

	// Test: delivered.
	rec = s.doJSON("POST", "/api/v1/settings/smtp/test", admin, `{"to":"owner@example.com"}`)
	var res struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil || !res.OK {
		t.Fatalf("test send: HTTP %d %s", rec.Code, rec.Body)
	}
	msgs := fake.Messages()
	if len(msgs) != 1 || msgs[0].To[0] != "owner@example.com" || !strings.Contains(msgs[0].Data, "Arrmada test email") || msgs[0].User != "mailbox-login-xyz" {
		t.Fatalf("fake server got %+v", msgs)
	}

	// A blank password on save keeps the saved one; a wrong one shows the server's reply.
	if rec := s.doJSON("PUT", "/api/v1/settings/smtp", admin, `{"from":"books@example.com"}`); rec.Code != http.StatusOK {
		t.Fatalf("partial save: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/settings/smtp/test", admin, `{"to":"owner@example.com"}`); !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("a save without the password must keep it: %s", rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/settings/smtp", admin, `{"password":"wrong-password-123"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	rec = s.doJSON("POST", "/api/v1/settings/smtp/test", admin, `{"to":"owner@example.com"}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.OK || !strings.Contains(res.Detail, "535") {
		t.Errorf("wrong password: %s, want ok=false with the 535 reply", rec.Body)
	}

	for _, secret := range []string{"sup3r-s3cret-pw", "wrong-password-123", "mailbox-login-xyz", fake.Host + ":", "owner@example.com"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a log line carries %q:\n%s", secret, logs)
		}
	}
}

// Bad input is refused before anything is saved, and the whole setup can be removed.
func TestSMTPSettingsValidateAndClear(t *testing.T) {
	s, _ := newMailServer(t, nil)
	_, admin := s.user(t, "owner@example.com", auth.RoleAdmin)
	for _, body := range []string{
		`{"host":"smtps://smtp.example.com"}`,
		`{"host":"smtp.example.com:587"}`,
		`{"security":"ssl"}`,
		`{"port":70000}`,
		`{"from":"not an address"}`,
		`{"username":"a\r\nb"}`,
		`{"remove_login":true,"password":"x"}`,
	} {
		if rec := s.doJSON("PUT", "/api/v1/settings/smtp", admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: HTTP %d, want 400", body, rec.Code)
		}
	}
	if rec := s.doJSON("POST", "/api/v1/settings/smtp/test", admin, `{"to":"owner@example.com"}`); rec.Code != http.StatusConflict {
		t.Errorf("test with nothing saved: HTTP %d, want 409", rec.Code)
	}
	if rec := s.doJSON("PUT", "/api/v1/settings/smtp", admin, `{"host":"smtp.example.com","username":"u","password":"p","from":"a@example.com"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/settings/smtp", admin, `{"remove_login":true}`); !strings.Contains(rec.Body.String(), `"username_set":false`) || !strings.Contains(rec.Body.String(), `"password_set":false`) {
		t.Errorf("remove login: %s", rec.Body)
	}
	rec := s.do("DELETE", "/api/v1/settings/smtp", admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":false`) || !strings.Contains(rec.Body.String(), `"host_set":false`) {
		t.Errorf("clear: HTTP %d %s", rec.Code, rec.Body)
	}
}

// Managers and requesters can't read or change the mail login.
func TestSMTPSettingsAreAdminOnly(t *testing.T) {
	s, _ := newMailServer(t, nil)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, rq := s.user(t, "rq@example.com", auth.RoleRequester)
	for _, c := range []*http.Cookie{mgr, rq} {
		if rec := s.do("GET", "/api/v1/settings/smtp", c); rec.Code != http.StatusForbidden {
			t.Errorf("GET: HTTP %d, want 403", rec.Code)
		}
		if rec := s.doJSON("PUT", "/api/v1/settings/smtp", c, `{"host":"smtp.example.com"}`); rec.Code != http.StatusForbidden {
			t.Errorf("PUT: HTTP %d, want 403", rec.Code)
		}
	}
}

func TestMaskHint(t *testing.T) {
	for in, want := range map[string]string{"": "", "short": "…", "smtp.gmail.com": "sm….com"} {
		if got := maskHint(in); got != want {
			t.Errorf("maskHint(%q) = %q, want %q", in, got, want)
		}
	}
}
