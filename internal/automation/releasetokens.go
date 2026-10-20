package automation

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Release tokens stand in for download links in everything the browser sees.
//
// A release's download link is the indexer's enclosure: for a Prowlarr- or
// Jackett-synced indexer it carries the indexer's apikey, and a MyAnonaMouse link
// carries the owner's personal download token. Shown to the browser, those land in
// devtools, HAR files and extensions; accepted back from the browser, they let any
// manager account make the server fetch whatever URL it likes. So an interactive search
// hands out a random token per release instead, the link stays here, and a grab names
// the token. A token is only good for the title and the person it was issued to, and
// only for a while — an open modal past that, or across a restart, is told to search
// again.

// Token kinds: which grab endpoint a token may be spent on.
const (
	ReleaseKindMovie  = "movie"
	ReleaseKindSeries = "series"
	ReleaseKindBook   = "book"
	ReleaseKindAlbum  = "album" // a music album's interactive search (MUS-17)
)

const (
	releaseTokenTTL = 2 * time.Hour
	// releaseTokenCap bounds the memory a burst of searches can pin: one search issues a
	// few hundred tokens at most, so this is many modals' worth.
	releaseTokenCap = 20000
)

var (
	// ErrReleaseExpired: the token is unknown — too old, evicted, or from before a restart.
	ErrReleaseExpired = errors.New("this search result has expired — search again")
	// ErrReleaseScope: the token is real but was issued for another title, kind or person.
	ErrReleaseScope = errors.New("that result belongs to a different title")
)

// ReleaseRef is everything a grab needs about one search result, held server-side under
// its token.
type ReleaseRef struct {
	Indexer     string
	DownloadURL string
	Title       string
	InfoHash    string // from the indexer's listing, when it gave one
	// Transport is the release's network as the listing showed it ("torrent" | "usenet").
	// The grab itself routes by the indexer (grabTo), so a stale token can't send an NZB
	// to a torrent client; this is what the release was offered as.
	Transport string
	MediaKind string // ReleaseKindMovie | ReleaseKindSeries | ReleaseKindBook | ReleaseKindAlbum
	MediaID   int64
	VersionID int64 // books: the audiobook version the release was matched to (0 = standard)
	// Series: the search the result came from — a negative Season is the whole show,
	// Episode 0 the whole season. It is the grab's scope, so the import gate's exemption
	// can't be widened by the browser.
	Season, Episode int
	UserID          int64
	ExpiresAt       time.Time
	// PreviewKey is set on a release preview's token (preview.go): what it was previewed
	// for ("request:12"). Such a token is spendable only through GrabPreviewed, never by a
	// grab endpoint.
	PreviewKey string
	// SizeGB and Edition are a preview's facts the grab checks again: the room it needs,
	// and for a book which edition it is.
	SizeGB  float64
	Edition string
}

type releaseTokens struct {
	mu    sync.Mutex
	refs  map[string]ReleaseRef
	order []string // insertion order, oldest first, for eviction; order[:head] is spent
	head  int
	now   func() time.Time
	ttl   time.Duration
	cap   int
}

func newReleaseTokens() *releaseTokens {
	return &releaseTokens{refs: map[string]ReleaseRef{}, now: time.Now, ttl: releaseTokenTTL, cap: releaseTokenCap}
}

// Issue stores ref and returns its new token. ExpiresAt is set here.
func (t *releaseTokens) Issue(ref ReleaseRef) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand doesn't fail on any supported platform; a token we can't make
		// unguessable must not be made at all.
		panic("release tokens: crypto/rand: " + err.Error())
	}
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	ref.ExpiresAt = t.now().Add(t.ttl)
	t.refs[tok] = ref
	t.order = append(t.order, tok)
	t.evictLocked()
	return tok
}

// evictLocked drops expired tokens from the front, then the oldest until under the cap.
// Every token has the same TTL, so insertion order is expiry order. The slice is
// compacted only once half of it is dead, so a full store doesn't copy 20,000 entries
// on every issue.
func (t *releaseTokens) evictLocked() {
	now := t.now()
	for t.head < len(t.order) {
		ref, ok := t.refs[t.order[t.head]]
		if ok && now.Before(ref.ExpiresAt) && len(t.order)-t.head <= t.cap {
			break
		}
		delete(t.refs, t.order[t.head])
		t.order[t.head] = ""
		t.head++
	}
	if t.head > 0 && t.head*2 >= len(t.order) {
		t.order = append(t.order[:0:0], t.order[t.head:]...)
		t.head = 0
	}
}

// Resolve returns the release behind token, provided it was issued for this kind of
// grab, this title and this user. An unknown or expired token is ErrReleaseExpired; a
// mismatch is ErrReleaseScope.
func (t *releaseTokens) Resolve(token, kind string, mediaID, userID int64) (ReleaseRef, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref, ok := t.refs[token]
	if !ok {
		return ReleaseRef{}, ErrReleaseExpired
	}
	if !t.now().Before(ref.ExpiresAt) {
		delete(t.refs, token) // its slot in order is dropped by the next eviction pass
		return ReleaseRef{}, ErrReleaseExpired
	}
	if ref.MediaKind != kind || ref.MediaID != mediaID || ref.UserID != userID || ref.PreviewKey != "" {
		return ReleaseRef{}, ErrReleaseScope
	}
	return ref, nil
}

