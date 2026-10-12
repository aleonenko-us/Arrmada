package subtitles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const osBaseURL = "https://api.opensubtitles.com/api/v1"

// OpenSubtitles is a subtitle provider backed by opensubtitles.com's REST API. Search
// needs only an API key; downloading additionally needs a (free) account, so username +
// password are optional and only required to actually grab. Credentials come from config.
type OpenSubtitles struct {
	http       *http.Client
	apiKeyFn   func() string
	usernameFn func() string
	passwordFn func() string
	ua         string
	baseURL    string // osBaseURL when empty; tests point it at a local server

	mu       sync.Mutex
	token    string // cached bearer token from /login
	tokenFor string // accountFingerprint of the credentials it was signed in with

	// Download quota, as the API reports it. Free accounts get a handful of downloads
	// a day; once spent, every /download answers 406 until the reset. Tracking it means
	// the pipeline can stop asking — and the UI can say why — instead of logging a
	// failure per file for the rest of the day.
	quotaMu        sync.Mutex
	quotaRemaining int       // -1 = never reported
	quotaResetAt   time.Time // zero = not paused

	st osState // the last login result, a rate-limit wait, the account (osstatus.go)
}

// ErrQuotaExhausted means the provider's daily download allowance is spent.
var ErrQuotaExhausted = errors.New("opensubtitles: daily download quota used up")

// ErrAuthFailed means OpenSubtitles rejected the API key or the account login. Every
// further request fails the same way until the credentials change, so a job records
// auth_failed and moves on to AI rather than trying each language.
var ErrAuthFailed = errors.New("opensubtitles: credentials rejected")

// authError is an ErrAuthFailed with what was rejected and the HTTP status, worded for
// the job note: "OpenSubtitles login rejected (401)".
type authError struct {
	what   string // "login" | "API key"
	status int
}

func (e *authError) Error() string {
	return fmt.Sprintf("OpenSubtitles %s rejected (%d)", e.what, e.status)
}

func (e *authError) Is(target error) bool { return target == ErrAuthFailed }

// Quota implements quotaReporter.
func (o *OpenSubtitles) Quota() (int, time.Time) {
	o.quotaMu.Lock()
	defer o.quotaMu.Unlock()
	if !o.quotaResetAt.IsZero() && time.Now().After(o.quotaResetAt) {
		o.quotaResetAt = time.Time{} // reset has passed — try again
	}
	return o.quotaRemaining, o.quotaResetAt
}

// quotaPaused reports whether downloads should be skipped until the reset.
func (o *OpenSubtitles) quotaPaused() bool {
	_, reset := o.Quota()
	return !reset.IsZero()
}

// Paused implements the pipeline's pauser: while the quota is spent, or the credentials
// in use were rejected, the download rung sends nothing — not even a search — and the
// languages go straight to the AI rung. PauseReason says which.
func (o *OpenSubtitles) Paused() bool {
	if bad, _ := o.authRejected(); bad {
		return true
	}
	return o.quotaPaused()
}

// noteQuota records what a /download response said about the allowance.
func (o *OpenSubtitles) noteQuota(remaining int, resetUTC string, exhausted bool) {
	o.quotaMu.Lock()
	defer o.quotaMu.Unlock()
	o.quotaRemaining = remaining
	if !exhausted {
		o.quotaResetAt = time.Time{}
		return
	}
	// Prefer the API's own reset time; fall back to a day, which is the documented window.
	if t, err := time.Parse(time.RFC3339, resetUTC); err == nil && t.After(time.Now()) {
		o.quotaResetAt = t
	} else {
		o.quotaResetAt = time.Now().Add(24 * time.Hour)
	}
}

// NewOpenSubtitles builds the provider from config credentials (any may be empty).
func NewOpenSubtitles(apiKey, username, password string) *OpenSubtitles {
	return NewOpenSubtitlesFunc(
		func() string { return apiKey },
		func() string { return username },
		func() string { return password },
	)
}

// NewOpenSubtitlesFunc builds the provider reading each credential lazily, so values set
// in the settings menu take effect without a restart.
func NewOpenSubtitlesFunc(apiKey, username, password func() string) *OpenSubtitles {
	return &OpenSubtitles{
		http:           &http.Client{Timeout: 25 * time.Second},
		apiKeyFn:       apiKey,
		usernameFn:     username,
		passwordFn:     password,
		ua:             "Arrmada/1.0",
		quotaRemaining: -1,
	}
}

func (o *OpenSubtitles) apiKey() string   { return strings.TrimSpace(o.apiKeyFn()) }
func (o *OpenSubtitles) username() string { return strings.TrimSpace(o.usernameFn()) }
func (o *OpenSubtitles) password() string { return strings.TrimSpace(o.passwordFn()) }

