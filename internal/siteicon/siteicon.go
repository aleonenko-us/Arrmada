// Package siteicon serves the small site icons shown beside indexers. Only sites on a
// fixed list are fetched (never an address someone typed), once, then kept on disk: a
// tracker's own /favicon.ico first, else DuckDuckGo's icon service for that host (which
// copes with sites behind Cloudflare). A miss is remembered for a day so a dead site
// isn't asked on every page view; the UI shows the tracker's monogram meanwhile.
package siteicon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// Hosts maps an icon slug (what the UI asks for) to the site it comes from.
var Hosts = map[string]string{
	// Torrent
	"torrentleech":   "www.torrentleech.org",
	"myanonamouse":   "www.myanonamouse.net",
	"1337x":          "1337x.to",
	"prowlarr":       "prowlarr.com",
	"iptorrents":     "iptorrents.com",
	"torrentday":     "www.torrentday.com",
	"yts":            "yts.mx",
	"eztv":           "eztvx.to",
	"nyaa":           "nyaa.si",
	"thepiratebay":   "thepiratebay.org",
	"limetorrents":   "www.limetorrents.lol",
	"torrentgalaxy":  "torrentgalaxy.to",
	"filelist":       "filelist.io",
	"beyondhd":       "beyond-hd.me",
	"passthepopcorn": "passthepopcorn.me",
	"broadcasthenet": "broadcasthe.net",
	// Usenet
	"nzbgeek":      "nzbgeek.info",
	"drunkenslug":  "drunkenslug.com",
	"nzbfinder":    "nzbfinder.ws",
	"nzbplanet":    "nzbplanet.net",
	"dognzb":       "dognzb.cr",
	"althub":       "althub.co.za",
	"ninjacentral": "ninjacentral.co.za",
	"nzbsu":        "nzb.su",
	"tabularasa":   "www.tabula-rasa.pw",
	"sabnzbd":      "sabnzbd.org",
}

// ErrUnknown is a slug not on the list; ErrNone a site with no icon to be had (for now).
var (
	ErrUnknown = errors.New("no such site icon")
	ErrNone    = errors.New("no icon for that site")
)

const (
	keep    = 30 * 24 * time.Hour
	missTTL = 24 * time.Hour
	maxSize = 256 << 10
)

// Service fetches and keeps icons under dir.
type Service struct {
	dir    string
	client *http.Client
	mu     sync.Mutex // one fetch at a time: a page full of icons is a handful of sites
}

func New(dir string) *Service {
	return &Service{dir: dir, client: httpua.Client(6 * time.Second)}
}

// Get returns the icon for slug and its content type.
func (s *Service) Get(ctx context.Context, slug string) ([]byte, string, error) {
	host, ok := Hosts[slug]
	if !ok {
		return nil, "", ErrUnknown
	}
	path := filepath.Join(s.dir, slug)
	if b, ok := s.cached(path); ok {
		return answer(b)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.cached(path); ok { // fetched while this one waited
		return answer(b)
	}
	var b []byte
	for _, u := range []string{"https://" + host + "/favicon.ico", "https://icons.duckduckgo.com/ip3/" + host + ".ico"} {
		if got, err := s.fetch(ctx, u); err == nil {
			b = got
			break
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
	}
	if err := os.MkdirAll(s.dir, 0o755); err == nil {
		_ = os.WriteFile(path, b, 0o644) // empty: a miss, kept for a day
	}
	return answer(b)
}

func answer(b []byte) ([]byte, string, error) {
	ct, err := sniff(b)
	if err != nil {
		return nil, "", err
	}
	return b, ct, nil
}

// cached is the kept icon, if it's still fresh (a miss stays fresh for a day only).
func (s *Service) cached(path string) ([]byte, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	ttl := keep
	if fi.Size() == 0 {
		ttl = missTTL
	}
	if time.Since(fi.ModTime()) > ttl {
		return nil, false
	}
	b, err := os.ReadFile(path)
	return b, err == nil
}

func (s *Service) fetch(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil || len(b) > maxSize {
		return nil, errors.New("too big")
	}
	if _, err := sniff(b); err != nil {
		return nil, err
	}
	return b, nil
}

// sniff accepts only raster images: an HTML challenge page isn't one, and an SVG (which
// can carry script) is never served from Arrmada's own origin.
func sniff(b []byte) (string, error) {
	if len(b) == 0 {
		return "", ErrNone
	}
	if bytes.HasPrefix(b, []byte{0, 0, 1, 0}) {
		return "image/x-icon", nil
	}
	ct := http.DetectContentType(b)
	switch ct {
	case "image/png", "image/gif", "image/jpeg", "image/webp", "image/x-icon", "image/vnd.microsoft.icon", "image/bmp":
		return ct, nil
	}
	return "", ErrNone
}
