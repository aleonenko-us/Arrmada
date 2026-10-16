package notify

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// A webhook URL pointing at something that isn't HTTP (an SSH port, say) is refused in
// fixed words: what that service sent back — its banner — never reaches the Test answer
// or the delivery log.
func TestWebhookNonHTTPReplyNotEchoed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Speak first like SSH does, then read the request so the client sees the reply.
			_, _ = conn.Write([]byte("SSH-2.0-SECRETBANNER_OpenSSH_9.6\r\n"))
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
		}
	}()

	s, _, _ := newTestService(t)
	c := mustCreate(t, s, webhookConn("http://"+ln.Addr().String()+"/webhook/x", nil, "release.grabbed"))
	err = s.Test(context.Background(), c, "")
	if err == nil {
		t.Fatal("a non-HTTP reply counted as delivered")
	}
	if strings.Contains(err.Error(), "SECRETBANNER") || strings.Contains(err.Error(), "SSH-2.0") {
		t.Fatalf("the error echoes the service's banner: %v", err)
	}
	if !strings.Contains(err.Error(), "isn't HTTP") && !strings.Contains(err.Error(), "closed the connection") {
		t.Fatalf("the error doesn't say what's wrong: %v", err)
	}
	list, _ := s.Deliveries(context.Background(), c.ID, 5)
	if len(list) != 1 || strings.Contains(list[0].LastError, "SECRETBANNER") {
		t.Fatalf("delivery log = %+v", list)
	}
}

// A saved header value or password only fills in for the server it was saved for. A path
// change on the same server (n8n's test URL → production URL) keeps them; another host,
// port or scheme doesn't, and a form that relies on one is asked for it again. The
// signing secret always carries over.
func TestWebhookKeptOnlyForTheSameServer(t *testing.T) {
	stored := map[string]string{
		"url": "http://192.168.1.10:5678/webhook-test/abc", "auth": "header", "header_name": "X-Key",
		"header_value": "hv-saved", "signing_secret": testSecret,
	}
	form := func(url, auth string) map[string]string {
		return map[string]string{"url": url, "auth": auth, "header_name": "X-Key", "header_value": "", "signing_secret": ""}
	}

	for _, same := range []string{
		"http://192.168.1.10:5678/webhook/abc", "HTTP://192.168.1.10:5678/other", "",
	} {
		kept, err := WebhookKept(form(same, "header"), stored)
		if err != nil || kept["header_value"] != "hv-saved" || kept["signing_secret"] != testSecret {
			t.Errorf("%q: kept %v, %v", same, kept, err)
		}
	}
	if kept, err := WebhookKept(form("http://n8n.local/webhook/abc", "header"), map[string]string{"url": "http://n8n.local:80/x", "header_value": "v"}); err != nil || kept["header_value"] != "v" {
		t.Errorf("default port: %v %v", kept, err)
	}

	for _, other := range []string{
		"http://192.168.1.99:5678/webhook/abc", "http://192.168.1.10:5679/webhook/abc", "https://192.168.1.10:5678/webhook/abc",
		"http://attacker.example/collect",
	} {
		_, err := WebhookKept(form(other, "header"), stored)
		fe, ok := err.(*FieldError)
		if !ok || fe.Field != "header_value" || strings.Contains(fe.Msg, "hv-saved") {
			t.Errorf("%q: header value not asked for again: %v", other, err)
		}
		// Typed again: fine, and nothing saved goes with it but the signing secret.
		f := form(other, "header")
		f["header_value"] = "typed-now"
		kept, err := WebhookKept(f, stored)
		if err != nil || kept["header_value"] != "" || kept["signing_secret"] != testSecret {
			t.Errorf("%q typed again: %v %v", other, kept, err)
		}
		merged := MergeSecrets(KindN8N, f, kept)
		if merged["header_value"] != "typed-now" || merged["signing_secret"] != testSecret {
			t.Errorf("%q merged: %v", other, merged)
		}
	}

	basic := map[string]string{"url": "http://192.168.1.10:5678/w", "auth": "basic", "username": "u", "password": "pw-saved", "signing_secret": testSecret}
	_, err := WebhookKept(map[string]string{"url": "http://elsewhere:5678/w", "auth": "basic", "username": "u"}, basic)
	if fe, ok := err.(*FieldError); !ok || fe.Field != "password" {
		t.Errorf("basic to another host: %v", err)
	}
	// Moving and dropping auth altogether needs nothing again, and carries nothing.
	kept, err := WebhookKept(map[string]string{"url": "http://elsewhere:5678/w", "auth": "none"}, basic)
	if err != nil || kept["password"] != "" {
		t.Errorf("auth none elsewhere: %v %v", kept, err)
	}
}

// A row whose kind says "n8n" but that has no webhook fields — an Apprise URL saved with
// that label before the n8n kind existed — goes through Apprise exactly as before: its
// text through the templates, its Test through the transport, no JSON.
func TestLegacyN8NLabelGoesThroughApprise(t *testing.T) {
	s, rec, _ := newTestService(t)
	s.SetPublicURL(func(context.Context) string { return "https://arrmada.example.com" })
	hook := newHookServer(t)
	ctx := context.Background()
	c := mustCreate(t, s, Connection{Name: "old", Kind: KindN8N, URL: "ntfy://old-topic", Enabled: true, Events: sub("movie.imported")})
	if IsWebhook(c) {
		t.Fatal("an Apprise URL labelled n8n counts as a webhook")
	}
	if _, err := s.Dispatch(ctx, "movie.imported", Message{Title: "Imported", Body: "📥 Dune", Link: "/movies/12"}); err != nil {
		t.Fatal(err)
	}
	var payload, body string
	if err := s.db.QueryRow(`SELECT payload, body FROM notification_deliveries WHERE id = 1`).Scan(&payload, &body); err != nil {
		t.Fatal(err)
	}
	if payload != "" || body != "📥 Dune\nhttps://arrmada.example.com/movies/12" {
		t.Fatalf("queued payload %q body %q", payload, body)
	}
	drainNow(t, s)
	if err := s.Test(ctx, c, ""); err != nil {
		t.Fatal(err)
	}
	got := rec.got()
	if len(got) != 2 || !strings.HasPrefix(got[0], "ntfy://old-topic|") || !strings.HasPrefix(got[1], "ntfy://old-topic|") {
		t.Fatalf("Apprise got %v", got)
	}
	if n := len(hook.got()); n != 0 {
		t.Fatalf("%d webhook requests", n)
	}
	// With its fields and an http(s) URL, the same kind is a webhook.
	if !IsWebhook(webhookConn(hook.URL+"/w", nil)) {
		t.Fatal("a real n8n connection isn't a webhook")
	}
}
