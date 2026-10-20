package connstatus

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestExplainClasses(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Class
		has  string // a phrase the guidance must contain
	}{
		{"timeout", `Get "http://192.168.1.5:8080/api": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`, ClassTimeout, "No answer"},
		{"dial timeout", `dial tcp 10.0.0.9:8989: i/o timeout`, ClassTimeout, "firewall"},
		{"dns", `Get "http://nas.lan:8080/": dial tcp: lookup nas.lan on 1.1.1.1:53: no such host`, ClassDNS, "nas.lan"},
		{"docker name", `Post "http://qbittorrent:8080/api/v2/auth/login": dial tcp: lookup qbittorrent on 127.0.0.53:53: no such host`, ClassDockerName, "Docker network"},
		{"refused", `Get "http://192.168.1.5:9999/": dial tcp 192.168.1.5:9999: connect: connection refused`, ClassRefused, "port"},
		{"localhost refused", `Get "http://localhost:8080/": dial tcp 127.0.0.1:8080: connect: connection refused`, ClassLocalhost, "own container"},
		{"loopback ip refused", `Get "http://127.0.0.1:8080/": dial tcp 127.0.0.1:8080: connect: connection refused`, ClassLocalhost, "localhost"},
		{"no route", `Get "http://192.168.50.2:80/": dial tcp 192.168.50.2:80: connect: no route to host`, ClassUnreachable, "192.168.50.2"},
		{"reset", `Get "http://10.0.0.2:80/": read tcp 1.2.3.4:5->10.0.0.2:80: read: connection reset by peer`, ClassReset, "cut off"},
		{"eof", `Get "http://10.0.0.2:80/": EOF`, ClassReset, "https://"},
		{"tls unknown authority", `Get "https://plex.home:32400/": tls: failed to verify certificate: x509: certificate signed by unknown authority`, ClassTLS, "certificate"},
		{"http to https client", `Get "https://10.0.0.2:8080/": http: server gave HTTP response to HTTPS client`, ClassWrongScheme, "http://"},
		{"tls on plain port", `Get "https://10.0.0.2:8080/": tls: first record does not look like a TLS handshake`, ClassWrongScheme, "http://"},
		{"bad scheme", `Get "192.168.1.5:8080/api": unsupported protocol scheme ""`, ClassBadURL, "http://"},
		{"401", `qbittorrent: login HTTP 401`, ClassAuth, "API key"},
		{"jellyfin key", `Jellyfin rejected the API key`, ClassAuth, "API key"},
		{"tmdb", `TMDB rejected the key: Unauthorized`, ClassAuth, "401"},
		{"403", `qbittorrent: preferences HTTP 403`, ClassForbidden, "403"},
		{"404", `prowlarr: HTTP 404`, ClassNotFound, "404"},
		{"not json", `invalid character '<' looking for beginning of value`, ClassNotFound, "expected"},
		{"429", `HTTP 429 Too Many Requests`, ClassRateLimited, "slow down"},
		{"5xx", `prowlarr: HTTP 502`, ClassServerError, "5xx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, text := Explain(errors.New(c.msg))
			if got != c.want {
				t.Fatalf("class = %q, want %q (text %q)", got, c.want, text)
			}
			if !strings.Contains(text, c.has) {
				t.Errorf("text %q lacks %q", text, c.has)
			}
			if strings.Contains(text, "context deadline") || strings.Contains(text, "dial tcp") {
				t.Errorf("raw error leaked into guidance: %q", text)
			}
		})
	}
}

func TestExplainUnknownPassesThroughRedacted(t *testing.T) {
	c, text := Explain(errors.New(`SABnzbd refused the address Arrmada uses for it. Add sab to host_whitelist`))
	if c != ClassNone || !strings.Contains(text, "host_whitelist") {
		t.Fatalf("got %q %q", c, text)
	}
	c, text = Explain(errors.New("weird failure at http://u:hunter2@h/x?apikey=SECRETVALUE"))
	if c != ClassNone || strings.Contains(text, "hunter2") || strings.Contains(text, "SECRETVALUE") {
		t.Fatalf("secret leaked: %q", text)
	}
	if c, text := Explain(nil); c != ClassNone || text != "" {
		t.Fatalf("nil error: %q %q", c, text)
	}
}

func TestExplainNeverEchoesSecrets(t *testing.T) {
	msg := `Get "http://user:hunter2@nas.lan:8080/x?apikey=SECRETVALUE": dial tcp: lookup nas.lan: no such host`
	_, text := Explain(errors.New(msg))
	for _, bad := range []string{"hunter2", "SECRETVALUE", "apikey"} {
		if strings.Contains(text, bad) {
			t.Fatalf("%q leaked in %q", bad, text)
		}
	}
}

func TestExplainContextDeadline(t *testing.T) {
	if c, _ := Explain(context.DeadlineExceeded); c != ClassTimeout {
		t.Fatalf("class = %q", c)
	}
}

// Real errors from the net stack, not only hand-written strings.
func TestExplainRealNetErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback")
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens now
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	_, err = http.DefaultClient.Do(req)
	if c, _ := Explain(err); c != ClassLocalhost {
		t.Errorf("refused loopback: class %q for %v", c, err)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://arrmada-no-such-box.invalid/", nil)
	_, err = http.DefaultClient.Do(req)
	if c, _ := Explain(err); c != ClassDockerName && c != ClassDNS {
		t.Errorf("dns: class %q for %v", c, err)
	}
}