// len is the number of live entries (tests).
func (t *releaseTokens) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.refs)
}

// ReleaseTokens is the coordinator's token store, made on first use so a bare
// Coordinator (tests) has one too.
func (c *Coordinator) ReleaseTokens() *releaseTokens {
	c.tokensOnce.Do(func() {
		if c.tokens == nil {
			c.tokens = newReleaseTokens()
		}
	})
	return c.tokens
}

// IssueReleaseToken stores ref and returns its token.
func (c *Coordinator) IssueReleaseToken(ref ReleaseRef) string { return c.ReleaseTokens().Issue(ref) }

// ResolveReleaseToken returns the release behind token for this grab (see releaseTokens.Resolve).
func (c *Coordinator) ResolveReleaseToken(token, kind string, mediaID, userID int64) (ReleaseRef, error) {
	return c.ReleaseTokens().Resolve(token, kind, mediaID, userID)
}

// Preview tokens stand for the releases a request's preview would grab (preview.go). They
// live apart from the interactive ones: they're issued to nobody — the request keeps them,
// server-side — and live as long as a preview counts as fresh, where an interactive
// result is good for two hours. Kept separate, the interactive store's insertion order
// stays its expiry order, and a preview token can never be spent at a grab endpoint.
const (
	previewTokenTTL = 7 * time.Hour // a preview is re-checked after six
	previewTokenCap = 5000          // previews are paced to a few a minute; this is days of them
)

type previewTokenStore struct {
	mu   sync.Mutex
	refs map[string]ReleaseRef
	now  func() time.Time
}

func (t *previewTokenStore) issue(ref ReleaseRef) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("release tokens: crypto/rand: " + err.Error())
	}
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	ref.ExpiresAt = now.Add(previewTokenTTL)
	// Few enough to sweep in full: drop the expired, then the soonest to expire while full.
	for k, r := range t.refs {
		if !now.Before(r.ExpiresAt) {
			delete(t.refs, k)
		}
	}
	for len(t.refs) >= previewTokenCap {
		oldest := ""
		for k, r := range t.refs {
			if oldest == "" || r.ExpiresAt.Before(t.refs[oldest].ExpiresAt) {
				oldest = k
			}
		}
		delete(t.refs, oldest)
	}
	t.refs[tok] = ref
	return tok
}

// resolve returns the releases behind tokens, all or none: an unknown or expired one is
// ErrReleaseExpired, one issued for another preview or kind is ErrReleaseScope.
func (t *previewTokenStore) resolve(tokens []string, key, kind string) ([]ReleaseRef, error) {
	if len(tokens) == 0 || key == "" {
		return nil, ErrReleaseExpired
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ReleaseRef, 0, len(tokens))
	for _, tok := range tokens {
		ref, ok := t.refs[tok]
		if !ok || !t.now().Before(ref.ExpiresAt) {
			return nil, ErrReleaseExpired
		}
		if ref.PreviewKey != key || ref.MediaKind != kind {
			return nil, ErrReleaseScope
		}
		out = append(out, ref)
	}
	return out, nil
}

// previewTokens is the coordinator's preview token store, made on first use.
func (c *Coordinator) previewTokens() *previewTokenStore {
	c.previewOnce.Do(func() {
		if c.previewTok == nil {
			c.previewTok = &previewTokenStore{refs: map[string]ReleaseRef{}, now: time.Now}
		}
	})
	return c.previewTok
}

// PreviewTokensLive reports whether every one of a preview's tokens still stands for its
// release: issued for key and kind, not expired, not lost to a restart.
func (c *Coordinator) PreviewTokensLive(tokens []string, key, kind string) bool {
	_, err := c.previewTokens().resolve(tokens, key, kind)
	return err == nil
}

// secretParams are query parameters that carry a credential on the indexer links we
// know of: Prowlarr/Jackett/Torznab apikeys, tracker passkeys and RSS keys.
var secretParams = map[string]bool{
	"apikey": true, "api_key": true, "jackett_apikey": true, "passkey": true,
	"torrent_pass": true, "authkey": true, "rsskey": true, "token": true, "dl": true,
}

// safeInfoURL is a release's details link with any credential stripped, or "" when it
// is the download link itself (a Torznab GUID often is). It's a link the browser shows,
// so it must never be a way to the download or a key.
func safeInfoURL(info, download string) string {
	info = strings.TrimSpace(info)
	if info == "" || (download != "" && info == download) {
		return ""
	}
	u, err := url.Parse(info)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.User = nil
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if secretParams[strings.ToLower(k)] {
				q.Del(k)
			}
		}
		u.RawQuery = q.Encode()
	}
	out := u.String()
	if download != "" {
		if d, err := url.Parse(download); err == nil {
			d.User = nil
			if dq := d.Query(); len(dq) > 0 {
				for k := range dq {
					if secretParams[strings.ToLower(k)] {
						dq.Del(k)
					}
				}
				d.RawQuery = dq.Encode()
			}
			if d.String() == out {
				return "" // the download link with its key taken out is still the download link
			}
		}
	}
	return out
}
