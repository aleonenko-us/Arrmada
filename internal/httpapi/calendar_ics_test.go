package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

func TestICSFormat(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	long := strings.Repeat("Ünïcödé ", 20) // multi-byte, well past 75 octets
	items := []CalendarItem{
		{Date: "2026-10-20", Type: "episode", Title: "Harbour, Lights; \\ Night", RefID: 11, Season: 2, Episode: 5, EpisodeTitle: "The\nBreakwater"},
		{Date: "2026-12-31", Type: "movie", Title: long, RefID: 21, Year: 2026},
		{Date: "not a date", Type: "movie", Title: "skipped", RefID: 22},
	}
	var b bytes.Buffer
	if err := writeICS(&b, items, "arrmada.example.com", now); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(out, "END:VCALENDAR\r\n") {
		t.Fatalf("not a calendar:\n%s", out)
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Error("a bare LF: every line must end CRLF")
	}
	for _, l := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(l) > 75 {
			t.Errorf("line of %d octets: %q", len(l), l)
		}
		if !utf8.ValidString(l) {
			t.Errorf("a fold split a character: %q", l)
		}
	}
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	for _, want := range []string{
		"X-WR-CALNAME:Arrmada",
		"UID:arrmada-ep-11-2x5@arrmada.example.com",
		"UID:arrmada-movie-21@arrmada.example.com",
		"DTSTAMP:20261013T093000Z",
		"DTSTART;VALUE=DATE:20261020", "DTEND;VALUE=DATE:20261021",
		"DTSTART;VALUE=DATE:20261231", "DTEND;VALUE=DATE:20270101",
		`SUMMARY:Harbour\, Lights\; \\ Night — S02E05 · The\nBreakwater`,
		"SUMMARY:" + long + " (2026)",
	} {
		if !strings.Contains(unfolded, want) {
			t.Errorf("missing %q in:\n%s", want, unfolded)
		}
	}
	if strings.Count(out, "BEGIN:VEVENT") != 2 {
		t.Errorf("events = %d, want 2 (the undated one skipped)", strings.Count(out, "BEGIN:VEVENT"))
	}
}

// icsServer is calendarServer with episodes and films dated around today, a requester
// and a manager.
func icsServer(t *testing.T, logTo *bytes.Buffer) (*routeServer, *auth.User, *http.Cookie, *http.Cookie) {
	t.Helper()
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		if logTo != nil {
			d.Log = slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{Level: slog.LevelDebug}))
		}
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Quality = quality.NewService(db)
		d.Movies = movies.NewService(db, nil, nil, root, "", nil, d.Log)
		d.Series = series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, d.Movies, d.Series, d.Books, nil, d.Quality, nil, "", d.Log)
	})
	day := func(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }
	for _, q := range []string{
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (11, 501, 'Harbour Lights', 1), (12, 502, 'Saltwind', 1)`,
		`INSERT INTO episodes (series_id, season_number, episode_number, title, air_date, monitored, has_file) VALUES
			(11, 2, 5, 'The Breakwater', '` + day(3) + `', 1, 0),
			(12, 1, 1, 'Pilot', '` + day(5) + `', 1, 0),
			(11, 2, 9, 'Far Off', '` + day(200) + `', 1, 0)`,
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, has_file, extra_json) VALUES
			(21, 601, 'The Cartographer', 2026, 1, 0, '{"release_date":"` + day(10) + `"}'),
			(22, 602, 'Hardcore Porn Night', 2026, 1, 0, '{"release_date":"` + day(11) + `"}')`,
	} {
		if _, err := s.st.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	kid, kidCookie := s.user(t, "kid", auth.RoleRequester)
	_, mgr := s.user(t, "mgr", auth.RoleManager)
	if _, err := s.st.DB().Exec(`INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name) VALUES ('movie', 601, 'x', 'approved', ?, 'kid')`, kid.ID); err != nil {
		t.Fatal(err)
	}
	return s, kid, kidCookie, mgr
}

type feedAnswer struct {
	Enabled   bool   `json:"enabled"`
	Mine      bool   `json:"mine"`
	URL       string `json:"url"`
	WebcalURL string `json:"webcal_url"`
}

