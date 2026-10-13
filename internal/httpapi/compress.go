package httpapi

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMinBytes is the smallest JSON answer worth compressing: below about a packet the
// gzip header and the CPU buy nothing.
const gzipMinBytes = 1400

// gzipLevel trades a little size for speed: the 2.5 MB books list compresses to ~20%
// in ~20 ms at level 4, where the default level takes over three times as long for
// another 2%.
const gzipLevel = 4

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(nil, gzipLevel)
	return w
}}

// compressJSON gzips the API's larger JSON answers for a browser that accepts it. The
// library lists are megabytes of JSON for a big library (2,500 movies is ~1.2 MB, 1,500
// books ~2.5 MB) and compress five to six times: on Wi-Fi or from outside the house the
// transfer, not the server, was most of the wait.
//
// Only GETs under /api/v1/ (never the websocket) and only JSON: files, streams and
// downloads pass through untouched, as does anything that already set its own
// Content-Encoding.
func (a *api) compressJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/api/v1/ws" ||
			r.Header.Get("Upgrade") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipJSONWriter{ResponseWriter: w}
		next.ServeHTTP(gw, r)
		gw.finish()
	})
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip (and not at q=0).
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if strings.EqualFold(strings.TrimSpace(k), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q <= 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// gzipJSONWriter holds the status back until the first write, when it can see the
// Content-Type and the size: writeJSON hands over the whole encoded body in one Write.
type gzipJSONWriter struct {
	http.ResponseWriter
	status  int
	decided bool
	gz      *gzip.Writer
}

func (g *gzipJSONWriter) WriteHeader(code int) {
	if g.status == 0 {
		g.status = code
	}
}

func (g *gzipJSONWriter) Write(p []byte) (int, error) {
	if !g.decided {
		g.decide(len(p))
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// decide picks gzip or plain for the whole answer and sends the held status.
func (g *gzipJSONWriter) decide(firstWrite int) {
	g.decided = true
	if g.status == 0 {
		g.status = http.StatusOK
	}
	h := g.Header()
	if firstWrite >= gzipMinBytes && g.status != http.StatusNoContent && g.status != http.StatusNotModified &&
		h.Get("Content-Encoding") == "" && strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		h.Add("Vary", "Accept-Encoding")
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(g.ResponseWriter)
		g.gz = gz
	}
	g.ResponseWriter.WriteHeader(g.status)
}

// Flush sends what's written so far (and the status, if nothing was).
func (g *gzipJSONWriter) Flush() {
	if !g.decided {
		g.decide(0)
	}
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish ends the gzip stream, or sends a status that had no body after it.
func (g *gzipJSONWriter) finish() {
	if !g.decided {
		if g.status == 0 {
			return // nothing written at all: net/http sends its own 200
		}
		g.decide(0)
	}
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(nil)
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// Unwrap lets http.ResponseController reach the connection underneath.
func (g *gzipJSONWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