// Available reports whether search is possible (needs the API key).
func (o *OpenSubtitles) Available() bool { return o.apiKey() != "" }

// CanDownload reports whether we can actually fetch files (needs a full account).
func (o *OpenSubtitles) CanDownload() bool {
	return o.apiKey() != "" && o.username() != "" && o.password() != ""
}

func (o *OpenSubtitles) req(ctx context.Context, method, path string, q url.Values, body any, bearer string) (*http.Response, error) {
	return o.reqKey(ctx, o.apiKey(), method, path, q, body, bearer)
}

// reqKey is req with an explicit API key, for testing one that isn't saved yet.
func (o *OpenSubtitles) reqKey(ctx context.Context, apiKey, method, path string, q url.Values, body any, bearer string) (*http.Response, error) {
	base := o.baseURL
	if base == "" {
		base = osBaseURL
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("User-Agent", o.ua)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return o.http.Do(req)
}

// login fetches (and caches) a bearer token for downloads.
func (o *OpenSubtitles) login(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// A token signed in with credentials since changed in Settings is dropped, so the
	// new account is the one in use straight away.
	acct := o.accountFingerprint()
	if o.token != "" && o.tokenFor == acct {
		return o.token, nil
	}
	o.token = ""
	if !o.CanDownload() {
		return "", fmt.Errorf("%w: OpenSubtitles username/password required to download", ErrNotConfigured)
	}
	resp, err := o.req(ctx, http.MethodPost, "/login", nil, map[string]string{
		"username": o.username(), "password": o.password(),
	}, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		err := &authError{what: "login", status: resp.StatusCode}
		o.noteAuth(false, err.Error())
		return "", err
	case http.StatusTooManyRequests:
		return "", o.rateLimit(resp.Header)
	case http.StatusOK:
	default:
		return "", fmt.Errorf("opensubtitles login: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("opensubtitles login: no token in response")
	}
	o.token, o.tokenFor = out.Token, acct
	o.noteAuth(true, "")
	return o.token, nil
}

// accountFingerprint identifies the credentials a token was signed in with, without
// keeping another copy of the password.
func (o *OpenSubtitles) accountFingerprint() string { return o.fingerprintFor(o.apiKey()) }

// fingerprintFor is accountFingerprint with another API key (a Test of a typed one).
func (o *OpenSubtitles) fingerprintFor(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey + "\x00" + o.username() + "\x00" + o.password()))
	return hex.EncodeToString(sum[:])
}

// Verify is the credentials Test, run on demand only. candidateKey is an API key typed
// and not yet saved (sent once, kept nowhere); empty means the saved one. The key is
// checked with a cheap Api-Key request; then, if the saved username and password are both
// set, it signs in fresh and reads the account (VIP, daily allowance, downloads left). A
// missing username or password is named, since search works without them but downloading
// doesn't. A Test of the saved credentials sets the state the header shows and the jobs
// obey: a rejected login pauses downloads until the credentials change, a good one clears
// that. A second press within two seconds gets the same answer without signing in again.
func (o *OpenSubtitles) Verify(ctx context.Context, candidateKey string) (string, error) {
	key := strings.TrimSpace(candidateKey)
	if key == "" {
		key = o.apiKey()
	}
	if key == "" {
		return "", ErrNotConfigured
	}
	fp := o.fingerprintFor(key)
	if detail, ok, err := o.verifyCached(fp); ok {
		return detail, err
	}
	detail, err := o.verify(ctx, key, key == o.apiKey())
	if ctx.Err() == nil {
		o.rememberVerify(fp, detail, err)
	}
	return detail, err
}