func makeFeed(t *testing.T, s *routeServer, c *http.Cookie) feedAnswer {
	t.Helper()
	rec := s.do("POST", "/api/v1/me/calendar-feed", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("make feed: HTTP %d %s", rec.Code, rec.Body)
	}
	var f feedAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// feedPath is a feed link's path and query, for routeServer.do.
func feedPath(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

func TestICSRequiresValidToken(t *testing.T) {
	s, _, kid, _ := icsServer(t, nil)
	f := makeFeed(t, s, kid)
	if !f.Enabled || !strings.Contains(f.URL, "/api/v1/calendar.ics?") || !strings.HasPrefix(f.WebcalURL, "webcal://") {
		t.Fatalf("feed = %+v", f)
	}
	rec := s.do("GET", feedPath(t, f.URL), nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/calendar") ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "private") {
		t.Fatalf("valid token: HTTP %d %v", rec.Code, rec.Header())
	}
	if rec := s.do("GET", "/api/v1/calendar.ics", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no token: HTTP %d, want 400", rec.Code)
	}
	for _, bad := range []string{"nope", strings.Repeat("A", 43), strings.Repeat("x", 500)} {
		rec := s.do("GET", "/api/v1/calendar.ics?token="+bad, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "BEGIN:VCALENDAR") {
			t.Errorf("token %.10q…: HTTP %d, want 404 with no data", bad, rec.Code)
		}
	}
	// The stored row is a hash, never the token.
	tok := mustQuery(t, f.URL, "token")
	var stored string
	if err := s.st.DB().QueryRow(`SELECT token_hash FROM calendar_feeds`).Scan(&stored); err != nil || stored == tok || strings.Contains(stored, tok) || len(stored) != 64 {
		t.Errorf("stored %q (%v)", stored, err)
	}
	// GET /me/calendar-feed says it's on but never gives the link back.
	rec = s.do("GET", "/api/v1/me/calendar-feed", kid)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), tok) || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("status: HTTP %d %s", rec.Code, rec.Body)
	}
}

func mustQuery(t *testing.T, link, key string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

// A new link stops the old one straight away; turning the feed off stops the new one;
// a disabled account's link answers like an unknown one.
func TestTokenRotationRevokesOld(t *testing.T) {
	s, kidUser, kid, _ := icsServer(t, nil)
	first := makeFeed(t, s, kid)
	second := makeFeed(t, s, kid)
	if first.URL == second.URL {
		t.Fatal("rotating gave the same link")
	}
	if rec := s.do("GET", feedPath(t, first.URL), nil); rec.Code != http.StatusNotFound {
		t.Errorf("old link after rotation: HTTP %d, want 404", rec.Code)
	}
	if rec := s.do("GET", feedPath(t, second.URL), nil); rec.Code != http.StatusOK {
		t.Errorf("new link: HTTP %d", rec.Code)
	}
	if _, err := s.st.DB().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, kidUser.ID); err != nil {
		t.Fatal(err)
	}
	if rec := s.do("GET", feedPath(t, second.URL), nil); rec.Code != http.StatusNotFound {
		t.Errorf("disabled account: HTTP %d, want 404", rec.Code)
	}
	if _, err := s.st.DB().Exec(`UPDATE users SET disabled = 0 WHERE id = ?`, kidUser.ID); err != nil {
		t.Fatal(err)
	}
	if rec := s.do("DELETE", "/api/v1/me/calendar-feed", kid); rec.Code != http.StatusOK {
		t.Fatalf("turn off: HTTP %d", rec.Code)
	}
	if rec := s.do("GET", feedPath(t, second.URL), nil); rec.Code != http.StatusNotFound {
		t.Errorf("turned-off link: HTTP %d, want 404", rec.Code)
	}
}

// A requester's link shows only their own requests by default (and the whole library's
// schedule, as their Calendar does, when they ask); a manager's shows everything. Adult
// titles never appear, and nothing outside the window does.
func TestICSRequesterScope(t *testing.T) {
	s, _, kid, mgr := icsServer(t, nil)
	kf := makeFeed(t, s, kid)
	if !kf.Mine || mustQuery(t, kf.URL, "mine") != "1" {
		t.Fatalf("requester feed should default to mine: %+v", kf)
	}
	body := func(path string) string {
		t.Helper()
		rec := s.do("GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", path, rec.Code)
		}
		return strings.ReplaceAll(rec.Body.String(), "\r\n ", "")
	}
	kidFeed := body(feedPath(t, kf.URL))
	if !strings.Contains(kidFeed, "The Cartographer") || strings.Contains(kidFeed, "Harbour Lights") || strings.Contains(kidFeed, "Saltwind") {
		t.Errorf("kid's default feed should hold only their request:\n%s", kidFeed)
	}
	all := body(strings.Replace(feedPath(t, kf.URL), "mine=1", "mine=0", 1))
	if !strings.Contains(all, "Harbour Lights") || !strings.Contains(all, "Saltwind") {
		t.Errorf("kid's whole-library feed:\n%s", all)
	}
	mf := makeFeed(t, s, mgr)
	if mf.Mine {
		t.Errorf("staff feed should default to everything: %+v", mf)
	}
	staff := body(feedPath(t, mf.URL))
	for _, want := range []string{"The Cartographer", "Harbour Lights", "Saltwind"} {
		if !strings.Contains(staff, want) {
			t.Errorf("staff feed missing %q", want)
		}
	}
	for _, feed := range []string{all, staff} {
		if strings.Contains(feed, "Porn") {
			t.Error("an adult title reached a feed")
		}
		if strings.Contains(feed, "Far Off") {
			t.Error("an episode past the window reached a feed")
		}
	}
}

