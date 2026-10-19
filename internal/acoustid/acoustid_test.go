package acoustid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediainfo"
)

const secretKey = "s3cr3t-app-key"

func stub(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(func() string { return secretKey })
	c.base = srv.URL
	c.http = srv.Client()
	return c
}

func TestLookupSendsTheKeyInTheBody(t *testing.T) {
	var gotURL string
	var form url.Values
	c := stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":"ok","results":[
			{"id":"a1","score":0.42,"recordings":[{"id":"rec-weak"}]},
			{"id":"a2","score":0.97,"recordings":[{"id":"rec-1"},{"id":"rec-2"}]},
			{"id":"a3","score":0.81,"recordings":[{"id":"rec-3"}]}]}`)
	})
	got, err := c.Lookup(context.Background(), "FP", 241, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotURL, secretKey) {
		t.Errorf("the key is in the URL %q", gotURL)
	}
	if form.Get("client") != secretKey || form.Get("fingerprint") != "FP" || form.Get("duration") != "241" {
		t.Errorf("form %v", form)
	}
	if len(got) != 2 || got[0].ID != "a2" || got[1].ID != "a3" || len(got[0].Recordings) != 2 {
		t.Errorf("matches %+v, want the two over the score floor, best first", got)
	}
}

func TestLookupWithoutKeyCallsNothing(t *testing.T) {
	called := false
	c := stub(t, func(http.ResponseWriter, *http.Request) { called = true })
	c.key = func() string { return "" }
	if c.Enabled() {
		t.Error("enabled without a key")
	}
	if _, err := c.Lookup(context.Background(), "FP", 10, ""); !errors.Is(err, ErrNoKey) {
		t.Errorf("err = %v, want ErrNoKey", err)
	}
	if called {
		t.Error("AcoustID was called without a key")
	}
}

func TestVerify(t *testing.T) {
	c := stub(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		if form.Get("client") == "bad" {
			_, _ = io.WriteString(w, `{"status":"error","error":{"code":4,"message":"invalid API key"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","results":[]}`)
	})
	if msg, err := c.Verify(context.Background(), ""); err != nil || !strings.Contains(msg, "accepted") {
		t.Errorf("saved key: %q %v", msg, err)
	}
	_, err := c.Verify(context.Background(), "bad")
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("a bad candidate: %v", err)
	}
	if strings.Contains(fmt.Sprint(err), "bad") && strings.Contains(fmt.Sprint(err), "client=") {
		t.Errorf("the error quotes the key: %v", err)
	}
}

// A transport failure's error never carries the request (whose body holds the key).
func TestLookupErrorKeepsTheKeyOut(t *testing.T) {
	c := New(func() string { return secretKey })
	c.base = "http://127.0.0.1:1" // nothing listens
	_, err := c.Lookup(context.Background(), "FP", 10, "")
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Errorf("err = %v", err)
	}
}

func TestPacing(t *testing.T) {
	c := New(func() string { return secretKey })
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := c.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// The fake clock never moves, so the second and third waits each sleep a full gap.
	if el := time.Since(start); el < 2*requestGap-20*time.Millisecond {
		t.Errorf("three requests took %v, want at least two gaps (%v)", el, 2*requestGap)
	}
}

// One fingerprint and one lookup per file, however often the import asks; a failure is
// asked again only after a while.
func TestFingerprinterCaches(t *testing.T) {
	var lookups int
	c := stub(t, func(w http.ResponseWriter, r *http.Request) {
		lookups++
		_, _ = io.WriteString(w, `{"status":"ok","results":[{"id":"a","score":0.9,"recordings":[{"id":"rec-1"}]}]}`)
	})
	dir := t.TempDir()
	p := filepath.Join(dir, "01.flac")
	if err := os.WriteFile(p, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	fps := 0
	f := NewFingerprinter(c)
	f.fingerprint = func(context.Context, string) (string, error) { fps++; return "FP", nil }
	f.duration = func(context.Context, string) (int, error) { return 200, nil }
	f.available = func() bool { return true }
	if !f.Enabled() {
		t.Fatal("not enabled with a key and a fingerprinter")
	}
	for i := 0; i < 3; i++ {
		recs, err := f.Recordings(context.Background(), p)
		if err != nil || len(recs) != 1 || recs[0] != "rec-1" {
			t.Fatalf("recordings %v %v", recs, err)
		}
	}
	if fps != 1 || lookups != 1 {
		t.Errorf("fingerprinted %d times, looked up %d times; want once each", fps, lookups)
	}

	broken := filepath.Join(dir, "02.flac")
	_ = os.WriteFile(broken, []byte("x"), 0o644)
	now := time.Unix(5000, 0)
	f.now = func() time.Time { return now }
	f.fingerprint = func(context.Context, string) (string, error) { fps++; return "", errors.New("decode failed") }
	before := fps
	for i := 0; i < 3; i++ {
		if _, err := f.Recordings(context.Background(), broken); err == nil {
			t.Fatal("a failed fingerprint reported success")
		}
	}
	if fps-before != 1 {
		t.Errorf("a failure was retried %d times at once", fps-before-1)
	}
	now = now.Add(failureTTL + time.Minute)
	_, _ = f.Recordings(context.Background(), broken)
	if fps-before != 2 {
		t.Error("a failure wasn't tried again after its wait")
	}
}

// The bundled ffmpeg makes the fingerprint (no fpcalc needed). Real ffmpeg on a generated
// tone in a temp dir; skipped where ffmpeg isn't installed unless ARRMADA_FFMPEG_IT=1.
func TestFingerprintGeneratedTone(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		if os.Getenv("ARRMADA_FFMPEG_IT") == "1" {
			t.Fatal("ARRMADA_FFMPEG_IT=1 but ffmpeg isn't on PATH")
		}
		t.Skip("ffmpeg not installed")
	}
	ctx := context.Background()
	if !mediainfo.FingerprintAvailable(ctx) {
		t.Fatal("this ffmpeg has no chromaprint muxer")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "tone.flac")
	if b, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=20", "-ac", "2", "-c:a", "flac", out).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	before, _ := os.ReadFile(out)
	fp, err := mediainfo.Fingerprint(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) < 20 || strings.ContainsAny(fp, "+/= \n") {
		t.Errorf("fingerprint %q, want URL-safe base64", fp)
	}
	again, err := mediainfo.Fingerprint(ctx, out)
	if err != nil || again != fp {
		t.Errorf("a second fingerprint differs: %v", err)
	}
	after, _ := os.ReadFile(out)
	if string(before) != string(after) {
		t.Error("fingerprinting changed the file")
	}
}