func (o *OpenSubtitles) verify(ctx context.Context, key string, saved bool) (string, error) {
	rejected := func(what string, status int) {
		if saved {
			o.noteAuth(false, (&authError{what: what, status: status}).Error())
		}
	}
	resp, err := o.reqKey(ctx, key, http.MethodGet, "/infos/formats", nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("Couldn't reach OpenSubtitles: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		rejected("API key", resp.StatusCode)
		return "", fmt.Errorf("OpenSubtitles rejected the API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("OpenSubtitles is rate-limiting requests — try again in %s", retryAfter(resp.Header, time.Now()).Round(time.Second))
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("OpenSubtitles answered HTTP %d", resp.StatusCode)
	}

	user, pass := o.username(), o.password()
	var missing []string
	if user == "" {
		missing = append(missing, "username")
	}
	if pass == "" {
		missing = append(missing, "password")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("The API key works, so search does, but downloading also needs the OpenSubtitles %s, which isn't set", strings.Join(missing, " and "))
	}

	resp, err = o.reqKey(ctx, key, http.MethodPost, "/login", nil, map[string]string{"username": user, "password": pass}, "")
	if err != nil {
		return "", fmt.Errorf("Couldn't reach OpenSubtitles to sign in: %w", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var login struct {
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &login)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		rejected("login", resp.StatusCode)
		return "", fmt.Errorf("Login rejected (%d): the API key works, but OpenSubtitles rejected the username or password", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("OpenSubtitles is rate-limiting sign-ins — try again in %s", retryAfter(resp.Header, time.Now()).Round(time.Second))
	case resp.StatusCode != http.StatusOK || login.Token == "":
		msg := login.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", fmt.Errorf("The API key works, but signing in failed: %s", msg)
	}
	if saved {
		o.noteAuth(true, "")
	}

	resp, err = o.reqKey(ctx, key, http.MethodGet, "/infos/user", nil, nil, login.Token)
	if err != nil {
		return "", fmt.Errorf("Signed in, but couldn't read the account: %w", err)
	}
	defer resp.Body.Close()
	var info struct {
		Data struct {
			AllowedDownloads   int    `json:"allowed_downloads"`
			RemainingDownloads *int   `json:"remaining_downloads"`
			Level              string `json:"level"`
			VIP                bool   `json:"vip"`
			ResetTimeUTC       string `json:"reset_time_utc"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info) != nil || info.Data.RemainingDownloads == nil {
		return "OK: API key accepted and signed in as " + user + ".", nil
	}
	left := *info.Data.RemainingDownloads
	if saved {
		o.noteAccount(info.Data.VIP, info.Data.AllowedDownloads)
		o.noteQuota(left, info.Data.ResetTimeUTC, left <= 0)
	}
	vip := ""
	if info.Data.VIP {
		vip = " (VIP)"
	}
	return fmt.Sprintf("OK: signed in as %s%s, %d downloads left today.", user, vip, left), nil
}

// Search finds candidate subtitles, best (most-downloaded) first.
func (o *OpenSubtitles) Search(ctx context.Context, sr SearchRequest) ([]SubtitleResult, error) {
	if !o.Available() {
		return nil, ErrNotConfigured
	}
	q := url.Values{}
	q.Set("languages", strings.ToLower(sr.Language))
	if id := strings.TrimPrefix(sr.IMDBID, "tt"); id != "" {
		if sr.Season > 0 {
			q.Set("parent_imdb_id", id)
		} else {
			q.Set("imdb_id", id)
		}
	} else if sr.Title != "" {
		q.Set("query", sr.Title)
	}
	if sr.Season > 0 {
		q.Set("type", "episode")
		q.Set("season_number", strconv.Itoa(sr.Season))
		q.Set("episode_number", strconv.Itoa(sr.Episode))
	} else {
		q.Set("type", "movie")
	}
	if sr.MovieHash != "" {
		q.Set("moviehash", sr.MovieHash)
	}
	// Forced (foreign-parts-only) uploads aren't the full subtitle, and AI-translated ones
	// are worse than our own whisper run. The API already leaves out machine translations
	// by default; ask it to leave these out too, and filter again below in case it doesn't.
	foreign := sr.ForeignParts
	if foreign != "only" {
		foreign = "exclude"
	}
	q.Set("foreign_parts_only", foreign)
	q.Set("ai_translated", "exclude")
	resp, err := o.req(ctx, http.MethodGet, "/subtitles", q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		err := &authError{what: "API key", status: resp.StatusCode}
		o.noteAuth(false, err.Error())
		return nil, err
	case http.StatusTooManyRequests:
		return nil, o.rateLimit(resp.Header)
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("opensubtitles search: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				Release         string `json:"release"`
				DownloadCount   int    `json:"download_count"`
				HearingImpaired bool   `json:"hearing_impaired"`
				HashMatch       bool   `json:"moviehash_match"`
				ForeignParts    bool   `json:"foreign_parts_only"`
				AITranslated    bool   `json:"ai_translated"`
				MachineTrans    bool   `json:"machine_translated"`
				Files           []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`

				// What scoring (scoring.go) compares against the file on disk.
				FPS         float64 `json:"fps"`
				FromTrusted bool    `json:"from_trusted"`
				Uploader    struct {
					Rank string `json:"rank"`
				} `json:"uploader"`
				Feature struct {
					Season  int `json:"season_number"`
					Episode int `json:"episode_number"`
				} `json:"feature_details"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var out []SubtitleResult
	for _, d := range payload.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		if d.Attributes.AITranslated || d.Attributes.MachineTrans || d.Attributes.ForeignParts != (foreign == "only") {
			continue
		}
		out = append(out, SubtitleResult{
			FileID:          strconv.Itoa(d.Attributes.Files[0].FileID),
			Language:        d.Attributes.Language,
			Release:         d.Attributes.Release,
			Downloads:       d.Attributes.DownloadCount,
			HearingImpaired: d.Attributes.HearingImpaired,
			HashMatch:       d.Attributes.HashMatch,
			FPS:             d.Attributes.FPS,
			Trusted:         d.Attributes.FromTrusted,
			FileName:        d.Attributes.Files[0].FileName,
			UploaderRank:    d.Attributes.Uploader.Rank,
			Season:          d.Attributes.Feature.Season,
			Episode:         d.Attributes.Feature.Episode,
			// Filtered out above already; carried so scoring would refuse them regardless.
			AITranslated:     d.Attributes.AITranslated || d.Attributes.MachineTrans,
			ForeignPartsOnly: d.Attributes.ForeignParts,
		})
	}
	// Ranking: a hash match is in sync by construction and beats everything; then a
	// plain subtitle over a hearing-impaired one (the SDH cues are noise for most
	// viewers, though an SDH file is still far better than none); then popularity.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HashMatch != out[j].HashMatch {
			return out[i].HashMatch
		}
		if out[i].HearingImpaired != out[j].HearingImpaired {
			return !out[i].HearingImpaired
		}
		return out[i].Downloads > out[j].Downloads
	})
	return out, nil
}

// Download resolves a file id to a temporary link, then fetches the subtitle bytes.
func (o *OpenSubtitles) Download(ctx context.Context, fileID string) ([]byte, error) {
	if o.quotaPaused() {
		return nil, ErrQuotaExhausted
	}
	link, err := o.downloadLink(ctx, fileID)
	if errors.Is(err, errSessionExpired) {
		// The cached token expired: sign in once more and ask again. A rejected login
		// says so itself; a fresh token refused too is the provider's problem, not a
		// reason to pause every download.
		link, err = o.downloadLink(ctx, fileID)
		if errors.Is(err, errSessionExpired) {
			err = fmt.Errorf("opensubtitles download: HTTP 401 even after signing in again")
		}
	}
	if err != nil {
		return nil, err
	}
	// Fetch the actual subtitle file from the temporary link.
	fr, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	fr.Header.Set("User-Agent", o.ua)
	fresp, err := o.http.Do(fr)
	if err != nil {
		return nil, err
	}
	defer fresp.Body.Close()
	if fresp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opensubtitles fetch: HTTP %d", fresp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(fresp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	// The bytes go straight to a sidecar next to the media, so make sure they are a
	// subtitle and not an HTML error page or an archive that would sit there as a
	// broken .srt forever.
	if !looksLikeSRT(data) {
		return nil, fmt.Errorf("opensubtitles fetch: response isn't an SRT file")
	}
	return data, nil
}

// errSessionExpired is a /download 401: the cached token is no longer accepted.
var errSessionExpired = errors.New("opensubtitles download: session expired")

// downloadLink asks /download for a file's temporary link, signing in first when there's
// no token for the credentials in use. A 406 is the daily quota (paused until the API's
// reset time); a 429 is the per-second limiter (a short wait, never a quota pause); a 401
// drops the token and returns errSessionExpired so the caller can sign in again once.
func (o *OpenSubtitles) downloadLink(ctx context.Context, fileID string) (string, error) {
	token, err := o.login(ctx)
	if err != nil {
		return "", err
	}
	id, _ := strconv.Atoi(fileID)
	resp, err := o.req(ctx, http.MethodPost, "/download", nil, map[string]any{"file_id": id}, token)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Link         string `json:"link"`
		Remaining    int    `json:"remaining"`
		ResetTimeUTC string `json:"reset_time_utc"`
		Message      string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	switch resp.StatusCode {
	case http.StatusOK:
		o.noteQuota(out.Remaining, out.ResetTimeUTC, out.Remaining <= 0)
		o.noteAuth(true, "")
	case http.StatusNotAcceptable:
		// The documented "quota exceeded": stop asking until the reset rather than
		// failing every file.
		o.noteQuota(0, out.ResetTimeUTC, true)
		return "", ErrQuotaExhausted
	case http.StatusTooManyRequests:
		return "", o.rateLimit(resp.Header)
	case http.StatusUnauthorized:
		o.mu.Lock()
		o.token = ""
		o.mu.Unlock()
		return "", errSessionExpired
	default:
		return "", fmt.Errorf("opensubtitles download: HTTP %d", resp.StatusCode)
	}
	if out.Link == "" {
		return "", fmt.Errorf("opensubtitles download: no link in response")
	}
	return out.Link, nil
}

// looksLikeSRT is a cheap sanity check: an SRT has a "-->" timing arrow within its first
// few kilobytes, and never starts with an HTML tag or a ZIP signature.
func looksLikeSRT(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	trimmed := bytes.TrimLeft(head, "\xEF\xBB\xBF \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("<")) || bytes.HasPrefix(b, []byte("PK\x03\x04")) {
		return false
	}
	return bytes.Contains(head, []byte("-->"))
}
