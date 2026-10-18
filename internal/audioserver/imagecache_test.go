package audioserver

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const pngBytes = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR fake png body"

// testImageCache is an image cache that may reach the local test server (the real one
// only dials public addresses).
func testImageCache(t *testing.T) *imageCache {
	t.Helper()
	c := newImageCache(t.TempDir())
	c.client = newImageClient(http.DefaultTransport.(*http.Transport).Clone())
	return c
}

func serveImage(c *imageCache, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c.serve(rec, httptest.NewRequest("GET", "/cover", nil), url, "")
	return rec
}

func cachePath(c *imageCache, url string) string {
	h := sha1.Sum([]byte(url))
	return filepath.Join(c.dir, hex.EncodeToString(h[:]))
}

// P1/W1: only raster images are kept and served, by declared type and by their bytes;
// what's served can't be sniffed or run, and is cached privately.
func TestImageCacheOnlyRasterImages(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/cover.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(pngBytes))
		case "/loose.jpg":
			w.Header().Set("Content-Type", "image/jpg")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00 fake jpeg"))
		case "/evil.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/disguised.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		}
	}))
	defer srv.Close()
	c := testImageCache(t)

	rec := serveImage(c, srv.URL+"/cover.png")
	if rec.Code != 200 || rec.Body.String() != pngBytes {
		t.Fatalf("png: %d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != imageCSP || h.Get("Cache-Control") != "private, max-age=604800" {
		t.Fatalf("png headers: %v", h)
	}

	// A loose spelling of the type is fine: the bytes decide, and what's served says so.
	if rec := serveImage(c, srv.URL+"/loose.jpg"); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image/jpg: %d %v", rec.Code, rec.Header())
	}

	for _, p := range []string{"/evil.svg", "/disguised.png"} {
		if rec := serveImage(c, srv.URL+p); rec.Code != 404 {
			t.Fatalf("%s: %d, want 404", p, rec.Code)
		}
		if _, err := os.Stat(cachePath(c, srv.URL+p)); err == nil {
			t.Fatalf("%s was cached", p)
		}
	}
	// A failure is remembered: asking again within ten minutes doesn't fetch again.
	before := hits.Load()
	if rec := serveImage(c, srv.URL+"/evil.svg"); rec.Code != 404 || hits.Load() != before {
		t.Fatalf("negative cache: %d, hits %d → %d", rec.Code, before, hits.Load())
	}
	c.now = func() time.Time { return time.Now().Add(imageFailTTL + time.Minute) }
	_ = serveImage(c, srv.URL+"/evil.svg")
	if hits.Load() != before+1 {
		t.Fatalf("after the failure expired, hits %d → %d", before, hits.Load())
	}
}

// An SVG cached before types were checked is refused and dropped, not served.
func TestImageCacheDropsAnOldCachedSVG(t *testing.T) {
	c := testImageCache(t)
	url := "http://127.0.0.1:1/old.svg" // nothing listens: the re-fetch fails
	path := cachePath(c, url)
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o644)
	_ = os.WriteFile(path+".type", []byte("image/svg+xml"), 0o644)
	if rec := serveImage(c, url); rec.Code != 404 {
		t.Fatalf("old SVG served: %d %v", rec.Code, rec.Header())
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the old SVG is still cached")
	}
}

// N4: the real fetcher never connects to a private or loopback address.
func TestImageCacheRefusesPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(pngBytes))
	}))
	defer srv.Close()
	c := newImageCache(t.TempDir())
	if rec := serveImage(c, srv.URL+"/cover.png"); rec.Code != 404 || hits.Load() != 0 {
		t.Fatalf("loopback cover: %d, hits %d", rec.Code, hits.Load())
	}
}

// Several asking for the same uncached cover at once cause one fetch.
func TestImageCacheOneFetchPerURL(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(pngBytes))
	}))
	defer srv.Close()
	c := testImageCache(t)
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = serveImage(c, srv.URL+"/cover.png").Code
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
	for i, code := range codes {
		if code != 200 {
			t.Fatalf("caller %d: %d", i, code)
		}
	}
}

func TestSniffImageAVIF(t *testing.T) {
	avif := append([]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), make([]byte, 32)...)
	if got := sniffImage(avif); got != "image/avif" {
		t.Fatalf("avif sniffed as %q", got)
	}
	mif1 := append([]byte("\x00\x00\x00\x1cftypmif1\x00\x00\x00\x00mif1avifmiaf"), make([]byte, 32)...)
	if got := sniffImage(mif1); got != "image/avif" {
		t.Fatalf("avif (compatible brand) sniffed as %q", got)
	}
	if got := sniffImage([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp41")); got != "" {
		t.Fatalf("mp4 sniffed as %q", got)
	}
}
