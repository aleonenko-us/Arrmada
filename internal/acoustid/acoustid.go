// Package acoustid identifies audio files by sound (MUS-32): a Chromaprint fingerprint
// of the file (mediainfo.Fingerprint, the bundled ffmpeg) goes to AcoustID, which answers
// with the MusicBrainz recordings it has heard. The music matcher uses it as a second pass
// when a download's tags and names aren't enough.
//
// The key is each owner's own AcoustID application key (free for non-commercial use),
// entered in Settings → Connections; with none, nothing here calls AcoustID. The key goes
// in the request body, never the URL, so a transport error (which quotes the URL) can't
// carry it into a log.
package acoustid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// KeyID is the AcoustID key's id in the API keys catalogue.
const KeyID = "acoustid"

// DefaultBaseURL is AcoustID's web service.
const DefaultBaseURL = "https://api.acoustid.org/v2"

// ErrNoKey means no AcoustID key is set: the second pass is off.
var ErrNoKey = errors.New("no AcoustID key is set")

// ErrKeyRejected means AcoustID refused the key.
var ErrKeyRejected = errors.New("AcoustID rejected the key")

// minScore is the lowest AcoustID match score whose recordings count: below it the
// fingerprint only vaguely resembles the one AcoustID knows.
const minScore = 0.5

// requestGap paces requests: AcoustID asks clients for at most three a second.
const requestGap = 334 * time.Millisecond

// Client talks to AcoustID.
type Client struct {
	http *http.Client
	base string
	key  func() string

	mu   sync.Mutex
	last time.Time
	now  func() time.Time
}

// New builds a client that reads the key on every call (nil: no key).
func New(key func() string) *Client {
	if key == nil {
		key = func() string { return "" }
	}
	return &Client{http: &http.Client{Timeout: 20 * time.Second}, base: DefaultBaseURL, key: key, now: time.Now}
}

// Enabled reports whether a key is set.
func (c *Client) Enabled() bool { return strings.TrimSpace(c.key()) != "" }

// wait holds a request until the pace allows it.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(requestGap)
	now := c.now()
	if next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	if d := next.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// Match is one AcoustID result: a track id, how well the fingerprint matched, and the
// MusicBrainz recordings AcoustID links to it.
type Match struct {
	ID         string
	Score      float64
	Recordings []string
}

// Lookup asks AcoustID which recordings a fingerprint is, best match first, with the key
// given (the saved one when key is ""). Matches under minScore are left out.
func (c *Client) Lookup(ctx context.Context, fingerprint string, durationSec int, key string) ([]Match, error) {
	if key == "" {
		key = strings.TrimSpace(c.key())
	}
	if key == "" {
		return nil, ErrNoKey
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	form := url.Values{
		"client": {key}, "format": {"json"}, "meta": {"recordingids"},
		"duration": {strconv.Itoa(durationSec)}, "fingerprint": {fingerprint},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/lookup", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Arrmada/"+httpua.Version()+" ( "+httpua.Homepage+" )")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("AcoustID didn't answer: %w", errors.Unwrap(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("AcoustID: %w", err)
	}
	var ans struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Results []struct {
			ID         string  `json:"id"`
			Score      float64 `json:"score"`
			Recordings []struct {
				ID string `json:"id"`
			} `json:"recordings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("AcoustID answered %s with something that isn't JSON", resp.Status)
	}
	if ans.Status != "ok" {
		if ans.Error != nil && (ans.Error.Code == 4 || strings.Contains(strings.ToLower(ans.Error.Message), "api key")) {
			return nil, ErrKeyRejected
		}
		msg := resp.Status
		if ans.Error != nil {
			msg = ans.Error.Message
		}
		return nil, fmt.Errorf("AcoustID: %s", msg)
	}
	var out []Match
	for _, r := range ans.Results {
		if r.Score < minScore {
			continue
		}
		m := Match{ID: r.ID, Score: r.Score}
		for _, rec := range r.Recordings {
			if rec.ID != "" {
				m.Recordings = append(m.Recordings, rec.ID)
			}
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// testFingerprint is a short, valid fingerprint (a second of a 440 Hz tone, made by
// ffmpeg's chromaprint muxer) for the key Test: AcoustID checks the key before it looks
// anything up.
const testFingerprint = "AQAAZEmUaEkSZSoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// Verify is the API key Test: one lookup with the key (the typed candidate, or the saved
// one), reporting whether AcoustID accepted it.
func (c *Client) Verify(ctx context.Context, candidate string) (string, error) {
	_, err := c.Lookup(ctx, testFingerprint, 15, strings.TrimSpace(candidate))
	switch {
	case err == nil:
		return "AcoustID accepted the key.", nil
	case errors.Is(err, ErrKeyRejected):
		return "", errors.New("AcoustID rejected the key: check it's an application key (from acoustid.org/new-application), copied in full")
	}
	return "", err
}
