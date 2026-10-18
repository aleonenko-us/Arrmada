package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestReadSABLogin(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "sabnzbd.ini")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	u, p, ok, err := ReadSABLogin(write("[misc]\napi_key = 0123456789abcdef0123456789abcdef\nusername = arrmada\npassword = 5ecret-pw\n[[servers]]\nusername = provider-user\npassword = provider-pw\n"))
	if err != nil || !ok || u != "arrmada" || p != "5ecret-pw" {
		t.Errorf("= %q %q %v %v", u, p, ok, err)
	}
	// A server's login is never taken for the page's.
	if _, _, ok, err := ReadSABLogin(write("[misc]\nusername = \"\"\npassword = \"\"\n[servers]\n[[news]]\nusername = provider-user\npassword = provider-pw\n")); err != nil || ok {
		t.Errorf("no page login: ok %v, %v", ok, err)
	}
	if _, _, _, err := ReadSABLogin(filepath.Join(dir, "missing.ini")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	if key, err := ReadSABAPIKey(write("[misc]\napi_key = 0123456789abcdef0123456789abcdef\n")); err != nil || key != "0123456789abcdef0123456789abcdef" {
		t.Errorf("the API key still reads: %q %v", key, err)
	}
}

// sabLogin is a fake SABnzbd's page login, behind get_config/set_config as SABnzbd 5.1.3
// answers them (checked against the real one): the password comes back masked when set.
type sabLogin struct {
	mu        sync.Mutex
	user      string
	pass      string
	sets      []string
	noMisc    bool // answers without the misc keys
	failOnSet string
}

func (l *sabLogin) wire(f *sabFake) {
	f.setAnswer("get_config", func(form url.Values) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if form.Get("section") != "misc" || l.noMisc {
			return 200, `{"config":{"misc":{"language":"en"}}}`
		}
		masked := ""
		if l.pass != "" {
			masked = "**********"
		}
		b, _ := json.Marshal(map[string]any{"config": map[string]any{"misc": map[string]any{"username": l.user, "password": masked, "language": "en"}}})
		return 200, string(b)
	})
	f.setAnswer("set_config", func(form url.Values) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		k := form.Get("keyword")
		if k == l.failOnSet {
			return 500, "boom"
		}
		l.sets = append(l.sets, k)
		switch k {
		case "username":
			l.user = form.Get("value")
		case "password":
			l.pass = form.Get("value")
		}
		return 200, `{"config":{}}`
	})
}

func TestSecureBundledSAB(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, l *sabLogin) (*Service, Client) {
		t.Helper()
		f, dc := newSABFake(t, "5.1.3")
		l.wire(f)
		svc, _ := newTestService(t)
		if _, err := svc.EnsureBundledSAB(ctx, dc.URL, sabTestKey); err != nil {
			t.Fatal(err)
		}
		return svc, dc
	}

	t.Run("no login: one is given", func(t *testing.T) {
		l := &sabLogin{}
		svc, dc := setup(t, l)
		given, err := svc.SecureBundledSAB(ctx, dc.URL)
		if err != nil || !given {
			t.Fatalf("= %v, %v", given, err)
		}
		if l.user != sabWebUser || len(l.pass) < 32 || strings.Join(l.sets, ",") != "username,password" {
			t.Errorf("login = %q / %d chars, sets %v", l.user, len(l.pass), l.sets)
		}
		// Again: it has one now.
		if given, err := svc.SecureBundledSAB(ctx, dc.URL); err != nil || given || len(l.sets) != 2 {
			t.Errorf("again = %v, %v (sets %v)", given, err, l.sets)
		}
	})
	t.Run("cut short after the username: finished", func(t *testing.T) {
		l := &sabLogin{user: sabWebUser}
		svc, dc := setup(t, l)
		if given, err := svc.SecureBundledSAB(ctx, dc.URL); err != nil || !given || l.pass == "" {
			t.Errorf("= %v, %v (pass set %v)", given, err, l.pass != "")
		}
	})
	for name, l := range map[string]*sabLogin{
		"the owner's login":                 {user: "me", pass: "owners"},
		"a password only":                   {pass: "owners"},
		"the owner's username, no password": {user: "me"},
	} {
		t.Run(name+": left alone", func(t *testing.T) {
			svc, dc := setup(t, l)
			if given, err := svc.SecureBundledSAB(ctx, dc.URL); err != nil || given || len(l.sets) != 0 {
				t.Errorf("= %v, %v (sets %v)", given, err, l.sets)
			}
		})
	}
	t.Run("an answer without the login settings: nothing guessed", func(t *testing.T) {
		l := &sabLogin{noMisc: true}
		svc, dc := setup(t, l)
		if _, err := svc.SecureBundledSAB(ctx, dc.URL); err == nil || len(l.sets) != 0 {
			t.Errorf("err %v, sets %v", err, l.sets)
		}
	})
	t.Run("a failed set never carries the password", func(t *testing.T) {
		l := &sabLogin{failOnSet: "password"}
		svc, dc := setup(t, l)
		_, err := svc.SecureBundledSAB(ctx, dc.URL)
		if err == nil || strings.Contains(err.Error(), "value=") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not the bundled container", func(t *testing.T) {
		l := &sabLogin{}
		svc, dc := setup(t, l)
		if _, err := svc.SecureBundledSAB(ctx, "http://arrmada-sabnzbd:8080"); !errors.Is(err, ErrBundledInactive) {
			t.Errorf("another address = %v", err)
		}
		c, _, _ := svc.BundledClient(ctx, KindSABnzbd)
		c.Enabled = false
		if _, err := svc.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SecureBundledSAB(ctx, dc.URL); !errors.Is(err, ErrBundledInactive) || len(l.sets) != 0 {
			t.Errorf("switched off = %v (sets %v)", err, l.sets)
		}
	})
}
