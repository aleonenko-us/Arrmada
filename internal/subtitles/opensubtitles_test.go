package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// osFake is a scriptable OpenSubtitles: the account "me" with password "secret" and key
// "good"; each endpoint's status can be overridden, and every request is counted.
type osFake struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	calls    map[string]int
	status   map[string][]int // path → statuses to answer in turn (then 200)
	header   http.Header      // extra headers on an overridden answer
	reset    string           // reset_time_utc on a 406
	userInfo string
}

func newOSFake(t *testing.T) *osFake {
	f := &osFake{t: t, calls: map[string]int{}, status: map[string][]int{}, header: http.Header{},
		userInfo: `{"data":{"allowed_downloads":20,"remaining_downloads":17,"level":"Sub leecher","vip":false,"reset_time_utc":""}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *osFake) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *osFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls[r.URL.Path]++
	var code int
	if q := f.status[r.URL.Path]; len(q) > 0 {
		code, f.status[r.URL.Path] = q[0], q[1:]
	}
	f.mu.Unlock()
	if code != 0 && code != http.StatusOK {
		for k, v := range f.header {
			w.Header()[k] = v
		}
		w.WriteHeader(code)
		if code == http.StatusNotAcceptable {
			_ = json.NewEncoder(w).Encode(map[string]any{"remaining": 0, "reset_time_utc": f.reset, "message": "quota"})
		}
		return
	}
	if r.URL.Path == "/file.srt" { // the temporary link: no key needed
		_, _ = io.WriteString(w, fakeSRT)
		return
	}
	if r.Header.Get("Api-Key") != "good" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/infos/formats":
		_, _ = io.WriteString(w, `{"data":{"output_formats":["srt"]}}`)
	case "/login":
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["username"] != "me" || b["password"] != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Invalid username/password","status":401}`)
			return
		}
		_, _ = io.WriteString(w, `{"token":"tok","status":200}`)
	case "/infos/user":
		_, _ = io.WriteString(w, f.userInfo)
	case "/subtitles":
		_, _ = io.WriteString(w, `{"data":[{"attributes":{"language":"en","release":"Movie.2020","download_count":5,"moviehash_match":true,"files":[{"file_id":9}]}}]}`)
	case "/download":
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"link": f.srv.URL + "/file.srt", "remaining": 16})
	default:
		http.NotFound(w, r)
	}
}

func (f *osFake) provider(pass *atomic.Value) *OpenSubtitles {
	o := NewOpenSubtitlesFunc(func() string { return "good" }, func() string { return "me" }, func() string { return pass.Load().(string) })
	o.baseURL = f.srv.URL
	return o
}

func password(p string) *atomic.Value {
	var v atomic.Value
	v.Store(p)
	return &v
}

// The Test signs in fresh and reads the account; the state the header shows is "ok" only
// after that, with the account's allowance.
func TestVerifyReadsUserInfo(t *testing.T) {
	f := newOSFake(t)
	f.userInfo = `{"data":{"allowed_downloads":1000,"remaining_downloads":998,"level":"VIP member","vip":true,"reset_time_utc":""}}`
	o := f.provider(password("secret"))
	if st := o.Status(); st.State != ProviderUnverified {
		t.Errorf("before any login: %+v, want unverified", st)
	}
	got, err := o.Verify(context.Background(), "")
	if err != nil || got != "OK: signed in as me (VIP), 998 downloads left today." {
		t.Fatalf("Verify = %q, %v", got, err)
	}
	st := o.Status()
	if st.State != ProviderOK || !st.VIP || st.Allowed != 1000 {
		t.Errorf("status = %+v, want ok, VIP, 1000 a day", st)
	}
	if left, reset := o.Quota(); left != 998 || !reset.IsZero() {
		t.Errorf("quota = %d %v", left, reset)
	}
	// A second press straight away is the same answer, not a second login.
	if _, err := o.Verify(context.Background(), ""); err != nil || f.count("/login") != 1 {
		t.Errorf("second Test: err=%v logins=%d, want 1 login", err, f.count("/login"))
	}
}

