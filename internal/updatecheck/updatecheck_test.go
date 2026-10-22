package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// fakeGitHub stands in for api.github.com. Tests never call the real API.
type fakeGitHub struct {
	mu       sync.Mutex
	requests []*http.Request
	latest   func(w http.ResponseWriter, r *http.Request)
	compare  func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Clone(context.Background()))
		latest, compare := f.latest, f.compare
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/repos/aleonenko-us/Arrmada/releases/latest" && latest != nil:
			latest(w, r)
		case strings.HasPrefix(r.URL.Path, "/repos/aleonenko-us/Arrmada/compare/") && compare != nil:
			compare(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeGitHub) request(i int) *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

// setLatest and setCompare swap an answer between checks (under the lock: the server
// reads them on its own goroutines).
func (f *fakeGitHub) setLatest(h func(w http.ResponseWriter, r *http.Request)) {
	f.mu.Lock()
	f.latest = h
	f.mu.Unlock()
}

func (f *fakeGitHub) setCompare(h func(w http.ResponseWriter, r *http.Request)) {
	f.mu.Lock()
	f.compare = h
	f.mu.Unlock()
}

func release(tag, body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "name": "Arrmada " + tag, "html_url": "https://github.com/aleonenko-us/Arrmada/releases/tag/" + tag,
			"body": body, "draft": false, "prerelease": false, "published_at": "2026-10-01T10:00:00Z",
		})
	}
}

func newChecker(srv *httptest.Server, version, commit string, enabled func(context.Context) bool) *Checker {
	return New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: version, Commit: commit, Enabled: enabled})
}

func TestUpdateCheckComparesSemver(t *testing.T) {
	gh := &fakeGitHub{latest: release("v1.3.0", "<!-- generated -->\r\n## What's Changed\r\n* Faster imports by @someone")}
	srv := gh.server(t)

	c := newChecker(srv, "v1.2.0", "abc1234", nil)
	if err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := c.Status(context.Background())
	if !st.Enabled || st.Channel != ChannelRelease || !st.Available || st.Latest == nil || st.Latest.Tag != "v1.3.0" {
		t.Fatalf("status = %+v, want v1.3.0 available to a v1.2.0 release build", st)
	}
	if st.Latest.Notes != "## What's Changed\n* Faster imports by @someone" {
		t.Errorf("notes = %q (comment and CRs should be gone)", st.Latest.Notes)
	}
	if st.Latest.URL != "https://github.com/aleonenko-us/Arrmada/releases/tag/v1.3.0" || st.CheckedAt == nil || st.Problem != "" {
		t.Errorf("status = %+v", st)
	}
	if st.Behind != nil {
		t.Errorf("a release build isn't compared with main, behind = %v", *st.Behind)
	}
	// Only the release was asked for, as a plain GET that says nothing about this server.
	if gh.count() != 1 {
		t.Fatalf("%d requests, want 1", gh.count())
	}
	r := gh.request(0)
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		t.Errorf("request = %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
	}
	if ua := r.Header.Get("User-Agent"); ua != httpua.UserAgent() {
		t.Errorf("User-Agent = %q, want %q", ua, httpua.UserAgent())
	}

	// The same release, the newest one, and a newer pre-release build aren't updates.
	for _, cur := range []string{"v1.3.0", "v1.4.0", "v1.3.1-rc.1", "v1.3.0+build.5"} {
		c := newChecker(srv, cur, "abc1234", nil)
		_ = c.Run(context.Background())
		if st := c.Status(context.Background()); st.Available {
			t.Errorf("%s: Available, want not (latest v1.3.0)", cur)
		}
	}
	// A pre-release of the latest is older than it.
	c = newChecker(srv, "v1.3.0-rc.2", "abc1234", nil)
	_ = c.Run(context.Background())
	if st := c.Status(context.Background()); !st.Available {
		t.Errorf("v1.3.0-rc.2: not Available, want v1.3.0 offered")
	}
}

