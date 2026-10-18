package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// A known address stays known for 30 days after its last right password, then is
// forgotten (and collected); each username keeps only its 16 most recent addresses, and a
// fresh entry isn't rewritten within the hour.
func TestKnownAddrs(t *testing.T) {
	k := newKnownAddrs()
	now := int64(0)
	k.now = func() int64 { return now }
	if k.isKnown("mom", "203.0.113.9") {
		t.Fatal("known before any sign-in")
	}
	k.remember("mom", "203.0.113.9")
	now += int64(29 * 24 * time.Hour)
	if !k.isKnown("mom", "203.0.113.9") {
		t.Error("forgotten after 29 days")
	}
	if k.isKnown("dad", "203.0.113.9") {
		t.Error("known for another username")
	}
	now += int64(2 * 24 * time.Hour)
	if k.isKnown("mom", "203.0.113.9") {
		t.Error("still known after 31 days")
	}
	k.remember("dad", "198.51.100.1") // runs the collection
	if _, ok := k.byUser["mom"]; ok {
		t.Error("expired entry not collected")
	}

	// The cap: the 16 most recent addresses per username.
	for i := 0; i < 40; i++ {
		now += int64(time.Minute)
		k.remember("kid", fmt.Sprintf("2001:db8:%x::/64", i))
	}
	if n := len(k.byUser["kid"]); n != knownPerUser {
		t.Errorf("kid has %d known addresses, want %d", n, knownPerUser)
	}
	if !k.isKnown("kid", "2001:db8:27::/64") || k.isKnown("kid", "2001:db8:0::/64") {
		t.Error("the cap kept the wrong addresses")
	}

	// Refreshed at most hourly.
	at := k.byUser["kid"]["2001:db8:27::/64"]
	now += int64(30 * time.Minute)
	k.remember("kid", "2001:db8:27::/64")
	if k.byUser["kid"]["2001:db8:27::/64"] != at {
		t.Error("refreshed within the hour")
	}
	now += int64(31 * time.Minute)
	k.remember("kid", "2001:db8:27::/64")
	if k.byUser["kid"]["2001:db8:27::/64"] == at {
		t.Error("not refreshed after the hour")
	}
}

// An IPv6 address counts by its /64; IPv4 as it is.
func TestAddrKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.9":                "203.0.113.9",
		"2001:db8:1:2:aaaa::1":       "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb:cccc::77": "2001:db8:1:2::/64",
		"::ffff:203.0.113.9":         "::ffff:203.0.113.9",
		"not-an-ip":                  "not-an-ip",
	} {
		if got := addrKey(in); got != want {
			t.Errorf("addrKey(%s) = %s, want %s", in, got, want)
		}
	}
}

// The same wrong password from one address counts once; a new one counts; it's forgotten
// after the hour.
func TestWrongSeen(t *testing.T) {
	s := newWrongSeen()
	now := int64(0)
	s.now = func() int64 { return now }
	if s.repeat("app:1\x00mom", "old-password") {
		t.Fatal("first wrong password seen as a repeat")
	}
	if !s.repeat("app:1\x00mom", "old-password") {
		t.Error("the same wrong password again isn't a repeat")
	}
	if s.repeat("app:2\x00mom", "old-password") || s.repeat("app:1\x00dad", "old-password") {
		t.Error("a repeat across addresses or usernames")
	}
	if s.repeat("app:1\x00mom", "other-guess") {
		t.Error("a different wrong password seen as a repeat")
	}
	now += int64(61 * time.Minute)
	if s.repeat("app:1\x00mom", "old-password") {
		t.Error("remembered past the hour")
	}
}

// The turn gate lets one holder per key through, queues the rest, and gives up after the
// context ends.
func TestTurnGate(t *testing.T) {
	g := newTurnGate()
	release, ok := g.acquire(context.Background(), "a", "b")
	if !ok {
		t.Fatal("first turn refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := g.acquire(ctx, "x", "b"); ok {
		t.Error("a second holder on b")
	}
	release()
	r2, ok := g.acquire(context.Background(), "x", "b")
	if !ok {
		t.Fatal("turn not given back")
	}
	r2()
	g.mu.Lock()
	n := len(g.slots)
	g.mu.Unlock()
	if n != 0 {
		t.Errorf("%d slots left behind", n)
	}
}

// parallelBurst sends n requests at once and counts the answers by status.
func parallelBurst(n int, send func(i int) int) map[int]int {
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			c := send(i)
			mu.Lock()
			codes[c]++
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()
	return codes
}

// Wrong passwords sent all at once from one address (on the LAN, so only the address's
// limit applies) are held to the same ten as sequential ones: Arrmada's own sign-in, the audiobook server and the OPDS catalogue.
func TestParallelGuessesAreCounted(t *testing.T) {
	const burst = 60
	h := newOPDSHarness(t)
	s := h.rs
	ctx := context.Background()
	u, _ := s.user(t, "gran", auth.RoleRequester)
	if err := s.deps.AudioServer.Accounts.SetPassword(ctx, u.ID, "gran-listens", false); err != nil {
		t.Fatal(err)
	}
	abs := s.deps.AudioServer.Handler()

	check := func(what string, codes map[int]int) {
		t.Helper()
		if codes[http.StatusUnauthorized] != 10 || codes[http.StatusTooManyRequests] != burst-10 {
			t.Errorf("%s: %v — want exactly 10 checked (401), the rest 429", what, codes)
		}
	}

	check("Arrmada sign-in", parallelBurst(burst, func(i int) int {
		return signIn(s, "192.168.1.120:5000", "gran", fmt.Sprintf("guess-%d", i), nil)
	}))
	check("audiobook server", parallelBurst(burst, func(i int) int {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(fmt.Sprintf(`{"username":"gran","password":"guess-%d"}`, i)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.168.1.121:5000"
		rec := httptest.NewRecorder()
		abs.ServeHTTP(rec, r)
		return rec.Code
	}))
	check("OPDS", parallelBurst(burst, func(i int) int {
		return h.get("/opds", "gran", fmt.Sprintf("guess-%d", i), "192.168.1.122:5000", nil).Code
	}))
}

// A device still sending an old password counts once, however often it tries, so it
// doesn't lock the rest of its address (a household behind one proxy) out; new guesses
// still count.
func TestStalePasswordCountsOnce(t *testing.T) {
	h := newOPDSHarness(t)
	s := h.rs
	ctx := context.Background()
	for _, name := range []string{"mom", "kid"} {
		u, _ := s.user(t, name, auth.RoleRequester)
		if err := s.deps.AudioServer.Accounts.SetPassword(ctx, u.ID, name+"-reads-1", false); err != nil {
			t.Fatal(err)
		}
	}
	const house = "192.168.1.1:5000" // everyone behind the router's proxy
	for i := 0; i < 30; i++ {
		if rec := h.get("/opds", "mom", "mom-old-password", house, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("stale reader try %d: HTTP %d, want 401", i, rec.Code)
		}
	}
	if rec := h.get("/opds", "kid", "kid-reads-1", house, nil); rec.Code != http.StatusOK {
		t.Errorf("the rest of the house after a stale reader: HTTP %d", rec.Code)
	}
	var guesses atomic.Int32
	for i := 0; i < 12; i++ {
		if h.get("/opds", "kid", fmt.Sprintf("guess-%d", i), house, nil).Code == http.StatusUnauthorized {
			guesses.Add(1)
		}
	}
	if n := guesses.Load(); n != 9 {
		t.Errorf("distinct guesses checked before the block: %d, want 9 (one failure already recorded)", n)
	}
}
