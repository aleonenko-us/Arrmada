package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/diskspace"
)

type madeWidgetKey struct {
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	Key       string `json:"key"`
	URL       string `json:"url"`
}

func makeWidgetKey(t *testing.T, s *routeServer, admin *http.Cookie) madeWidgetKey {
	t.Helper()
	rec := s.do("POST", "/api/v1/system/widget-key", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("make widget key: HTTP %d %s", rec.Code, rec.Body)
	}
	var k madeWidgetKey
	if err := json.Unmarshal(rec.Body.Bytes(), &k); err != nil {
		t.Fatal(err)
	}
	return k
}

// fetchWidget calls the widget from the LAN with headers (name → value).
func fetchWidget(s *routeServer, path string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://arrmada.local"+path, nil)
	r.RemoteAddr = "192.168.1.20:5000"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

const widgetPath = "/api/v1/widget/homepage"

func TestWidgetKeyLifecycle(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)

	if rec := s.do("GET", "/api/v1/system/widget-key", admin); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("before: HTTP %d %s", rec.Code, rec.Body)
	}
	// No key made yet: nothing opens the widget.
	if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": "arrw_guess"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("no key made: HTTP %d, want 401", rec.Code)
	}

	k := makeWidgetKey(t, s, admin)
	if !k.Enabled || !strings.HasPrefix(k.Key, widgetTokenPrefix) || len(k.Key) < 40 || !strings.HasSuffix(k.URL, widgetPath) {
		t.Fatalf("made = %+v", k)
	}
	// Only a hash is kept, and the state never gives the key back.
	stored := s.deps.Settings.Get(context.Background(), keyWidgetToken, "")
	at, hash, _ := strings.Cut(stored, ":")
	if at != strconv.FormatInt(k.CreatedAt, 10) || hash != hashWidgetToken(k.Key) || strings.Contains(stored, k.Key) {
		t.Errorf("stored %q", stored)
	}
	if rec := s.do("GET", "/api/v1/system/widget-key", admin); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), k.Key) || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("state: HTTP %d %s", rec.Code, rec.Body)
	}

	for _, h := range []map[string]string{{"X-Api-Key": k.Key}, {"Authorization": "Bearer " + k.Key}} {
		rec := fetchWidget(s, widgetPath, h)
		if rec.Code != http.StatusOK {
			t.Fatalf("valid key %v: HTTP %d %s", h, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
		}
	}
	for _, bad := range []string{"", k.Key + "x", k.Key[:len(k.Key)-1], strings.TrimPrefix(k.Key, widgetTokenPrefix), "arr_" + strings.TrimPrefix(k.Key, widgetTokenPrefix)} {
		if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": bad}); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), `"movies"`) {
			t.Errorf("key %.12q…: HTTP %d, want 401 with no data", bad, rec.Code)
		}
	}
	// Never in the address: a key in the query string stays out of logs by not working.
	for _, q := range []string{"?key=", "?apikey=", "?api_key=", "?token="} {
		if rec := fetchWidget(s, widgetPath+q+k.Key, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("key as %s: HTTP %d, want 401", q, rec.Code)
		}
	}

	// A new key stops the old one at once.
	k2 := makeWidgetKey(t, s, admin)
	if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": k.Key}); rec.Code != http.StatusUnauthorized {
		t.Errorf("old key after reset: HTTP %d, want 401", rec.Code)
	}
	if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": k2.Key}); rec.Code != http.StatusOK {
		t.Errorf("new key: HTTP %d", rec.Code)
	}
	// Turning it off stops it too.
	if rec := s.do("DELETE", "/api/v1/system/widget-key", admin); rec.Code != http.StatusOK {
		t.Fatalf("turn off: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": k2.Key}); rec.Code != http.StatusUnauthorized {
		t.Errorf("after turning off: HTTP %d, want 401", rec.Code)
	}
}