func TestUpdateCheckBehindMain(t *testing.T) {
	var asked atomic.Value
	gh := &fakeGitHub{
		latest: release("v1.3.0", "notes"),
		compare: func(w http.ResponseWriter, r *http.Request) {
			asked.Store(r.URL.Path + "?" + r.URL.RawQuery)
			// Large fields before and after ahead_by, as GitHub's answer has.
			_, _ = w.Write([]byte(`{"url":"x","base_commit":{"sha":"abc1234","commit":{"message":"m"}},` +
				`"status":"behind","ahead_by":7,"behind_by":0,"total_commits":7,"commits":[{"sha":"1"}],"files":[{"filename":"a"}]}`))
		},
	}
	srv := gh.server(t)
	c := newChecker(srv, "2026.10.13", "abc1234", nil)
	_ = c.Run(context.Background())
	st := c.Status(context.Background())
	if st.Channel != ChannelMain || st.Behind == nil || *st.Behind != 7 {
		t.Fatalf("status = %+v, want 7 commits behind main", st)
	}
	// A commit-date build is never offered a release as an "update": it isn't a release.
	if st.Available || st.Latest == nil || st.Latest.Tag != "v1.3.0" {
		t.Errorf("status = %+v", st)
	}
	if got, _ := asked.Load().(string); got != "/repos/aleonenko-us/Arrmada/compare/abc1234...main?per_page=1" {
		t.Errorf("compare asked %q", got)
	}

	// A commit GitHub doesn't know (local work) is "unknown", not a problem.
	gh.setCompare(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	c = newChecker(srv, "2026.10.13-dirty", "def5678", nil)
	_ = c.Run(context.Background())
	if st := c.Status(context.Background()); st.Behind != nil || st.Problem != "" {
		t.Errorf("unknown commit: status = %+v", st)
	}
}

func TestUpdateCheckDisabled(t *testing.T) {
	gh := &fakeGitHub{latest: release("v9.0.0", "")}
	srv := gh.server(t)
	off := func(context.Context) bool { return false }
	c := newChecker(srv, "v1.0.0", "abc1234", off)
	if err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := c.CheckNow(context.Background())
	if gh.count() != 0 {
		t.Fatalf("%d requests to GitHub with the check off, want none", gh.count())
	}
	if st.Enabled || st.Latest != nil || st.Available || st.Current != "v1.0.0" {
		t.Errorf("status = %+v", st)
	}

	// Switching it off hides what an earlier check found.
	var on atomic.Bool
	on.Store(true)
	c = newChecker(srv, "v1.0.0", "abc1234", func(context.Context) bool { return on.Load() })
	_ = c.Run(context.Background())
	on.Store(false)
	if st := c.Status(context.Background()); st.Enabled || st.Latest != nil {
		t.Errorf("after switching off: %+v", st)
	}
}

// No release published yet: nothing offered, nothing wrong.
func TestUpdateCheckNoReleases(t *testing.T) {
	gh := &fakeGitHub{}
	srv := gh.server(t)
	c := newChecker(srv, "v1.0.0", "abc1234", nil)
	_ = c.Run(context.Background())
	st := c.Status(context.Background())
	if st.Latest != nil || st.Available || st.Problem != "" || st.CheckedAt == nil {
		t.Errorf("status = %+v, want a quiet 'nothing published'", st)
	}
}

// The rate limit (and other failures) keep the last good answer and say why, without
// failing the task.
func TestUpdateCheckRateLimitKeepsLastAnswer(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	gh := &fakeGitHub{latest: release("v1.3.0", "notes")}
	srv := gh.server(t)
	c := New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: "v1.2.0", Commit: "abc1234", Now: func() time.Time { return now }})
	_ = c.Run(context.Background())

	gh.setLatest(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	now = now.Add(Interval)
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v, want nil (GitHub trouble isn't a task failure)", err)
	}
	st := c.Status(context.Background())
	if !st.Available || st.Latest == nil || st.Latest.Tag != "v1.3.0" || !strings.Contains(st.Problem, "wait") {
		t.Errorf("status = %+v, want v1.3.0 kept and a rate-limit note", st)
	}
	if st.CheckedAt == nil || !st.CheckedAt.Equal(now.Add(-Interval)) {
		t.Errorf("checked_at = %v, want the last good check", st.CheckedAt)
	}

	gh.setLatest(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	_ = c.Run(context.Background())
	if st := c.Status(context.Background()); st.Latest == nil || !strings.Contains(st.Problem, "502") {
		t.Errorf("status = %+v", st)
	}

	srv.Close()
	_ = c.Run(context.Background())
	if st := c.Status(context.Background()); st.Latest == nil || !strings.Contains(st.Problem, "reach GitHub") {
		t.Errorf("status = %+v", st)
	}
}

// The second check sends the ETag; a 304 keeps the release it describes.
func TestUpdateCheckUsesETag(t *testing.T) {
	var seen atomic.Value
	gh := &fakeGitHub{}
	gh.latest = func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		release("v1.3.0", "notes")(w, r)
	}
	srv := gh.server(t)
	c := newChecker(srv, "v1.2.0", "abc1234", nil)
	_ = c.Run(context.Background())
	_ = c.Run(context.Background())
	if got, _ := seen.Load().(string); got != `"v1"` {
		t.Errorf("If-None-Match = %q", got)
	}
	if st := c.Status(context.Background()); st.Latest == nil || st.Latest.Tag != "v1.3.0" || !st.Available {
		t.Errorf("after 304: %+v", st)
	}
}

