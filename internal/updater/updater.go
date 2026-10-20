// Package updater is how the app asks for its own update. The app never touches Docker:
// an optional companion container (arrmada-updater, compose profile "updater", started by
// `install.sh --with-updater`) holds Docker's socket and runs update.sh when the app asks it
// to, over the private compose network with a shared token. This package is the app's side
// of that conversation, plus the rule for the optional automatic updates.
//
// With no ARRMADA_UPDATER_URL and ARRMADA_UPDATER_TOKEN there is no Client (FromEnv returns
// nil) and the app offers the install line to run by hand instead, as it always did.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
)

const (
	// KeyAutoEnabled is the setting for automatic updates. Off until an admin turns it on.
	KeyAutoEnabled = "auto_update_enabled"
	// KeyAutoLastTag is the release automatic updates last started for, so a release that
	// failed to install isn't started again every half hour.
	KeyAutoLastTag = "auto_update_last_tag"

	// HoldAfterRelease is how long a release is out before an automatic update takes it,
	// so a bad release that gets pulled or fixed quickly never reaches a server on its own.
	HoldAfterRelease = 24 * time.Hour
	// QuietFrom and QuietUntil bound the server's local hours (QuietFrom <= hour <
	// QuietUntil) in which an automatic update may start: the app restarts for a minute or
	// two, so it picks the middle of the night.
	QuietFrom  = 2
	QuietUntil = 6

	// Interval is how often the scheduled task looks at whether to update.
	Interval = 30 * time.Minute
)

// Updater states, as the updater container reports them.
const (
	StateIdle    = "idle"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// Errors the API maps to answers.
var (
	// ErrBusy: an update is already running.
	ErrBusy = errors.New("an update is already running")
	// ErrRefused: the updater didn't accept the app's token.
	ErrRefused = errors.New("the updater refused the request: its token doesn't match the one in .env")
)

// Progress is how an update is going, as the updater reports it.
type Progress struct {
	// State is idle, running, done or failed.
	State string `json:"state"`
	// StartedAt is when the last update started (RFC 3339, UTC); "" when none has.
	StartedAt string `json:"started_at"`
	// Log is the end of update.sh's output, as text.
	Log string `json:"log"`
}

// Client talks to the updater container.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// FromEnv returns a Client from ARRMADA_UPDATER_URL and ARRMADA_UPDATER_TOKEN, or nil when
// either is empty or the URL isn't a plain http(s) address: the one-click update isn't set up.
func FromEnv(getenv func(string) string) *Client {
	return New(getenv("ARRMADA_UPDATER_URL"), getenv("ARRMADA_UPDATER_TOKEN"), nil)
}

// New returns a Client for base (the updater's address) with token, or nil when either is
// empty or base isn't http or https. hc nil uses a client with a short timeout.
func New(base, token string, hc *http.Client) *Client {
	base, token = strings.TrimRight(strings.TrimSpace(base), "/"), strings.TrimSpace(token)
	if base == "" || token == "" {
		return nil
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: base, token: token, http: hc}
}

// Available reports whether there is an updater to ask. Safe on a nil Client.
func (c *Client) Available() bool { return c != nil }

// Apply starts the update. It returns as soon as the updater has started it (the app is
// restarted part-way through, so the answer can't wait for the end); Progress says how it
// goes. ErrBusy when one is already running.
func (c *Client) Apply(ctx context.Context) error {
	if c == nil {
		return errors.New("the updater isn't set up")
	}
	status, body, err := c.do(ctx, http.MethodPost, "?apply")
	if err != nil {
		return err
	}
	switch status {
	case http.StatusAccepted, http.StatusOK:
		return nil
	case http.StatusConflict:
		return ErrBusy
	case http.StatusUnauthorized:
		return ErrRefused
	default:
		return fmt.Errorf("the updater answered %d: %s", status, errorText(body))
	}
}

// Progress reads how the last or running update is going.
func (c *Client) Progress(ctx context.Context) (Progress, error) {
	if c == nil {
		return Progress{}, errors.New("the updater isn't set up")
	}
	status, body, err := c.do(ctx, http.MethodGet, "?status")
	if err != nil {
		return Progress{}, err
	}
	switch status {
	case http.StatusOK:
		var p Progress
		if err := json.Unmarshal(body, &p); err != nil {
			return Progress{}, fmt.Errorf("the updater's answer wasn't readable: %w", err)
		}
		return p, nil
	case http.StatusUnauthorized:
		return Progress{}, ErrRefused
	default:
		return Progress{}, fmt.Errorf("the updater answered %d: %s", status, errorText(body))
	}
}

func (c *Client) do(ctx context.Context, method, query string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/cgi-bin/updater"+query, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("couldn't reach the updater: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("couldn't read the updater's answer: %w", err)
	}
	return resp.StatusCode, body, nil
}

// errorText pulls {"error": "..."} out of an updater answer, or says it was something else.
func errorText(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return "no reason given"
}

// AutoInput is everything the automatic-update rule looks at.
type AutoInput struct {
	// Enabled is the setting; Configured says there is an updater to ask.
	Enabled, Configured bool
	// Status is the update check's last answer.
	Status updatecheck.Status
	// LastTag is KeyAutoLastTag.
	LastTag string
	// Now is the time, in the server's own zone (the quiet hours are its local hours).
	Now time.Time
}

// AutoDecision says whether an automatic update should start now, and if not, why: the
// reason is for the log and never shown as an error. It only ever follows published
// releases: a build that follows main, or one that doesn't know its commit, is left alone.
func AutoDecision(in AutoInput) (bool, string) {
	st := in.Status
	switch {
	case !in.Enabled:
		return false, "automatic updates are off"
	case !in.Configured:
		return false, "the updater isn't set up"
	case st.Host == updatecheck.HostUnraid:
		return false, "Unraid updates this container itself"
	case st.Channel != updatecheck.ChannelRelease && st.Channel != updatecheck.ChannelBuild:
		return false, "this install doesn't follow the published releases"
	case !st.Enabled || !st.Available || st.Latest == nil:
		return false, "no newer release is known"
	case st.Latest.Tag == in.LastTag:
		return false, "already started for " + st.Latest.Tag
	case st.Latest.PublishedAt.IsZero():
		return false, "the release date isn't known"
	case in.Now.Sub(st.Latest.PublishedAt) < HoldAfterRelease:
		return false, st.Latest.Tag + " is less than a day old"
	case in.Now.Hour() < QuietFrom || in.Now.Hour() >= QuietUntil:
		return false, "waiting for the quiet hours"
	}
	return true, ""
}