// Making, showing and turning off the key is a system surface: admins only.
func TestWidgetKeyRoutesAreAdminOnly(t *testing.T) {
	s := newRouteServer(t, nil)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	_, kid := s.user(t, "kid", auth.RoleRequester)
	for _, m := range []string{"GET", "POST", "DELETE"} {
		for who, c := range map[string]*http.Cookie{"manager": mgr, "requester": kid} {
			if rec := s.do(m, "/api/v1/system/widget-key", c); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: HTTP %d, want 403", m, who, rec.Code)
			}
		}
		if rec := s.do(m, "/api/v1/system/widget-key", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: HTTP %d, want 401", m, rec.Code)
		}
	}
}

// Only the widget key opens the widget: no account's session or API key does, a
// requester's least of all — and an admin's doesn't either, so there's one way in.
func TestWidgetRefusesAccountCredentials(t *testing.T) {
	s := newRouteServer(t, nil)
	ctx := context.Background()
	adminU, admin := s.user(t, "owner", auth.RoleAdmin)
	kidU, kid := s.user(t, "kid", auth.RoleRequester)
	makeWidgetKey(t, s, admin)
	kidKey, _, err := s.auth.CreateAPIKey(ctx, kidU.ID, auth.NewAPIKey{Name: "kid's key", Scope: auth.KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _, err := s.auth.CreateAPIKey(ctx, adminU.ID, auth.NewAPIKey{Name: "admin key", Scope: auth.KeyScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	// Sessions and nothing at all: the widget's own 401.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"requester session": fetchWidget(s, widgetPath, nil, kid),
		"admin session":     fetchWidget(s, widgetPath, nil, admin),
		"nothing at all":    fetchWidget(s, widgetPath, nil),
	} {
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), `"movies"`) {
			t.Errorf("%s: HTTP %d %s, want 401 with no data", name, rec.Code, rec.Body)
		}
	}
	// A personal API key is the account middleware's: the widget route isn't one keys may
	// use, so it's refused (403) before the widget is even asked.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"requester API key":           fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": kidKey}),
		"admin API key":               fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": adminKey}),
		"requester bearer":            fetchWidget(s, widgetPath, map[string]string{"Authorization": "Bearer " + kidKey}),
		"requester session + its key": fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": kidKey}, kid),
		"admin key as bearer":         fetchWidget(s, widgetPath, map[string]string{"Authorization": "Bearer " + adminKey}),
	} {
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), `"movies"`) {
			t.Errorf("%s: HTTP %d %s, want 403 with no data", name, rec.Code, rec.Body)
		}
	}
	// A revoked or made-up personal key: 401 from the middleware.
	if rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": "arrk_" + strings.Repeat("z", 52)}); rec.Code != http.StatusUnauthorized {
		t.Errorf("made-up personal key: HTTP %d, want 401", rec.Code)
	}
}

// The widget key works in either header, and a personal key riding along in the other
// header doesn't get in its way or get in through it.
func TestWidgetKeyReadFromItsOwnHeaders(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	k := makeWidgetKey(t, s, admin)
	for name, h := range map[string]map[string]string{
		"X-Api-Key": {"X-Api-Key": k.Key},
		"Bearer":    {"Authorization": "Bearer " + k.Key},
		"bearer":    {"Authorization": "bearer " + k.Key},
	} {
		if rec := fetchWidget(s, widgetPath, h); rec.Code != http.StatusOK {
			t.Errorf("widget key in %s: HTTP %d %s", name, rec.Code, rec.Body)
		}
	}
	for _, v := range []string{"", "arrk_" + strings.Repeat("a", 52), "Basic " + k.Key, "Bearer arrw_"} {
		r := httptest.NewRequest("GET", widgetPath, nil)
		r.Header.Set("Authorization", v)
		if got := widgetKeyFromRequest(r); got != "" && got != "arrw_" {
			t.Errorf("Authorization %q read as widget key %q", v, got)
		}
	}
}

// The widget key opens the widget and nothing else.
func TestWidgetKeyOpensNothingElse(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	k := makeWidgetKey(t, s, admin)
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/dashboard", "/api/v1/attention", "/api/v1/system/widget-key", "/api/v1/movies", "/api/v1/requests"} {
		for _, h := range []map[string]string{{"X-Api-Key": k.Key}, {"Authorization": "Bearer " + k.Key}} {
			if rec := fetchWidget(s, path, h); rec.Code == http.StatusOK {
				t.Errorf("%s answered 200 with only the widget key", path)
			}
		}
	}
}

