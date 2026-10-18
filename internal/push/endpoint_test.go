package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestValidEndpoint(t *testing.T) {
	for ep, ok := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                      true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc":       true,
		"https://web.push.apple.com/QGx3":                              true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":          true,
		"https://FCM.googleapis.com:443/fcm/send/abc":                  true,
		"http://fcm.googleapis.com/fcm/send/abc":                       false,
		"https://fcm.googleapis.com:8443/fcm/send/abc":                 false,
		"https://user:pw@fcm.googleapis.com/fcm/send/abc":              false,
		"https://evil.example/fcm.googleapis.com":                      false,
		"https://fcm.googleapis.com.evil.example/x":                    false,
		"https://push.apple.com.evil.example/x":                        false,
		"https://192.168.1.1/":                                         false,
		"https://169.254.169.254/latest/meta-data/":                    false,
		"https://storage.googleapis.com/bucket/x":                      false,
		"https://evilpush.services.mozilla.com.attacker.example/wpush": false,
		"": false,
	} {
		if got := ValidEndpoint(ep) == nil; got != ok {
			t.Errorf("ValidEndpoint(%q) ok = %v, want %v", ep, got, ok)
		}
	}
}

// N3: an endpoint that isn't a push service is refused on subscribe, and one account
// keeps at most MaxPerUser devices, losing its oldest first.
func TestSubscribeRefusesOtherAddressesAndCapsDevices(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.Subscribe(ctx, 1, "https://192.168.1.10:8080/hook", "p", "a"); !errors.Is(err, ErrBadEndpoint) {
		t.Fatalf("LAN endpoint: %v", err)
	}
	for i := range MaxPerUser + 2 {
		if err := s.Subscribe(ctx, 1, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i), "p", "a"); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Subscribe(ctx, 2, "https://fcm.googleapis.com/fcm/send/other", "p", "a")
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE user_id = 1`).Scan(&n)
	if n != MaxPerUser {
		t.Fatalf("user 1 has %d devices, want %d", n, MaxPerUser)
	}
	if s.HasSubscription(ctx, 1, "https://fcm.googleapis.com/fcm/send/0") || s.HasSubscription(ctx, 1, "https://fcm.googleapis.com/fcm/send/1") {
		t.Fatal("the oldest devices were kept")
	}
	if !s.HasSubscription(ctx, 1, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", MaxPerUser+1)) || !s.HasSubscription(ctx, 2, "https://fcm.googleapis.com/fcm/send/other") {
		t.Fatal("the newest device, or another account's, was dropped")
	}
}

// recordingClient stands in for the push services.
type recordingClient struct {
	mu   sync.Mutex
	urls []string
}

func (c *recordingClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.urls = append(c.urls, req.URL.String())
	c.mu.Unlock()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// A row saved before endpoints were checked is never posted to; the push services are,
// through the bounded client and with the caller's context.
func TestSendSkipsEndpointsThatArentPushServices(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.PublicKey(ctx); err != nil {
		t.Fatal(err)
	}
	rc := &recordingClient{}
	s.httpClient = rc
	// The old check took any https address.
	if _, err := s.db.Exec(`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (1, 'https://10.0.0.1/hook', 'BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM', 'tBHItJI5svbpez7KI4CCXg')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, 1, "https://fcm.googleapis.com/fcm/send/ok", "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM", "tBHItJI5svbpez7KI4CCXg"); err != nil {
		t.Fatal(err)
	}
	n, err := s.SendToUserResult(ctx, 1, "t", "b", "/")
	if n != 1 || err != nil {
		t.Fatalf("sent %d, %v", n, err)
	}
	if len(rc.urls) != 1 || !strings.HasPrefix(rc.urls[0], "https://fcm.googleapis.com/") {
		t.Fatalf("posted to %v", rc.urls)
	}
	// A cancelled caller sends nothing that completes.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if n, _ := s.SendToUserResult(cctx, 1, "t", "b", "/"); n != 0 {
		t.Fatalf("a cancelled send delivered to %d devices", n)
	}
}
