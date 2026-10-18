package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/audioserver"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/listening"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The catalogue signs readers in itself, so the router lets every /opds route through
// from outside the network, signed in to Arrmada or not.
func TestExternalGateLetsOPDSThrough(t *testing.T) {
	rt := testRouter(t, true)
	for _, p := range []string{"/opds", "/opds/", "/opds/new", "/opds/mine", "/opds/authors/12", "/opds/series/s1",
		"/opds/search?q=x", "/opds/opensearch.xml", "/opds/books/12/file", "/opds/books/12/cover"} {
		if rec := callFromOutside(rt, "GET", p, nil); rec.Code != sentinelStatus {
			t.Errorf("GET %s from the internet: HTTP %d, want it let through to the catalogue", p, rec.Code)
		}
	}
}

// opdsHarness is the real router with the books service and the audiobook server behind
// it: one book with an EPUB, a requester with an audiobook password, and every log line
// captured.
type opdsHarness struct {
	rs     *routeServer
	logs   *syncBuffer
	bookID int64
	title  string
}

func newOPDSHarness(t *testing.T) *opdsHarness {
	t.Helper()
	h := &opdsHarness{logs: &syncBuffer{}, title: "Zebrafish Secrets"}
	log := slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h.rs = newRouteServer(t, func(d *Deps) {
		d.Log = log
		ctx := context.Background()
		db := d.Store.DB()
		bs := books.NewService(db, nil, log)
		added, _ := bs.AddWorks(ctx, []metadata.BookResult{{Key: "hc:7", Title: h.title, Author: "Ann Author"}}, "", true)
		if len(added) != 1 {
			t.Fatal("book not added")
		}
		h.bookID = added[0].ID
		p := filepath.Join(t.TempDir(), "Zebrafish Secrets.epub")
		if err := os.WriteFile(p, []byte("epub bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := bs.MarkImported(ctx, h.bookID, books.KindEbook, p, "EPUB", 10, 1); err != nil {
			t.Fatal(err)
		}
		// An uploaded cover, as Books' cover upload stores it.
		data := t.TempDir()
		if err := os.MkdirAll(filepath.Join(data, "covers"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "covers", fmt.Sprintf("book-%d.png", h.bookID)), []byte("fake png"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := bs.SetCover(ctx, h.bookID, fmt.Sprintf("/api/v1/books/%d/cover-image?v=1", h.bookID)); err != nil {
			t.Fatal(err)
		}
		d.Books = bs
		d.AudioServer = audioserver.New(audioserver.Options{DB: db, Books: bs, Listen: listening.NewStore(db), Users: d.Auth,
			Settings: d.Settings, Log: log, FFprobe: "ffprobe-not-installed", DataDir: data})
	})
	return h
}

func (h *opdsHarness) get(path, user, pass, remote string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://arrmada.local"+path, nil)
	r.RemoteAddr = remote
	if user != "" {
		r.SetBasicAuth(user, pass)
	}
	if c != nil {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.rs.h.ServeHTTP(rec, r)
	return rec
}

const (
	lanAddr      = "192.168.1.20:5000"
	internetAddr = "8.8.8.8:5000"
)

// From the internet through the whole middleware chain, a reader browses and downloads,
// and the log holds route patterns only: no book id, no title, no search term, no
// password and no Authorization header.
func TestOPDSEndToEndLogsNoBooks(t *testing.T) {
	h := newOPDSHarness(t)
	u, cookie := h.rs.user(t, "kid", auth.RoleRequester)
	if err := h.rs.deps.AudioServer.Accounts.SetPassword(context.Background(), u.ID, "kid-reads-1", false); err != nil {
		t.Fatal(err)
	}
	// An Arrmada session alone doesn't open the catalogue, nor does the Arrmada password.
	if rec := h.get("/opds", "", "", internetAddr, cookie); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("session cookie only: HTTP %d, want a Basic challenge", rec.Code)
	}
	if rec := h.get("/opds", "kid", "password123", internetAddr, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("Arrmada password: HTTP %d, want 401", rec.Code)
	}
	before := len(h.logs.String())

	for _, p := range []string{"/opds", "/opds/new", "/opds/search?q=zebrafish", "/opds/authors"} {
		rec := h.get(p, "kid", "kid-reads-1", internetAddr, nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/atom+xml;profile=opds-catalog") {
			t.Fatalf("GET %s: HTTP %d %s", p, rec.Code, rec.Header().Get("Content-Type"))
		}
		if p == "/opds/search?q=zebrafish" && !strings.Contains(rec.Body.String(), h.title) {
			t.Errorf("search didn't find the book: %s", rec.Body)
		}
	}
	file := fmt.Sprintf("/opds/books/%d/file", h.bookID)
	rec := h.get(file, "kid", "kid-reads-1", internetAddr, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "epub bytes" || rec.Header().Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("download: HTTP %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if rec := h.get(fmt.Sprintf("/opds/books/%d/cover", h.bookID), "kid", "kid-reads-1", internetAddr, nil); rec.Code != http.StatusOK || rec.Body.String() != "fake png" {
		t.Errorf("cover: HTTP %d %q", rec.Code, rec.Body)
	}

	logged := h.logs.String()[before:]
	if !strings.Contains(logged, "GET /opds/books/{id}/file") || !strings.Contains(logged, "GET /opds/search") {
		t.Errorf("the request log should name the route patterns:\n%s", logged)
	}
	for _, leak := range []string{fmt.Sprintf("/opds/books/%d", h.bookID), h.title, "zebrafish", "Zebrafish", "kid-reads-1", "Basic ", "Authorization"} {
		if strings.Contains(logged, leak) {
			t.Errorf("the log mentions %q:\n%s", leak, logged)
		}
	}
}

// Wrong e-reader passwords go through the login limiter: ten from one address in fifteen
// minutes, then even the right one waits. From outside, one username backs off after
// five wherever the guesses come from.
func TestOPDSFailuresAreThrottled(t *testing.T) {
	h := newOPDSHarness(t)
	u, _ := h.rs.user(t, "kid", auth.RoleRequester)
	if err := h.rs.deps.AudioServer.Accounts.SetPassword(context.Background(), u.ID, "kid-reads-1", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if rec := h.get("/opds", "kid", "guess-guess", lanAddr, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: HTTP %d", i, rec.Code)
		}
	}
	rec := h.get("/opds", "kid", "kid-reads-1", lanAddr, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after ten wrong passwords: HTTP %d, want 429 with Retry-After", rec.Code)
	}
	// Another address on the LAN isn't held up by those.
	if rec := h.get("/opds", "kid", "kid-reads-1", "192.168.1.21:5000", nil); rec.Code != http.StatusOK {
		t.Errorf("another LAN address: HTTP %d", rec.Code)
	}

	// From the internet, five wrong passwords from five addresses hold the username.
	mom, _ := h.rs.user(t, "mom", auth.RoleRequester)
	if err := h.rs.deps.AudioServer.Accounts.SetPassword(context.Background(), mom.ID, "mom-reads-1", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if rec := h.get("/opds", "mom", "guess-guess", fmt.Sprintf("8.8.4.%d:5000", i+1), nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("outside guess %d: HTTP %d", i, rec.Code)
		}
	}
	if rec := h.get("/opds", "mom", "mom-reads-1", "8.8.4.99:5000", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("username after five outside failures: HTTP %d, want 429", rec.Code)
	}
	// At home she isn't held up by strangers guessing her name.
	if rec := h.get("/opds", "mom", "mom-reads-1", "192.168.1.30:5000", nil); rec.Code != http.StatusOK {
		t.Errorf("mom at home: HTTP %d", rec.Code)
	}
}

// Someone who knows a username can't lock that person's e-reader out from outside: an
// address the person signed in from before carries on through the username's pause, a
// right password elsewhere doesn't wipe out the guesses, and the guesser still waits.
func TestOPDSGuesserCantLockOutTheReader(t *testing.T) {
	h := newOPDSHarness(t)
	u, _ := h.rs.user(t, "dad", auth.RoleRequester)
	if err := h.rs.deps.AudioServer.Accounts.SetPassword(context.Background(), u.ID, "dad-reads-1", false); err != nil {
		t.Fatal(err)
	}
	const reader = "8.8.7.7:5000" // dad's e-reader on his phone's network
	if rec := h.get("/opds", "dad", "dad-reads-1", reader, nil); rec.Code != http.StatusOK {
		t.Fatalf("first sign-in: HTTP %d", rec.Code)
	}
	for i := 0; i < 8; i++ {
		_ = h.get("/opds", "dad", "guess-guess", fmt.Sprintf("8.8.5.%d:5000", i+1), nil)
		// His reader keeps polling the catalogue throughout.
		if rec := h.get("/opds", "dad", "dad-reads-1", reader, nil); rec.Code != http.StatusOK {
			t.Fatalf("reader during guess %d: HTTP %d, want 200", i, rec.Code)
		}
	}
	// The guesser, at a new address, is still held by the username's pause.
	if rec := h.get("/opds", "dad", "guess-guess", "8.8.5.99:5000", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("guesser after eight failures: HTTP %d, want 429", rec.Code)
	}
}

// The audiobook server's /login shares the e-readers' limits: wrong passwords there count
// for the catalogue too, and a listening app's sign-in from a known address goes through.
func TestAudioServerLoginSharesTheOPDSLimits(t *testing.T) {
	h := newOPDSHarness(t)
	u, _ := h.rs.user(t, "gran", auth.RoleRequester)
	if err := h.rs.deps.AudioServer.Accounts.SetPassword(context.Background(), u.ID, "gran-listens", false); err != nil {
		t.Fatal(err)
	}
	if err := h.rs.deps.AudioServer.SetAllowed(context.Background(), u.ID, true); err != nil {
		t.Fatal(err)
	}
	abs := h.rs.deps.AudioServer.Handler()
	login := func(pass, remote string) int {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(`{"username":"gran","password":"`+pass+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remote
		rec := httptest.NewRecorder()
		abs.ServeHTTP(rec, r)
		return rec.Code
	}
	// Her phone has signed in from outside before.
	if code := login("gran-listens", "8.8.6.6:5000"); code != http.StatusOK {
		t.Fatalf("outside sign-in: HTTP %d", code)
	}
	// Lissen at home signs in again and again: successes never use anything up.
	for i := 0; i < 15; i++ {
		if code := login("gran-listens", lanAddr); code != http.StatusOK {
			t.Fatalf("sign-in %d at home: HTTP %d", i, code)
		}
	}
	// Ten wrong ones from one address, half through each door: the address is blocked
	// on both, even with the right password.
	for i := 0; i < 5; i++ {
		if code := login("wrong-wrong", "192.168.1.40:5000"); code != http.StatusUnauthorized {
			t.Fatalf("wrong %d: HTTP %d", i, code)
		}
		if rec := h.get("/opds", "gran", "wrong-wrong", "192.168.1.40:5000", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong reader %d: HTTP %d", i, rec.Code)
		}
	}
	if code := login("gran-listens", "192.168.1.40:5000"); code != http.StatusTooManyRequests {
		t.Errorf("listening app after ten shared failures: HTTP %d, want 429", code)
	}
	if rec := h.get("/opds", "gran", "gran-listens", "192.168.1.40:5000", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("reader after ten shared failures: HTTP %d, want 429", rec.Code)
	}
	// Strangers outside guess her name too; her phone's known address carries on.
	for i := 0; i < 6; i++ {
		_ = login("wrong-wrong", fmt.Sprintf("8.8.3.%d:5000", i+1))
	}
	if code := login("gran-listens", "8.8.6.6:5000"); code != http.StatusOK {
		t.Errorf("known address during guessing: HTTP %d, want 200", code)
	}
	if code := login("gran-listens", "8.8.9.9:5000"); code != http.StatusTooManyRequests {
		t.Errorf("new outside address during the pause: HTTP %d, want 429", code)
	}
}

// With the Books module off, /opds is gone (404), not a sign-in prompt.
func TestOPDSBooksModuleOff(t *testing.T) {
	h := newOPDSHarness(t)
	if err := h.rs.deps.Settings.Set(context.Background(), settings.KeyModuleBooks, "false"); err != nil {
		t.Fatal(err)
	}
	if rec := h.get("/opds", "", "", lanAddr, nil); rec.Code != http.StatusNotFound {
		t.Errorf("Books off: HTTP %d, want 404", rec.Code)
	}
}