// LAN-only, like the rest of the app for anyone who isn't signed-in staff.
func TestWidgetIsLANOnly(t *testing.T) {
	rt := testRouter(t, true)
	if code := callFromOutside(rt, "GET", widgetPath, nil).Code; code != http.StatusForbidden {
		t.Errorf("widget from outside: HTTP %d, want 403", code)
	}
}

// Counts only: no title, no person, no path — every string in the answer is one of the
// fixed words, and the shape is the documented one (docs/homepage.md maps these fields).
func TestWidgetCountsOnly(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	kid, _ := s.user(t, "Secretkid", auth.RoleRequester)
	db := s.st.DB()
	for _, q := range []string{
		`INSERT INTO movies (tmdb_id, title, monitored, has_file) VALUES (1, 'Zorblax Returns', 1, 1), (2, 'Quuxfilm', 1, 0)`,
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 9, 'Flibbertshow', 1)`,
		`INSERT INTO books (id, ol_key, title, monitored, has_file) VALUES (1, 'OL1W', 'Grimbleton Tales', 1, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name) VALUES ('movie', 77, 'Wantedmovie', 'pending', ?, 'Secretkid')`, kid.ID); err != nil {
		t.Fatal(err)
	}
	k := makeWidgetKey(t, s, admin)
	rec := fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": k.Key})
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, secret := range []string{"Zorblax", "Quuxfilm", "Flibbert", "Grimbleton", "Wantedmovie", "Secretkid", "owner", s.deps.Config.DataDir} {
		if strings.Contains(body, secret) {
			t.Errorf("the answer names %q: %s", secret, body)
		}
	}

	var got homepageWidget
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Movies != 2 || got.Shows != 1 || got.Books != 1 || got.Wanted.Movies != 1 || got.Wanted.Books != 1 || got.Wanted.Total != 2 {
		t.Errorf("counts = %+v", got)
	}
	if got.Health.Status != "ok" || got.Queue.OK {
		t.Errorf("health %+v queue %+v (no download client: not ok)", got.Health, got.Queue)
	}

	// Walk the answer: every key is a documented field, and the only strings are the fixed
	// words (version, latest version, health status).
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	var paths []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				walk(strings.TrimPrefix(prefix+"."+k, "."), c)
			}
		case []any:
			t.Errorf("%s is a list; the widget carries no lists", prefix)
		case string:
			switch prefix {
			case "version", "latest_version", "health.status":
			default:
				t.Errorf("%s is a string (%q); only counts belong here", prefix, x)
			}
			paths = append(paths, prefix)
		default:
			paths = append(paths, prefix)
		}
	}
	walk("", doc)
	sort.Strings(paths)
	want := []string{
		"books", "disk.free_bytes", "disk.lowest_free_bytes", "disk.lowest_free_pct", "disk.ok", "disk.total_bytes",
		"episodes", "health.errors", "health.problems", "health.status", "health.warnings",
		"latest_version", "movies", "needs_you",
		"queue.down_speed", "queue.downloading", "queue.errored", "queue.ok", "queue.paused", "queue.seeding", "queue.stalled", "queue.total", "queue.up_speed",
		"requests_pending", "shows", "update_available", "version",
		"wanted.books", "wanted.episodes", "wanted.movies", "wanted.total",
	}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("fields =\n%v\nwant\n%v", paths, want)
	}
}

// fetchWidgetFrom is fetchWidget from a given LAN address.
func fetchWidgetFrom(s *routeServer, ip string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://arrmada.local"+widgetPath, nil)
	r.RemoteAddr = ip + ":5000"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	return rec
}

// Only wrong keys count: Homepage polling with the right key is never limited, and a
// misconfigured client on another address can't use up Homepage's turns.
func TestWidgetLimitsOnlyWrongKeys(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	k := makeWidgetKey(t, s, admin)
	good := map[string]string{"X-Api-Key": k.Key}
	bad := map[string]string{"X-Api-Key": widgetTokenPrefix + "wrong"}
	const homepage, other = "192.168.1.30", "192.168.1.40"
	for i := 0; i < 4*widgetFailMax; i++ {
		if rec := fetchWidgetFrom(s, homepage, good); rec.Code != http.StatusOK {
			t.Fatalf("right key, fetch %d: HTTP %d", i+1, rec.Code)
		}
	}
	for i := 0; i < widgetFailMax; i++ {
		if rec := fetchWidgetFrom(s, other, bad); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: HTTP %d, want 401", i+1, rec.Code)
		}
	}
	if rec := fetchWidgetFrom(s, other, bad); rec.Code != http.StatusTooManyRequests {
		t.Errorf("wrong key past the limit: HTTP %d, want 429", rec.Code)
	}
	if rec := fetchWidgetFrom(s, homepage, good); rec.Code != http.StatusOK {
		t.Errorf("Homepage after another address's failures: HTTP %d, want 200", rec.Code)
	}
}

// The widget key's setting stays out of a support bundle.
func TestWidgetKeyWithheldFromBundle(t *testing.T) {
	split := bundleSettings(map[string]string{keyWidgetToken: "1700000000:" + strings.Repeat("a", 64)})
	if len(split.doc.Included) != 0 {
		t.Errorf("included in bundle: %v", split.doc.Included)
	}
}

// Resets and turn-offs racing each other leave one whole record: either no key, or a key
// that one of the resets handed out, with that reset's time.
func TestWidgetKeyRotateRevokeRace(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	var (
		mu   sync.Mutex
		made = map[string]int64{}
		wg   sync.WaitGroup
	)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 2 {
				s.do("DELETE", "/api/v1/system/widget-key", admin)
				return
			}
			rec := s.do("POST", "/api/v1/system/widget-key", admin)
			var k madeWidgetKey
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &k) != nil {
				t.Errorf("reset: HTTP %d %s", rec.Code, rec.Body)
				return
			}
			mu.Lock()
			made[k.Key] = k.CreatedAt
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	stored := s.deps.Settings.Get(context.Background(), keyWidgetToken, "")
	working := 0
	for key, at := range made {
		if fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": key}).Code == http.StatusOK {
			working++
			if stored != strconv.FormatInt(at, 10)+":"+hashWidgetToken(key) {
				t.Errorf("stored %q doesn't match the working key's own record", stored)
			}
		}
	}
	switch {
	case stored == "" && working != 0:
		t.Errorf("turned off, yet %d keys work", working)
	case stored != "" && working != 1:
		t.Errorf("%d keys work, want exactly one (stored %q)", working, stored)
	}
}

// widgetAPI is an api with a widget key's worth of dependencies and a counting disk reader.
func widgetAPI(t *testing.T, measure func(context.Context) []storageVolume) *api {
	t.Helper()
	a := dashAPI(t)
	a.widget.volumes = measure
	return a
}

// One answer is reused for widgetTTL, and callers arriving together share one working-out.
func TestWidgetCacheAndSingleFlight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	a := widgetAPI(t, func(context.Context) []storageVolume {
		calls.Add(1)
		<-release
		return []storageVolume{{Roots: []string{"Movies"}, Usage: diskspace.Usage{TotalBytes: 100, FreeBytes: 40, UsedPct: 60}}}
	})
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make(chan homepageWidget, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, ok := a.cachedWidget(ctx); ok {
				results <- v
			}
		}()
	}
	time.Sleep(100 * time.Millisecond) // let them all arrive while the first is reading
	close(release)
	wg.Wait()
	close(results)
	n := 0
	for v := range results {
		n++
		if !v.Disk.OK || v.Disk.FreeBytes != 40 || v.Disk.LowestFreePct != 40 {
			t.Errorf("disk = %+v", v.Disk)
		}
	}
	if n != 8 {
		t.Errorf("%d of 8 callers got an answer", n)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("disk read %d times for 8 callers at once, want 1", got)
	}

	// Within the TTL: the same answer, nothing read again.
	a.cachedWidget(ctx)
	if got := calls.Load(); got != 1 {
		t.Errorf("disk read %d times within the TTL, want 1", got)
	}
	// Past it: worked out again.
	a.widget.mu.Lock()
	a.widget.at = time.Now().Add(-widgetTTL - time.Second)
	a.widget.mu.Unlock()
	a.cachedWidget(ctx)
	if got := calls.Load(); got != 2 {
		t.Errorf("disk read %d times after the TTL, want 2", got)
	}
}

// A disk that hangs (a dead network mount) doesn't hold the answer up, and a reading still
// stuck is never started again — they can't pile up.
func TestWidgetHungDiskDoesNotPileUp(t *testing.T) {
	var calls atomic.Int32
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	a := widgetAPI(t, func(context.Context) []storageVolume {
		calls.Add(1)
		<-hang
		return nil
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		start := time.Now()
		v, ok := a.cachedWidget(ctx)
		if !ok {
			t.Fatal("no answer")
		}
		if d := time.Since(start); d > widgetDiskWait+2*time.Second {
			t.Errorf("answer %d took %v with a hung disk", i+1, d)
		}
		if v.Disk.OK {
			t.Errorf("disk = %+v with no reading ever finished", v.Disk)
		}
		a.widget.mu.Lock()
		a.widget.at = time.Time{} // expire it
		a.widget.mu.Unlock()
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d disk readings started while one hung, want 1", got)
	}
}

// Folders on one filesystem fold together by device id even when free space moved between
// the readings; without an id, identical totals still fold.
func TestFoldVolumesByDevice(t *testing.T) {
	u := func(free uint64) diskspace.Usage {
		return diskspace.Usage{TotalBytes: 1000, FreeBytes: free, UsedBytes: 1000 - free, UsedPct: float64(1000-free) / 10}
	}
	vols := foldVolumes([]measuredRoot{
		{label: "Movies", path: "/m", usage: u(400), dev: 7, devOK: true},
		{label: "TV", path: "/t", usage: u(399), dev: 7, devOK: true}, // a download wrote in between
		{label: "Downloads", path: "/d", usage: u(900), dev: 9, devOK: true},
		{label: "Ebooks", path: "/e", usage: u(500)},
		{label: "Music", path: "/u", usage: u(500)},
	})
	if len(vols) != 3 {
		t.Fatalf("got %d volumes, want 3: %+v", len(vols), vols)
	}
	if strings.Join(vols[0].Roots, ",") != "Movies,TV" || strings.Join(vols[1].Roots, ",") != "Ebooks,Music" || strings.Join(vols[2].Roots, ",") != "Downloads" {
		t.Errorf("roots = %v / %v / %v", vols[0].Roots, vols[1].Roots, vols[2].Roots)
	}
	if d := diskTotals(vols); d.FreeBytes != 400+500+900 || d.TotalBytes != 3000 || d.LowestFreeBytes != 400 {
		t.Errorf("totals = %+v (one filesystem counted twice?)", d)
	}
}

// An admin on an outside address gets a placeholder for Arrmada's address: Homepage can
// only use a LAN one.
func TestWidgetKeyOutsideAddressPlaceholder(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	call := func(method, ip string) map[string]any {
		r := httptest.NewRequest(method, "https://arrmada.example.com/api/v1/system/widget-key", nil)
		r.RemoteAddr = ip + ":5000"
		r.AddCookie(admin)
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s from %s: HTTP %d %s", method, ip, rec.Code, rec.Body)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	out := call("POST", "203.0.113.9")
	if out["outside"] != true || out["url"] != widgetPlaceholderOrigin+widgetPath {
		t.Errorf("from outside: %v", out)
	}
	if st := call("GET", "203.0.113.9"); st["outside"] != true {
		t.Errorf("state from outside: %v", st)
	}
	home := call("POST", "192.168.1.20")
	if home["outside"] != false || home["url"] != "https://arrmada.example.com"+widgetPath {
		t.Errorf("from home: %v", home)
	}
}