// Check now asks GitHub at most once a minute.
func TestCheckNowThrottles(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	gh := &fakeGitHub{latest: release("v1.3.0", "")}
	srv := gh.server(t)
	c := New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: "v1.2.0", Commit: "abc1234", Now: func() time.Time { return now }})
	c.CheckNow(context.Background())
	c.CheckNow(context.Background())
	if gh.count() != 1 {
		t.Fatalf("%d requests, want 1 within a minute", gh.count())
	}
	now = now.Add(2 * time.Minute)
	c.CheckNow(context.Background())
	if gh.count() != 2 {
		t.Fatalf("%d requests, want 2", gh.count())
	}
}

// Notes are cut to MaxNotes on a line; a link that isn't a github.com page is dropped.
func TestReleaseNotesAndLinkAreSafe(t *testing.T) {
	long := strings.Repeat("* a line of notes é\n", 400)
	gh := &fakeGitHub{latest: func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0", "html_url": "javascript:alert(1)", "body": long,
		})
	}}
	srv := gh.server(t)
	c := newChecker(srv, "v1.0.0", "abc1234", nil)
	_ = c.Run(context.Background())
	st := c.Status(context.Background())
	if st.Latest == nil {
		t.Fatal("no release")
	}
	if st.Latest.URL != "" {
		t.Errorf("url = %q, want it dropped", st.Latest.URL)
	}
	if len(st.Latest.Notes) > MaxNotes || !st.Latest.NotesTruncated || !strings.HasSuffix(st.Latest.Notes, "é") {
		t.Errorf("notes: %d bytes, truncated=%v, ends %q", len(st.Latest.Notes), st.Latest.NotesTruncated, st.Latest.Notes[len(st.Latest.Notes)-5:])
	}
	for _, u := range []string{"http://github.com/x", "https://evil.example/x", "https://github.com.evil.example/x"} {
		if githubURL(u) != "" {
			t.Errorf("githubURL(%q) kept it", u)
		}
	}
}

func TestSemverOrder(t *testing.T) {
	ordered := []string{"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2", "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0", "v1.0.1", "v1.2.0", "v1.10.0", "v2.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		if !newer(ordered[i+1], ordered[i]) || newer(ordered[i], ordered[i+1]) {
			t.Errorf("want %s < %s", ordered[i], ordered[i+1])
		}
	}
	for _, bad := range []string{"", "docker", "0.0.0-dev.", "1.2", "1.2.3.4", "v01.2.3", "2026.10.13x", "v1.2.3-"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("parseSemver(%q) ok, want not", bad)
		}
	}
	if newer("v1.2.0", "v1.2.0-dirty") {
		t.Error("a build of v1.2.0 with local edits isn't behind v1.2.0")
	}
}