// A wrong password: the Test says so, downloads pause (no requests at all) with an
// auth_failed reason — until the password changes, when the next login is fresh.
func TestLoginRefreshesOnCredentialChange(t *testing.T) {
	f := newOSFake(t)
	pass := password("wrong")
	o := f.provider(pass)
	ctx := context.Background()
	_, err := o.Verify(ctx, "")
	if err == nil || !strings.HasPrefix(err.Error(), "Login rejected (401)") {
		t.Fatalf("Verify with a wrong password = %v", err)
	}
	if st := o.Status(); st.State != ProviderAuthFailed || st.Detail != "OpenSubtitles login rejected (401)" {
		t.Errorf("status = %+v", st)
	}
	if !o.Paused() {
		t.Fatal("downloads not paused after a rejected login")
	}
	if res, msg := o.PauseReason(); res != ResultAuthFailed || msg != "OpenSubtitles login rejected (401)" {
		t.Errorf("pause reason = %s %q", res, msg)
	}
	pass.Store("secret") // changed in Settings: a new fingerprint
	if o.Paused() {
		t.Error("still paused after the password changed")
	}
	if _, err := o.Download(ctx, "9"); err != nil {
		t.Fatalf("download with the new password: %v", err)
	}
	if st := o.Status(); st.State != ProviderOK {
		t.Errorf("after a good login: %+v", st)
	}
	// Saving the same values again (ResetAuth) forgets the token too: the next download
	// signs in fresh.
	before := f.count("/login")
	o.ResetAuth()
	if _, err := o.Download(ctx, "9"); err != nil || f.count("/login") != before+1 {
		t.Errorf("after ResetAuth: err=%v logins %d → %d", err, before, f.count("/login"))
	}
}

