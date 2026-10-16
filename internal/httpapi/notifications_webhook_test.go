package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/notify"
)

const (
	n8nTestSecret = "Zq3Zf1y7VJ0cK6m2Xr9T4bNw8LhPdUa5EsGiYoCjRkM"
	n8nTestHeader = "header-value-Pq81Lm"
	n8nTestPath   = "/webhook/arrmada-path-9fz"
)

// n8nCatcher is a stand-in n8n Webhook node: it keeps every body it's sent.
type n8nCatcher struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	heads  []http.Header
}

func newN8NCatcher(t *testing.T) *n8nCatcher {
	t.Helper()
	c := &n8nCatcher{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		c.mu.Lock()
		c.bodies, c.heads = append(c.bodies, m), append(c.heads, r.Header.Clone())
		c.mu.Unlock()
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *n8nCatcher) got() ([]map[string]any, []http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.bodies...), append([]http.Header(nil), c.heads...)
}

// An n8n connection is made from its fields; the URL, the header value and the signing
// secret never come back out — not in the create answer, not in the list (to an admin or a
// manager) — and the URL's hint shows the server only.
func TestN8NConnectionSecretsNeverReturned(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	hook := newN8NCatcher(t)

	body := `{"name":"Home n8n","kind":"n8n","enabled":true,"events":["movie.imported"],"fields":{"url":"` + hook.URL + n8nTestPath +
		`","auth":"header","header_name":"X-Arrmada-Key","header_value":"` + n8nTestHeader + `","signing_secret":"` + n8nTestSecret + `"}}`
	rec := s.doJSON("POST", "/api/v1/notifications", admin, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: HTTP %d %s", rec.Code, rec.Body)
	}
	answers := []string{rec.Body.String()}
	for _, who := range []*http.Cookie{admin, mgr} {
		list := listNotifications(t, s, who)
		raw, _ := json.Marshal(list)
		answers = append(answers, string(raw))
		fields, _ := list[0]["fields"].(map[string]any)
		if list[0]["preset"] != "n8n" || fields["header_name"] != "X-Arrmada-Key" || fields["auth"] != "header" ||
			fields["signing_secret_set"] != true || fields["header_value_set"] != true || fields["url_set"] != true {
			t.Fatalf("list entry = %s", raw)
		}
		if hint, _ := list[0]["url_hint"].(string); !strings.HasPrefix(hint, "http://127.0.0.1:") || !strings.HasSuffix(hint, "/••••") {
			t.Fatalf("url_hint = %q", hint)
		}
		if list[0]["invalid_reason"] != nil {
			t.Fatalf("a webhook URL isn't an Apprise link and shouldn't be flagged: %v", list[0]["invalid_reason"])
		}
	}
	for _, a := range answers {
		for _, secret := range []string{n8nTestPath, "arrmada-path-9fz", n8nTestHeader, n8nTestSecret} {
			if strings.Contains(a, secret) {
				t.Fatalf("an answer carries %q: %s", secret, a)
			}
		}
	}

	// Renaming without fields keeps everything; the saved Test signs with the stored secret.
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"n8n","enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/notifications/1/test", admin, `{"event":"movie.imported"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("saved test: HTTP %d %s", rec.Code, rec.Body)
	}
	bodies, heads := hook.got()
	if len(bodies) != 1 || bodies[0]["event"] != "movie.imported" || bodies[0]["test"] != true || heads[0].Get("X-Arrmada-Key") != n8nTestHeader {
		t.Fatalf("n8n got %v %v", bodies, heads)
	}
	// An edit that leaves the secrets blank keeps them (and switching auth drops the old header).
	rec = s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"Renamed","kind":"n8n","enabled":true,"fields":{"url":"","auth":"basic","username":"arr","password":"pw-123","signing_secret":""}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch to basic: HTTP %d %s", rec.Code, rec.Body)
	}
	c, _ := s.deps.Notify.Get(context.Background(), 1)
	f := notify.PresetFields(c)
	if f["signing_secret"] != n8nTestSecret || f["url"] != hook.URL+n8nTestPath || f["header_value"] != "" || f["username"] != "arr" || c.URL != hook.URL+n8nTestPath {
		t.Fatalf("after the switch: %q %v", c.URL, f)
	}
}

// An n8n connection takes its fields, never a bare link; its fields are checked on save,
// and Test takes only catalog events.
func TestN8NConnectionRefusals(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	for name, body := range map[string]string{
		"a bare link":   `{"name":"x","kind":"n8n","url":"json://192.168.1.10/hook"}`,
		"no secret":     `{"name":"x","kind":"n8n","fields":{"url":"http://192.168.1.10:5678/webhook/a"}}`,
		"short secret":  `{"name":"x","kind":"n8n","fields":{"url":"http://192.168.1.10:5678/webhook/a","signing_secret":"abc"}}`,
		"not http":      `{"name":"x","kind":"n8n","fields":{"url":"gotify://h/t","signing_secret":"` + n8nTestSecret + `"}}`,
		"creds in url":  `{"name":"x","kind":"n8n","fields":{"url":"http://u:p@h/x","signing_secret":"` + n8nTestSecret + `"}}`,
		"header in use": `{"name":"x","kind":"n8n","fields":{"url":"http://h/x","auth":"header","header_name":"X-Arrmada-Signature","header_value":"v","signing_secret":"` + n8nTestSecret + `"}}`,
	} {
		if rec := s.doJSON("POST", "/api/v1/notifications", admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d %s", name, rec.Code, rec.Body)
		}
	}
	// The LAN and this machine are fine for an admin's webhook (that's where n8n lives).
	for _, u := range []string{"http://192.168.1.10:5678/webhook/a", "http://localhost:5678/webhook/a", "http://n8n:5678/webhook/a"} {
		rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"x","kind":"n8n","fields":{"url":"`+u+`","signing_secret":"`+n8nTestSecret+`"}}`)
		if rec.Code != http.StatusCreated {
			t.Errorf("%s refused: HTTP %d %s", u, rec.Code, rec.Body)
		}
	}
	// A saved n8n connection can't be turned into an Apprise link by a raw URL, nor an
	// Apprise one into a webhook without its fields.
	if rec := s.doJSON("PUT", "/api/v1/notifications/1", admin, `{"name":"x","kind":"n8n","url":"json://h/x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("n8n with a new link: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/notifications", admin, `{"name":"d","url":"discord://1/2","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("discord: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("PUT", "/api/v1/notifications/4", admin, `{"name":"d","kind":"n8n"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("Apprise → n8n without fields: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/notifications/1/test", admin, `{"event":"no.such.event"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown test event: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("POST", "/api/v1/notifications/1/test", admin, `{"event":"movie.imported","url":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field in a test body: HTTP %d", rec.Code)
	}
}

// Testing an unsaved n8n form sends the chosen event's sample to the typed URL, signed with
// the typed secret — what n8n's "Listen for test event" needs — and the answer never
// quotes the URL's path when it fails.
func TestN8NUnsavedTestSendsSample(t *testing.T) {
	s, _ := newNotifyServer(t)
	_, admin := s.user(t, "admin@example.com", auth.RoleAdmin)
	hook := newN8NCatcher(t)
	body := `{"name":"x","kind":"n8n","event":"request.created","fields":{"url":"` + hook.URL + `/webhook-test/abc","signing_secret":"` + n8nTestSecret + `"}}`
	rec := s.doJSON("POST", "/api/v1/notifications/test", admin, body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test: HTTP %d %s", rec.Code, rec.Body)
	}
	bodies, heads := hook.got()
	if len(bodies) != 1 || bodies[0]["event"] != "request.created" || heads[0].Get("X-Arrmada-Signature") == "" {
		t.Fatalf("n8n got %v", bodies)
	}
	data, _ := bodies[0]["data"].(map[string]any)
	if data["title"] != "Dune" || data["requested_by"] != "Sam" {
		t.Fatalf("sample data = %v", data)
	}

	// Nothing there: a plain refusal that names the server, not the secret path.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	rec = s.doJSON("POST", "/api/v1/notifications/test", admin, `{"name":"x","kind":"n8n","fields":{"url":"`+url+`/webhook/s3cret-path-zz","signing_secret":"`+n8nTestSecret+`"}}`)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || strings.Contains(rec.Body.String(), "s3cret-path-zz") {
		t.Fatalf("dead test: %s", rec.Body)
	}
}