func TestChannelOf(t *testing.T) {
	for _, tc := range []struct{ version, commit, install, want string }{
		{"v1.2.0", "abc1234", "", ChannelRelease},
		{"v1.3.0-rc.1", "abc1234", "", ChannelRelease},
		{"2026.10.13", "abc1234", "", ChannelMain},
		{"2026.10.13-dirty", "abc1234", "", ChannelMain},
		{"main-abc1234", "abc1234", "", ChannelMain},
		{"0.0.0-dev", "unknown", "", ChannelDev},
		{"docker", "unknown", "", ChannelDev},
		// An install on the releases: the published image is a release; a build of a
		// commit (kept by update.sh until a release includes it) is compared with one.
		{"v1.2.0", "abc1234", InstallStable, ChannelRelease},
		{"2026.10.13", "abc1234", InstallStable, ChannelBuild},
		{"2026.10.13-dirty", "abc1234", InstallStable, ChannelBuild},
		{"docker", "unknown", InstallStable, ChannelDev},
		// An install on main compares with main, even a build of a release's commit
		// (stamped with the tag).
		{"2026.10.13", "abc1234", InstallMain, ChannelMain},
		{"v1.2.0", "abc1234", InstallMain, ChannelMain},
		{"v1.2.0", "unknown", InstallMain, ChannelRelease},
		{"docker", "unknown", InstallMain, ChannelDev},
	} {
		if got := channelOf(tc.version, tc.commit, tc.install); got != tc.want {
			t.Errorf("channelOf(%q, %q, %q) = %q, want %q", tc.version, tc.commit, tc.install, got, tc.want)
		}
	}
}

// A build of a commit on an install that follows the releases is compared with the
// latest release: offered it when the release includes the build's code, told it's newer
// when it has code the release doesn't.
func TestUpdateCheckBuildOnStable(t *testing.T) {
	var asked atomic.Value
	answer := func(ahead, behind int) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			asked.Store(r.URL.Path + "?" + r.URL.RawQuery)
			_, _ = w.Write([]byte(`{"url":"x","base_commit":{"sha":"abc1234"},"status":"s",` +
				`"ahead_by":` + strconv.Itoa(ahead) + `,"behind_by":` + strconv.Itoa(behind) + `,"commits":[{"sha":"1"}],"files":[]}`))
		}
	}
	gh := &fakeGitHub{latest: release("v1.3.0", "notes"), compare: answer(4, 0)}
	srv := gh.server(t)
	check := func(version string) Status {
		t.Helper()
		c := New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: version, Commit: "abc1234", Install: InstallStable})
		_ = c.Run(context.Background())
		return c.Status(context.Background())
	}

	// The release has the build's code and 4 commits more: update.sh moves to it.
	st := check("2026.10.08")
	if st.Channel != ChannelBuild || !st.Available || st.Ahead == nil || *st.Ahead != 0 || st.Behind != nil || st.Problem != "" {
		t.Fatalf("status = %+v, want v1.3.0 offered to an older build", st)
	}
	if got, _ := asked.Load().(string); got != "/repos/aleonenko-us/Arrmada/compare/abc1234...v1.3.0?per_page=1" {
		t.Errorf("compare asked %q", got)
	}
	// The build has 6 commits the release doesn't: newer, nothing offered.
	gh.setCompare(answer(2, 6))
	if st := check("2026.10.14"); st.Available || st.Ahead == nil || *st.Ahead != 6 {
		t.Errorf("status = %+v, want 6 ahead and nothing offered", st)
	}
	// The release's own commit, built here: up to date.
	gh.setCompare(answer(0, 0))
	if st := check("2026.10.14"); st.Available || st.Ahead == nil || *st.Ahead != 0 {
		t.Errorf("status = %+v, want up to date", st)
	}
	// Local edits: never offered a release, whatever GitHub says of the commit.
	gh.setCompare(answer(4, 0))
	if st := check("2026.10.08-dirty"); st.Available {
		t.Errorf("status = %+v, want a -dirty build offered nothing", st)
	}
	// A commit GitHub doesn't know: unknown, not a problem.
	gh.setCompare(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if st := check("2026.10.14"); st.Available || st.Ahead != nil || st.Problem != "" {
		t.Errorf("status = %+v, want unknown", st)
	}
	// No release published: nothing compared.
	gh.setLatest(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	gh.setCompare(func(w http.ResponseWriter, _ *http.Request) { t.Error("compared with no release published") })
	if st := check("2026.10.14"); st.Latest != nil || st.Available || st.Ahead != nil {
		t.Errorf("status = %+v", st)
	}
}

// An install on main compares a build with main, not with the releases.
func TestUpdateCheckMainInstall(t *testing.T) {
	var asked atomic.Value
	gh := &fakeGitHub{
		latest: release("v1.3.0", "notes"),
		compare: func(w http.ResponseWriter, r *http.Request) {
			asked.Store(r.URL.Path)
			_, _ = w.Write([]byte(`{"ahead_by":3,"behind_by":0}`))
		},
	}
	srv := gh.server(t)
	c := New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: "v1.2.0", Commit: "abc1234", Install: InstallMain})
	_ = c.Run(context.Background())
	st := c.Status(context.Background())
	if st.Channel != ChannelMain || st.Behind == nil || *st.Behind != 3 || st.Available || st.Ahead != nil {
		t.Fatalf("status = %+v, want 3 behind main and no release offered", st)
	}
	if got, _ := asked.Load().(string); got != "/repos/aleonenko-us/Arrmada/compare/abc1234...main" {
		t.Errorf("compare asked %q", got)
	}
}