// A 429 is seconds: ErrRateLimited with the Retry-After, the quota untouched.
func Test429IsShortBackoffNotQuota(t *testing.T) {
	f := newOSFake(t)
	f.status["/download"] = []int{http.StatusTooManyRequests}
	f.header.Set("Retry-After", "3")
	o := f.provider(password("secret"))
	_, err := o.Download(context.Background(), "9")
	var rl *RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || rl.RetryAfter != 3*time.Second {
		t.Fatalf("Download on 429 = %v, want a 3s rate limit", err)
	}
	if o.quotaPaused() || o.Paused() {
		t.Error("a 429 paused downloads like a spent quota")
	}
	if left, _ := o.Quota(); left != -1 {
		t.Errorf("downloads left = %d, want unknown (-1) — a 429 says nothing about the quota", left)
	}
	if st := o.Status(); st.State != ProviderRateLimited {
		t.Errorf("status = %+v, want rate_limited", st)
	}
	// Search 429s the same way.
	f.status["/subtitles"] = []int{http.StatusTooManyRequests}
	if _, err := o.Search(context.Background(), SearchRequest{Title: "Movie", Language: "en"}); !errors.Is(err, ErrRateLimited) {
		t.Errorf("Search on 429 = %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		h    http.Header
		want time.Duration
	}{
		"none":          {http.Header{}, 10 * time.Second},
		"seconds":       {http.Header{"Retry-After": {"7"}}, 7 * time.Second},
		"date":          {http.Header{"Retry-After": {now.Add(30 * time.Second).Format(http.TimeFormat)}}, 30 * time.Second},
		"ratelimit":     {http.Header{"Ratelimit-Reset": {"2"}}, 2 * time.Second},
		"capped":        {http.Header{"Retry-After": {"86400"}}, 10 * time.Minute},
		"garbage→10s":   {http.Header{"Retry-After": {"soon"}}, 10 * time.Second},
		"past date→10s": {http.Header{"Retry-After": {now.Add(-time.Minute).Format(http.TimeFormat)}}, 10 * time.Second},
	} {
		if got := retryAfter(c.h, now); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// A 406 is the daily quota: paused until the API's own reset time.
func Test406PausesUntilReset(t *testing.T) {
	f := newOSFake(t)
	reset := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	f.reset = reset.Format(time.RFC3339)
	f.status["/download"] = []int{http.StatusNotAcceptable}
	o := f.provider(password("secret"))
	if _, err := o.Download(context.Background(), "9"); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("Download on 406 = %v", err)
	}
	if _, at := o.Quota(); !at.Equal(reset) {
		t.Errorf("paused until %v, want the API's reset %v", at, reset)
	}
	if res, _ := o.PauseReason(); !o.Paused() || res != ResultQuota {
		t.Errorf("paused=%v reason=%s", o.Paused(), res)
	}
	if st := o.Status(); st.State != ProviderQuotaSpent || !strings.HasPrefix(st.Detail, "Daily quota used — resumes") {
		t.Errorf("status = %+v", st)
	}
}

// An expired token is refreshed once inside the download, rather than failing the file.
func TestDownloadRefreshesExpiredToken(t *testing.T) {
	f := newOSFake(t)
	o := f.provider(password("secret"))
	if _, err := o.login(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.status["/download"] = []int{http.StatusUnauthorized}
	data, err := o.Download(context.Background(), "9")
	if err != nil || string(data) != fakeSRT {
		t.Fatalf("Download = %q, %v", data, err)
	}
	if f.count("/login") != 2 || f.count("/download") != 2 {
		t.Errorf("logins=%d downloads=%d, want a second login and one retry", f.count("/login"), f.count("/download"))
	}
	if o.Paused() {
		t.Error("an expired token paused downloads")
	}
}

// A job against a wrong password: the first run's login is rejected (auth_failed) and the
// language goes to AI; the next run makes no OpenSubtitles request at all.
func TestAuthFailedPausesDownloads(t *testing.T) {
	f := newOSFake(t)
	o := f.provider(password("wrong"))
	ai := &fakeAI{avail: true, transcribe: true}
	s, video := ladderFixture(t, "en", englishAudio(), o, ai)
	job := runJob(s)
	if job.State != StateDone || job.Note != "en: OpenSubtitles login rejected (401) → AI-generated" {
		t.Errorf("first run: state=%q note=%q", job.State, job.Note)
	}
	if err := os.Remove(sidecarPath(video, "en")); err != nil {
		t.Fatal(err)
	}
	searches, logins := f.count("/subtitles"), f.count("/login")
	job = runJob(s)
	if f.count("/subtitles") != searches || f.count("/login") != logins {
		t.Errorf("second run made requests: searches %d→%d logins %d→%d", searches, f.count("/subtitles"), logins, f.count("/login"))
	}
	if len(job.Outcomes) == 0 || job.Outcomes[0].Result != ResultAuthFailed {
		t.Errorf("second run outcomes = %+v, want auth_failed without a request", job.Outcomes)
	}
	if st := s.GetSettings(context.Background()); st.ProviderState != ProviderAuthFailed {
		t.Errorf("settings provider_state = %q", st.ProviderState)
	}
}

// rateLimitedProvider answers 429 to the first n searches.
type rateLimitedProvider struct {
	fakeProvider
	limit int
}

func (p *rateLimitedProvider) Search(ctx context.Context, r SearchRequest) ([]SubtitleResult, error) {
	p.mu.Lock()
	if p.limit > 0 {
		p.limit--
		p.searches++
		p.mu.Unlock()
		return nil, &RateLimitError{RetryAfter: 10 * time.Second}
	}
	p.mu.Unlock()
	return p.fakeProvider.Search(ctx, r)
}

// In a job, a 429 is waited out once and asked again; still limited, it's recorded as
// rate_limited and the rest goes to AI.
func TestProcessRateLimitWaitsOnce(t *testing.T) {
	prov := &rateLimitedProvider{fakeProvider: fakeProvider{canDownload: true, results: map[string][]SubtitleResult{"en": {{FileID: "1", HashMatch: true}}}}, limit: 1}
	s, video := ladderFixture(t, "en", englishAudio(), prov, &fakeAI{})
	s.rateWait = time.Millisecond
	job := runJob(s)
	assertSidecar(t, video, "en")
	if job.State != StateDone || job.Note != "en: downloaded" || prov.searches != 2 {
		t.Errorf("one 429: state=%q note=%q searches=%d", job.State, job.Note, prov.searches)
	}

	prov2 := &rateLimitedProvider{fakeProvider: fakeProvider{canDownload: true}, limit: 5}
	mi := &mediaInfo{AudioLangs: []string{"eng", "spa"}, Audio: []AudioTrack{{Index: 0, Lang: "eng"}, {Index: 1, Lang: "spa"}}}
	s2, _ := ladderFixture(t, "en,es", mi, prov2, &fakeAI{avail: true, transcribe: true})
	s2.rateWait = time.Millisecond
	job = runJob(s2)
	if prov2.searches != 2 {
		t.Errorf("searches = %d, want 2 (one retry, then stop asking)", prov2.searches)
	}
	want := "en: OpenSubtitles rate limit → AI-generated · es: OpenSubtitles rate limit → AI-generated"
	if job.State != StateDone || job.Note != want {
		t.Errorf("still limited: state=%q note=%q", job.State, job.Note)
	}
}