// The feed is the one calendar route reachable from outside the network; the Calendar
// and the link's own management stay LAN-only for requesters.
func TestICSExternalAllowed(t *testing.T) {
	rt := testRouter(t, true)
	if code := callFromOutside(rt, "GET", "/api/v1/calendar.ics", nil).Code; code != sentinelStatus {
		t.Errorf("feed from outside: HTTP %d, want it let through", code)
	}
	kid := &auth.User{ID: 5, Role: auth.RoleRequester}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/calendar"}, {"GET", "/api/v1/me/calendar-feed"}, {"POST", "/api/v1/me/calendar-feed"}, {"DELETE", "/api/v1/me/calendar-feed"},
	} {
		if code := callFromOutside(rt, c.method, c.path, kid).Code; code != http.StatusForbidden {
			t.Errorf("%s %s from outside as a requester: HTTP %d, want 403", c.method, c.path, code)
		}
	}
}

// The token opens the feed and nothing else: not as a header, not as a query parameter
// on another route.
func TestICSTokenOpensNothingElse(t *testing.T) {
	s, _, kid, _ := icsServer(t, nil)
	tok := mustQuery(t, makeFeed(t, s, kid).URL, "token")
	for _, path := range []string{"/api/v1/calendar?token=" + tok, "/api/v1/me/calendar-feed?token=" + tok, "/api/v1/auth/me?token=" + tok} {
		if rec := s.do("GET", path, nil); rec.Code == http.StatusOK {
			t.Errorf("%s answered 200 with only the feed token", path)
		}
	}
	for _, h := range []string{"Authorization", "X-Api-Key"} {
		r := httptest.NewRequest("GET", "http://arrmada.local/api/v1/calendar", nil)
		r.RemoteAddr = "192.168.1.20:5000"
		v := tok
		if h == "Authorization" {
			v = "Bearer " + tok
		}
		r.Header.Set(h, v)
		rec := httptest.NewRecorder()
		s.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("feed token as %s: HTTP %d, want 401", h, rec.Code)
		}
	}
}

// The request log records the route, never the query string that carries the token.
func TestRequestLogOmitsQuery(t *testing.T) {
	var logs bytes.Buffer
	s, _, kid, _ := icsServer(t, &logs)
	f := makeFeed(t, s, kid)
	tok := mustQuery(t, f.URL, "token")
	logs.Reset()
	if rec := s.do("GET", feedPath(t, f.URL), nil); rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	s.do("GET", "/api/v1/calendar.ics?token="+tok+"x", nil) // a wrong one too
	out := logs.String()
	if !strings.Contains(out, "/api/v1/calendar.ics") {
		t.Fatalf("no request line logged:\n%s", out)
	}
	if strings.Contains(out, tok) || strings.Contains(out, "token=") {
		t.Errorf("the token reached the log:\n%s", out)
	}
}

// Fetches are rate-limited per address.
func TestICSRateLimited(t *testing.T) {
	s, _, kid, _ := icsServer(t, nil)
	path := feedPath(t, makeFeed(t, s, kid).URL)
	limited := false
	for i := 0; i < icsPerMinute+5 && !limited; i++ {
		limited = s.do("GET", path, nil).Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Errorf("%d fetches in a row were never limited", icsPerMinute+5)
	}
}

// With an outside address set, the link is built from it.
func TestICSLinkUsesOutsideAddress(t *testing.T) {
	s, _, kid, _ := icsServer(t, nil)
	if err := s.deps.Settings.Set(t.Context(), settings.KeyPublicURL, "https://arrmada.example.com"); err != nil {
		t.Fatal(err)
	}
	f := makeFeed(t, s, kid)
	if !strings.HasPrefix(f.URL, "https://arrmada.example.com/api/v1/calendar.ics?") || !strings.HasPrefix(f.WebcalURL, "webcal://arrmada.example.com/") {
		t.Errorf("feed = %+v", f)
	}
}