// A container Unraid's Docker manager made (HOST_OS=Unraid) says so, on or off, so the
// card can point at Unraid's Docker page rather than the install line. Nothing else
// about the check changes.
func TestUpdateCheckUnraidHost(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "HOST_OS" {
				return v
			}
			return ""
		}
	}
	for v, want := range map[string]string{"Unraid": HostUnraid, " unraid ": HostUnraid, "": "", "Linux": ""} {
		if got := HostFromEnv(env(v)); got != want {
			t.Errorf("HOST_OS=%q: host %q, want %q", v, got, want)
		}
	}

	gh := &fakeGitHub{latest: release("v1.3.0", "notes")}
	srv := gh.server(t)
	var on atomic.Bool
	on.Store(true)
	c := New(Options{Client: srv.Client(), BaseURL: srv.URL, Version: "v1.2.0", Commit: "abc1234", Host: HostUnraid,
		Enabled: func(context.Context) bool { return on.Load() }})
	_ = c.Run(context.Background())
	if st := c.Status(context.Background()); st.Host != HostUnraid || !st.Available || st.Channel != ChannelRelease {
		t.Fatalf("status = %+v, want an Unraid host with v1.3.0 on offer", st)
	}
	on.Store(false)
	if st := c.Status(context.Background()); st.Host != HostUnraid {
		t.Errorf("check off: host %q", st.Host)
	}
	if st := newChecker(srv, "v1.2.0", "abc1234", nil).Status(context.Background()); st.Host != "" {
		t.Errorf("no host given: host %q", st.Host)
	}
}

func TestReadCompareNeedsBothCounts(t *testing.T) {
	if _, err := readCompare(strings.NewReader(`{"ahead_by":1}`)); err == nil {
		t.Error("an answer without behind_by read as a comparison")
	}
	cmp, err := readCompare(strings.NewReader(`{"behind_by":2,"ahead_by":1,"files":[`))
	if err != nil || cmp.Ahead != 1 || cmp.Behind != 2 {
		t.Errorf("readCompare = %+v, %v (it should stop once both are read)", cmp, err)
	}
}
