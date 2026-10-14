package plexscan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// batchClient is a server that takes several folders in one request (Jellyfin's
// /Library/Media/Updated).
type batchClient struct {
	fakePlex
	mu      sync.Mutex
	batches [][]string
	failB   error
}

func (b *batchClient) RefreshPaths(_ context.Context, paths []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failB != nil {
		return b.failB
	}
	b.batches = append(b.batches, append([]string(nil), paths...))
	return nil
}

func newBatchHarness(t *testing.T) (*Scanner, *batchClient, *fakeSettings, *time.Time) {
	t.Helper()
	c := &batchClient{fakePlex: fakePlex{libs: []plex.Library{
		{Key: "lib-m", Title: "Films", Type: "movie", Locations: []string{"/media/Movies"}},
		{Key: "lib-t", Title: "Shows", Type: "show", Locations: []string{"/media/TV"}},
	}}}
	set := &fakeSettings{m: map[string]string{}}
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := New(Options{
		Client:     func(context.Context) Client { return c },
		Configured: func(context.Context) bool { return true },
		Settings:   set,
		Roots: func(context.Context) map[string]string {
			return map[string]string{KindMovie: "/movies", KindShow: "/tv"}
		},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Keys:   Keys{Enabled: "jellyfin_scan_on_import", PathMap: "jellyfin_path_map"},
		Server: "Jellyfin",
	})
	s.now = func() time.Time { return clock }
	s.exists = func(string) bool { return true }
	return s, c, set, &clock
}

// Folders that come due together go out in one call, each still counted as its own scan
// (status, the per-folder minute, the listeners).
func TestBatchClientGetsOneCall(t *testing.T) {
	s, c, set, clock := newBatchHarness(t)
	if err := s.Save(context.Background(), true, []PathMap{{From: "/movies", To: "/media/Movies"}, {From: "/tv", To: "/media/TV"}}); err != nil {
		t.Fatal(err)
	}
	if set.m["jellyfin_path_map"] == "" || set.m["jellyfin_scan_on_import"] != "true" || set.m[KeyPathMap] != "" || set.m[KeyEnabled] != "" {
		t.Fatalf("settings went to the wrong keys: %v", set.m)
	}
	var heard []string
	s.OnRefreshed(func(kind, sec, p string) { heard = append(heard, kind+"|"+sec+"|"+p) })
	s.Request(KindMovie, "/movies/Heat (1995)")
	s.Request(KindMovie, "/movies/Alien (1979)")
	s.Request(KindShow, "/tv/The Bear (2022)")
	*clock = clock.Add(defaultDebounce)
	s.runDue(context.Background())
	if len(c.batches) != 1 {
		t.Fatalf("batches = %v, want one call", c.batches)
	}
	got := append([]string(nil), c.batches[0]...)
	sort.Strings(got)
	want := []string{"/media/Movies/Alien (1979)", "/media/Movies/Heat (1995)", "/media/TV/The Bear (2022)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch = %v", got)
	}
	if len(c.got()) != 0 {
		t.Fatalf("single refreshes sent too: %v", c.got())
	}
	if len(heard) != 3 || s.LastScan().Error != "" || s.Pending() != 0 {
		t.Fatalf("listeners %v, last %+v, pending %d", heard, s.LastScan(), s.Pending())
	}
	// The same folder again inside the minute waits it out, like a single scan.
	s.Request(KindMovie, "/movies/Heat (1995)")
	*clock = clock.Add(defaultDebounce)
	s.runDue(context.Background())
	if len(c.batches) != 1 || s.Pending() != 1 {
		t.Fatalf("sent again inside the minute: %v (pending %d)", c.batches, s.Pending())
	}
}

// A failed batch puts every folder back for a retry, and messages name the server.
func TestBatchFailureRetriesEach(t *testing.T) {
	s, c, _, clock := newBatchHarness(t)
	c.failB = errors.New("jellyfin isn't reachable")
	s.Request(KindMovie, "/media/Movies/Heat (1995)")
	s.Request(KindMovie, "/media/Movies/Alien (1979)")
	*clock = clock.Add(defaultDebounce)
	s.runDue(context.Background())
	if s.Pending() != 2 || s.LastScan().Error == "" {
		t.Fatalf("pending %d, last %+v", s.Pending(), s.LastScan())
	}
	c.failB = nil
	*clock = clock.Add(retryAfter)
	s.runDue(context.Background())
	if len(c.batches) != 1 || len(c.batches[0]) != 2 || s.Pending() != 0 {
		t.Fatalf("retry batches %v pending %d", c.batches, s.Pending())
	}

	// Not connected: the error and the notes say Jellyfin, not Plex.
	off := New(Options{Server: "Jellyfin", Configured: func(context.Context) bool { return false }, Client: func(context.Context) Client { return c }})
	if _, err := off.ScanNow(context.Background(), KindMovie, false); err == nil || !strings.Contains(err.Error(), "Jellyfin isn't connected") {
		t.Fatalf("ScanNow unconfigured = %v", err)
	}
	c.libs = c.libs[1:] // no movie library
	rv, err := s.ScanNow(context.Background(), KindMovie, false)
	if err != nil || rv.How != HowNone || !strings.Contains(rv.Note, "Jellyfin has no movie library") {
		t.Fatalf("no movie library: %+v %v", rv, err)
	}
	if err := s.Save(context.Background(), true, []PathMap{{From: "/movies", To: ""}}); err == nil || !strings.Contains(err.Error(), "Jellyfin folder") {
		t.Fatalf("half mapping = %v", err)
	}
}

func TestPathKey(t *testing.T) {
	for in, want := range map[string]string{
		"/media/Movies/":           "/media/Movies",
		"/media//Movies/Heat":      "/media/Movies/Heat",
		`D:\Media\Movies\Heat`:     "d:/media/movies/heat",
		`\\nas\share\Movies`:       "//nas/share/movies",
		"/media/Movies/Back\\Door": "/media/Movies/Back\\Door",
	} {
		if got := PathKey(in); got != want {
			t.Errorf("PathKey(%q) = %q, want %q", in, got, want)
		}
	}
}
