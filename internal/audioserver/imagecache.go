package audioserver

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
	"github.com/tristenlammi/arrmada/internal/netsafe"
)

// imageCache fetches remote cover and author images once and serves them from disk, so
// a phone scrolling the library doesn't hit the catalogue's image hosts every time.
//
// The URLs aren't all the catalogue's: a book request's cover, a manager's pick, an old
// row. So what it fetches and what it serves are both held to plain raster images:
//   - only public addresses are dialled (netsafe.PublicTransport checks the address at
//     connect time, redirects and DNS answers included), so a cover URL can't reach
//     Arrmada's own network;
//   - only JPEG, PNG, WebP, GIF and AVIF are kept, by the declared type and by what the
//     bytes say. An SVG is a script-capable document on Arrmada's own origin, and is never
//     stored or served;
//   - served images carry nosniff and a sandboxing CSP, so even a bad file can't run.
//
// A URL that failed isn't tried again for a while (imageFailTTL), and only one fetch per
// URL runs at a time, without holding up fetches of other URLs.
type imageCache struct {
	dir    string
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	inflight map[string]*imageFetch
	failed   map[string]time.Time
}

// imageFetch is one URL's fetch in progress; the callers that ask meanwhile wait on done.
type imageFetch struct {
	done chan struct{}
	err  error
}

const (
	imageFailTTL  = 10 * time.Minute
	imageMaxBytes = 10 << 20
	// imageMaxFailed bounds the failure memory; past it, expired entries go, then all.
	imageMaxFailed = 2048
	// imageCSP stops a served file from running anything or loading anything, whatever it is.
	imageCSP = "default-src 'none'; sandbox"
)

// imageTypes are the only image types fetched, stored and served.
var imageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "image/avif": true,
}

var (
	errNotAnImage    = errors.New("not a JPEG, PNG, WebP, GIF or AVIF image")
	errImageTooLarge = errors.New("image too large")
	errImageRecent   = errors.New("image failed recently")
)

func newImageCache(dir string) *imageCache {
	return &imageCache{
		dir: dir, client: newImageClient(netsafe.PublicTransport()), now: time.Now,
		inflight: map[string]*imageFetch{}, failed: map[string]time.Time{},
	}
}

// newImageClient is the image fetcher's client over rt (tests pass one that may reach a
// local server): a bounded wait and a few web redirects at most.
func newImageClient(rt http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: httpua.Transport(rt),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirected to a non-web address")
			}
			return nil
		},
	}
}

func (c *imageCache) serve(w http.ResponseWriter, r *http.Request, url, cache string) {
	h := sha1.Sum([]byte(url))
	path := filepath.Join(c.dir, hex.EncodeToString(h[:]))
	ct, ok := c.cached(path)
	if !ok {
		if err := c.get(r.Context(), url, path); err != nil {
			writeError(w, http.StatusNotFound, "Image unavailable")
			return
		}
		if ct, ok = c.cached(path); !ok {
			writeError(w, http.StatusNotFound, "Image unavailable")
			return
		}
	}
	// Covers come through someone's sign-in, so only their own browser keeps a copy.
	w.Header().Set("Cache-Control", firstNonEmpty(cache, "private, max-age=604800"))
	w.Header().Set("Content-Type", ct)
	setImageGuards(w)
	http.ServeFile(w, r, path)
}

// setImageGuards are the headers every served image carries: no sniffing it into
// something else, and a sandbox in case it's opened as a page.
func setImageGuards(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", imageCSP)
}

// cached returns a cached image's type when the file is there and it is, by its stored
// type and by its bytes, an allowed image. Anything else (an SVG cached before types were
// checked, a file missing its type) is dropped, so it's fetched and checked again.
func (c *imageCache) cached(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Close()
	if raw, err := os.ReadFile(path + ".type"); err == nil {
		mt, _, err := mime.ParseMediaType(strings.TrimSpace(string(raw)))
		if err == nil && imageTypes[mt] && sniffImage(head[:n]) != "" {
			return mt, true
		}
	}
	_ = os.Remove(path)
	_ = os.Remove(path + ".type")
	return "", false
}

// get fetches url into path once: a URL that failed lately fails at once, and a fetch
// already running for it is waited on rather than repeated.
func (c *imageCache) get(ctx context.Context, url, path string) error {
	c.mu.Lock()
	if at, ok := c.failed[url]; ok {
		if c.now().Sub(at) < imageFailTTL {
			c.mu.Unlock()
			return errImageRecent
		}
		delete(c.failed, url)
	}
	if f, ok := c.inflight[url]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &imageFetch{done: make(chan struct{})}
	c.inflight[url] = f
	c.mu.Unlock()

	// Others may be waiting on this fetch: the first asker leaving doesn't cancel it (the
	// client's timeout still bounds it).
	f.err = c.fetch(context.WithoutCancel(ctx), url, path)
	c.mu.Lock()
	delete(c.inflight, url)
	if f.err != nil {
		c.noteFailure(url)
	}
	c.mu.Unlock()
	close(f.done)
	return f.err
}

// noteFailure remembers a failed URL. Callers hold c.mu.
func (c *imageCache) noteFailure(url string) {
	now := c.now()
	if len(c.failed) >= imageMaxFailed {
		for u, at := range c.failed {
			if now.Sub(at) >= imageFailTTL {
				delete(c.failed, u)
			}
		}
		if len(c.failed) >= imageMaxFailed {
			c.failed = map[string]time.Time{}
		}
	}
	c.failed[url] = now
}

func (c *imageCache) fetch(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image fetch: %s", resp.Status)
	}
	declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !imageTypes[declared] {
		return errNotAnImage
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > imageMaxBytes {
		return errImageTooLarge
	}
	// What the bytes are is what's stored and served, not what the server called them.
	ct := sniffImage(body)
	if ct == "" {
		return errNotAnImage
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	// The type goes in first, so a cached image is never without one.
	if err := writeAtomic(path+".type", []byte(ct)); err != nil {
		return err
	}
	return writeAtomic(path, body)
}

// writeAtomic writes data to path through a temp file and a rename.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// sniffImage is the allowed image type b's first bytes say it is, or "".
func sniffImage(b []byte) string {
	if ct := http.DetectContentType(b); imageTypes[ct] {
		return ct
	}
	if isAVIF(b) {
		return "image/avif"
	}
	return ""
}

// isAVIF reads an ISO-BMFF ftyp box for the avif/avis brand (major or compatible).
func isAVIF(b []byte) bool {
	if len(b) < 16 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	size := int(binary.BigEndian.Uint32(b[:4]))
	if size < 16 || size > len(b) {
		size = min(len(b), 64)
	}
	brand := func(x []byte) bool { return bytes.Equal(x, []byte("avif")) || bytes.Equal(x, []byte("avis")) }
	if brand(b[8:12]) {
		return true
	}
	for i := 16; i+4 <= size; i += 4 {
		if brand(b[i : i+4]) {
			return true
		}
	}
	return false
}
