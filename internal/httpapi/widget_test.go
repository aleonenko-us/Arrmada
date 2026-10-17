package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
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
	stored := s.deps.Settings.Get(context.Background(), keyWidgetTokenHash, "")
	if stored == "" || stored == k.Key || strings.Contains(stored, k.Key) || len(stored) != 64 {
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
	kidKey, err := s.auth.CreateAPIKey(ctx, kidU.ID, "kid's key")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, err := s.auth.CreateAPIKey(ctx, adminU.ID, "admin key")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]*httptest.ResponseRecorder{
		"requester session":  fetchWidget(s, widgetPath, nil, kid),
		"admin session":      fetchWidget(s, widgetPath, nil, admin),
		"requester API key":  fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": kidKey}),
		"admin API key":      fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": adminKey}),
		"requester bearer":   fetchWidget(s, widgetPath, map[string]string{"Authorization": "Bearer " + kidKey}),
		"nothing at all":     fetchWidget(s, widgetPath, nil),
		"requester+kid key?": fetchWidget(s, widgetPath, map[string]string{"X-Api-Key": kidKey}, kid),
	}
	for name, rec := range cases {
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), `"movies"`) {
			t.Errorf("%s: HTTP %d %s, want 401 with no data", name, rec.Code, rec.Body)
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
		"books", "disk.free_bytes", "disk.lowest_free_bytes", "disk.lowest_free_pct", "disk.total_bytes",
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

// A flood from one address is cut off; Homepage's normal polling is far below the limit.
func TestWidgetRateLimited(t *testing.T) {
	s := newRouteServer(t, nil)
	_, admin := s.user(t, "owner", auth.RoleAdmin)
	k := makeWidgetKey(t, s, admin)
	h := map[string]string{"X-Api-Key": k.Key}
	for i := 0; i < widgetPerMinute; i++ {
		if rec := fetchWidget(s, widgetPath, h); rec.Code != http.StatusOK {
			t.Fatalf("fetch %d: HTTP %d", i+1, rec.Code)
		}
	}
	if rec := fetchWidget(s, widgetPath, h); rec.Code != http.StatusTooManyRequests {
		t.Errorf("fetch past the limit: HTTP %d, want 429", rec.Code)
	}
}

// The widget key's settings stay out of a support bundle.
func TestWidgetKeyWithheldFromBundle(t *testing.T) {
	split := bundleSettings(map[string]string{keyWidgetTokenHash: strings.Repeat("a", 64), keyWidgetTokenCreated: "1700000000"})
	if len(split.doc.Included) != 0 {
		t.Errorf("included in bundle: %v", split.doc.Included)
	}
}
