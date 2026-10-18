package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"sort"
	"sync"
	"time"
)

// Helpers the password sign-ins share with the limiter (ratelimit.go): one check at a
// time per address and username, the addresses a person signs in from, and wrong
// passwords already counted.

// turnGate lets one password check at a time through per key. The limiter only learns of
// a wrong password once bcrypt has said so, so without this two hundred guesses sent at
// once from one address all got past "fewer than ten failures?" before the first was
// recorded. Waiting for a turn (rather than refusing) keeps a household's bursts working:
// an e-reader fetching fifty covers at once just queues for a few milliseconds each.
type turnGate struct {
	mu    sync.Mutex
	slots map[string]*turnSlot
}

type turnSlot struct {
	ch   chan struct{}
	refs int
}

// turnWait is the longest a check waits for its turn before it's answered 429.
const turnWait = 10 * time.Second

func newTurnGate() *turnGate { return &turnGate{slots: map[string]*turnSlot{}} }

// acquire takes a turn on every key, in order (callers always pass the address before the
// username, so two checks never wait on each other). It gives up after turnWait or when
// ctx ends; ok is false then and nothing is held. A nil gate lets everything through.
func (g *turnGate) acquire(ctx context.Context, keys ...string) (release func(), ok bool) {
	if g == nil {
		return func() {}, true
	}
	timer := time.NewTimer(turnWait)
	defer timer.Stop()
	var held []string
	release = func() {
		for i := len(held) - 1; i >= 0; i-- {
			g.put(held[i])
		}
		held = nil
	}
	for _, k := range keys {
		s := g.get(k)
		select {
		case s.ch <- struct{}{}:
			held = append(held, k)
		case <-timer.C:
			g.unref(k)
			release()
			return func() {}, false
		case <-ctx.Done():
			g.unref(k)
			release()
			return func() {}, false
		}
	}
	return release, true
}

// get returns key's slot, counting the caller in so it isn't dropped while waited on.
func (g *turnGate) get(key string) *turnSlot {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.slots[key]
	if s == nil {
		s = &turnSlot{ch: make(chan struct{}, 1)}
		g.slots[key] = s
	}
	s.refs++
	return s
}

// put ends a held turn on key.
func (g *turnGate) put(key string) {
	g.mu.Lock()
	s := g.slots[key]
	g.mu.Unlock()
	<-s.ch
	g.unref(key)
}

// unref drops the caller's count on key's slot, and the slot once nobody uses it.
func (g *turnGate) unref(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.slots[key]; s != nil {
		if s.refs--; s.refs <= 0 {
			delete(g.slots, key)
		}
	}
}

// addrKey is the address a limit or a known address is kept under: an IPv4 address as it
// is, an IPv6 one by its /64, which is what one home or phone is given — keyed by the
// whole address, rotating through the /64 would make a fresh key every time.
func addrKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil || p.To4() != nil {
		return ip
	}
	return (&net.IPNet{IP: p.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

// knownAddrs remembers where each username last signed in with the right password, for
// letting a person's own devices past their username's pause (appsignin.go). Bounded: the
// most recent knownPerUser addresses per username, each for knownMemory, refreshed at most
// once per knownRefresh, so a reader signing in on every request costs one map read.
type knownAddrs struct {
	mu     sync.Mutex
	byUser map[string]map[string]int64 // username → address key → last right password
	lastGC int64
	now    func() int64
}

const (
	knownMemory  = 30 * 24 * time.Hour
	knownPerUser = 16
	knownRefresh = time.Hour
)

func newKnownAddrs() *knownAddrs {
	return &knownAddrs{byUser: map[string]map[string]int64{}, now: func() int64 { return time.Now().UnixNano() }}
}

// remember records a right password for user from addr.
func (k *knownAddrs) remember(user, addr string) {
	if k == nil {
		return
	}
	now := k.now()
	k.mu.Lock()
	defer k.mu.Unlock()
	if now-k.lastGC > int64(knownRefresh) {
		cut := now - int64(knownMemory)
		for u, m := range k.byUser {
			for a, at := range m {
				if at < cut {
					delete(m, a)
				}
			}
			if len(m) == 0 {
				delete(k.byUser, u)
			}
		}
		k.lastGC = now
	}
	m := k.byUser[user]
	if m == nil {
		m = map[string]int64{}
		k.byUser[user] = m
	}
	if at, ok := m[addr]; ok && now-at < int64(knownRefresh) {
		return
	}
	m[addr] = now
	if len(m) > knownPerUser {
		addrs := make([]string, 0, len(m))
		for a := range m {
			addrs = append(addrs, a)
		}
		sort.Slice(addrs, func(i, j int) bool { return m[addrs[i]] > m[addrs[j]] })
		for _, a := range addrs[knownPerUser:] {
			delete(m, a)
		}
	}
}

// isKnown reports whether user signed in from addr with the right password recently.
func (k *knownAddrs) isKnown(user, addr string) bool {
	if k == nil {
		return false
	}
	now := k.now()
	k.mu.Lock()
	defer k.mu.Unlock()
	at, ok := k.byUser[user][addr]
	return ok && now-at < int64(knownMemory)
}

// wrongSeen remembers the wrong passwords already counted per address and username, so a
// stale password a device keeps sending (an e-reader after the password changed, an app
// retrying) counts once rather than locking its whole household's address out and
// raising the guessing alert. Only an HMAC under a per-process random key is kept, never
// the password, and only for failMemory; at most wrongPerKey per address and username.
type wrongSeen struct {
	mu     sync.Mutex
	key    []byte
	seen   map[string][]wrongEntry
	lastGC int64
	now    func() int64
}

type wrongEntry struct {
	sum [sha256.Size]byte
	at  int64
}

const wrongPerKey = 8

func newWrongSeen() *wrongSeen {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &wrongSeen{key: key, seen: map[string][]wrongEntry{}, now: func() int64 { return time.Now().UnixNano() }}
}

// repeat records password as wrong for key and reports whether it already was, within
// failMemory.
func (s *wrongSeen) repeat(key, password string) bool {
	if s == nil {
		return false
	}
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(key))
	m.Write([]byte{0})
	m.Write([]byte(password))
	var sum [sha256.Size]byte
	copy(sum[:], m.Sum(nil))
	now := s.now()
	cut := now - int64(failMemory)
	s.mu.Lock()
	defer s.mu.Unlock()
	if now-s.lastGC > int64(15*time.Minute) {
		for k, es := range s.seen {
			if len(es) == 0 || es[len(es)-1].at < cut {
				delete(s.seen, k)
			}
		}
		s.lastGC = now
	}
	var kept []wrongEntry
	seen := false
	for _, e := range s.seen[key] {
		if e.at < cut {
			continue
		}
		if hmac.Equal(e.sum[:], sum[:]) {
			seen = true
			continue // moved to the end below, as the newest
		}
		kept = append(kept, e)
	}
	kept = append(kept, wrongEntry{sum: sum, at: now})
	if len(kept) > wrongPerKey {
		kept = kept[len(kept)-wrongPerKey:]
	}
	s.seen[key] = kept
	return seen
}
