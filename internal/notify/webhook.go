package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
)

// n8n / Webhook connections. Instead of a title and a body through Apprise, the alert is
// POSTed as a structured, versioned JSON document (webhookEnvelope) to a URL — usually an
// n8n Webhook node, but any endpoint that takes JSON works. People build their own
// automations from it: post to Discord their way, log to a sheet, run a script.
//
// The connection is a preset (presets.go) whose fields are the URL, an optional
// authentication (a header, as n8n's Header Auth, or HTTP Basic), and a signing secret.
// All three secrets are write-only like any preset secret: never returned, masked in the
// list, scrubbed from errors and the support bundle.
//
// Every request is signed: X-Arrmada-Timestamp is the send time in unix seconds and
// X-Arrmada-Signature is "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
// The timestamp is inside what's signed so a captured request can't be replayed later
// with a fresh one; a receiver checks the signature and that the timestamp is recent.
//
// Where it may point: anywhere. This is an admin-only connection, and the admin's
// Apprise connections already take json://, form:// and webhook:// URLs to any host —
// the LAN included, which is where n8n usually runs (http://192.168.x.x:5678). Holding
// this one to public addresses would refuse its main use while protecting nothing an
// admin can't already do. Requesters can't make one (see usernotify.go: their personal
// links are held to public push services). Redirects aren't followed, so a request with
// the auth header can't be bounced to a second host.

// KindN8N is the n8n / Webhook connection kind. Not "webhook": the very first alert
// connections stored that as a free-form label on an Apprise URL (migration 0020), and
// those rows must keep sending through Apprise.
const KindN8N = "n8n"

// WebhookVersion is the payload's "version". It goes up only when a field is removed or
// changes meaning; new fields are added without a bump.
const WebhookVersion = 1

// The headers every webhook request carries.
const (
	HeaderSignature = "X-Arrmada-Signature"
	HeaderTimestamp = "X-Arrmada-Timestamp"
	HeaderEvent     = "X-Arrmada-Event"
	HeaderDelivery  = "X-Arrmada-Delivery"
)

// The authentication choices.
const (
	webhookAuthNone   = "none"
	webhookAuthHeader = "header"
	webhookAuthBasic  = "basic"
)

const (
	webhookTimeout    = 15 * time.Second
	maxWebhookURL     = 2048
	maxWebhookString  = 2000 // runes per text value in the payload
	minSigningSecret  = 32
	maxSigningSecret  = 128
	maxHeaderValueLen = 1024
)

var (
	reHeaderName    = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,64}$")
	reSigningSecret = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// reservedHeaders are the ones Arrmada sets itself (or the HTTP client owns).
	reservedHeaders = map[string]bool{
		"host": true, "content-type": true, "content-length": true, "transfer-encoding": true,
		"connection": true, "user-agent": true, "te": true, "trailer": true, "upgrade": true,
		"x-arrmada-signature": true, "x-arrmada-timestamp": true, "x-arrmada-event": true, "x-arrmada-delivery": true,
	}
)

// IsWebhook reports whether a connection is set up as an n8n / Webhook one: kind "n8n"
// with its preset fields and a plain http(s) URL. Before this kind existed, kind was a
// free-form label, so a row can say "n8n" over an Apprise URL (an API script's, say);
// that row keeps going through Apprise exactly as it always did.
func IsWebhook(c Connection) bool {
	return isWebhookConfig(c.Kind, c.URL, c.Config)
}

func isWebhookConfig(kind, rawURL string, config json.RawMessage) bool {
	if kind != KindN8N {
		return false
	}
	f := PresetFields(Connection{Kind: kind, Config: config})
	if f == nil || strings.TrimSpace(f["url"]) == "" {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// ValidateWebhookURL checks a webhook URL: http or https, a host, no credentials in it
// (they go under Authentication), no spaces. Any host is allowed — see the top of this
// file for why.
func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("the webhook URL is empty")
	}
	if len(raw) > maxWebhookURL {
		return errors.New("the webhook URL is too long")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("the webhook URL must not contain spaces")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return errors.New("the webhook URL couldn't be read — paste the whole address, like http://192.168.1.10:5678/webhook/arrmada")
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return errors.New("the webhook URL must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return errors.New("the webhook URL has no server in it")
	}
	if u.User != nil {
		return errors.New("put a username and password under Authentication, not in the URL")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return errors.New("the webhook URL's port should be a number like 5678")
		}
	}
	return nil
}

// webhookConfig is a webhook connection's settings, read from its preset fields.
type webhookConfig struct {
	url         string
	auth        string
	headerName  string
	headerValue string
	username    string
	password    string
	secret      string
}

// webhookConfigOf reads and checks a webhook connection's fields. Errors name the field,
// never its value.
func webhookConfigOf(f map[string]string) (webhookConfig, error) {
	get := func(name string) string { return strings.TrimSpace(f[name]) }
	c := webhookConfig{url: get("url"), auth: get("auth"), secret: get("signing_secret")}
	if c.url == "" {
		return c, errors.New("Webhook URL is required")
	}
	if err := ValidateWebhookURL(c.url); err != nil {
		return c, err
	}
	if c.auth == "" {
		c.auth = webhookAuthNone
	}
	switch c.auth {
	case webhookAuthNone:
	case webhookAuthHeader:
		c.headerName, c.headerValue = get("header_name"), f["header_value"]
		if !reHeaderName.MatchString(c.headerName) {
			return c, errors.New("the header name should be one word like X-Arrmada-Key or Authorization")
		}
		if reservedHeaders[strings.ToLower(c.headerName)] {
			return c, fmt.Errorf("Arrmada sets %s itself — pick another header name", c.headerName)
		}
		if strings.TrimSpace(c.headerValue) == "" {
			return c, errors.New("Header value is required for header authentication")
		}
		if err := plainHeaderValue(c.headerValue); err != nil {
			return c, err
		}
	case webhookAuthBasic:
		// The password isn't trimmed: spaces at either end of one are part of it.
		c.username, c.password = get("username"), f["password"]
		if c.username == "" {
			return c, errors.New("Username is required for basic authentication")
		}
		if strings.Contains(c.username, ":") {
			return c, errors.New("the username can't contain a colon")
		}
		if err := plainHeaderValue(c.username + c.password); err != nil {
			return c, err
		}
	default:
		return c, errors.New("authentication should be none, header or basic")
	}
	if c.secret == "" {
		return c, errors.New("Signing secret is required")
	}
	if len(c.secret) < minSigningSecret || len(c.secret) > maxSigningSecret || !reSigningSecret.MatchString(c.secret) {
		return c, fmt.Errorf("the signing secret should be %d to %d letters, numbers, - and _ — use Make a new one", minSigningSecret, maxSigningSecret)
	}
	return c, nil
}

// plainHeaderValue refuses what can't go in a header: line breaks and control characters.
func plainHeaderValue(v string) error {
	if len(v) > maxHeaderValueLen {
		return errors.New("the authentication value is too long")
	}
	if strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) }) {
		return errors.New("the authentication value can't contain line breaks or control characters")
	}
	return nil
}

// FieldError is a refusal about one form field, so the page can show it beside that field.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Msg }

// WebhookKept is which of a saved webhook's fields may fill the blanks in a new form
// (an edit, or a Test of an unsaved edit). A saved header value or password only goes to
// the server it was saved for: when the form's URL points at another scheme, host or
// port, they're left out, and a form that relies on one gets a FieldError asking for it
// again. A path change on the same server keeps them — that's n8n's switch from its test
// URL to its production URL. The signing secret always carries over: it isn't sent, only
// used to sign.
func WebhookKept(form, stored map[string]string) (map[string]string, error) {
	if stored == nil {
		return nil, nil
	}
	newURL := strings.TrimSpace(form["url"])
	if newURL == "" || sameOrigin(newURL, stored["url"]) {
		return stored, nil
	}
	kept := map[string]string{}
	for k, v := range stored {
		if k != "header_value" && k != "password" {
			kept[k] = v
		}
	}
	const again = " — the saved one isn't sent to a different server than it was saved for, so enter it again"
	switch strings.TrimSpace(form["auth"]) {
	case webhookAuthHeader:
		if strings.TrimSpace(form["header_value"]) == "" && stored["header_value"] != "" {
			return nil, &FieldError{Field: "header_value", Msg: "Header value: the webhook URL now points at another server" + again}
		}
	case webhookAuthBasic:
		if form["password"] == "" && stored["password"] != "" {
			return nil, &FieldError{Field: "password", Msg: "Password: the webhook URL now points at another server" + again}
		}
	}
	return kept, nil
}

// sameOrigin reports whether two URLs share scheme, host and port (a missing port being
// the scheme's own). Anything unreadable counts as different.
func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	origin := func(u *url.URL) string {
		scheme := strings.ToLower(u.Scheme)
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[scheme]
		}
		return scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
	}
	return origin(ua) == origin(ub)
}

// webhookFieldsFor keeps only the fields the chosen authentication uses, so switching
// from header to basic doesn't leave the old header value stored.
func webhookFieldsFor(f map[string]string) map[string]string {
	out := map[string]string{}
	keep := []string{"url", "auth", "signing_secret"}
	switch strings.TrimSpace(f["auth"]) {
	case webhookAuthHeader:
		keep = append(keep, "header_name", "header_value")
	case webhookAuthBasic:
		keep = append(keep, "username", "password")
	}
	for _, k := range keep {
		if v, ok := f[k]; ok {
			out[k] = v
		}
	}
	return out
}

// scrub blanks the values that must never show in an error, a log or the delivery log:
// the whole URL and its path and query (n8n's webhook path works like a password), the
// auth and the secret. Whole values only — the server's name may stay, the connection's
// hint shows it anyway.
func (c webhookConfig) scrub(s string) string {
	vals := []string{c.url, c.headerValue, c.password, c.secret}
	if u, err := url.Parse(c.url); err == nil {
		vals = append(vals, u.EscapedPath(), u.Path, u.RawQuery)
	}
	for _, v := range vals {
		if len(strings.TrimSpace(v)) >= 4 {
			s = strings.ReplaceAll(s, v, mask)
		}
	}
	return s
}

// Instance names the Arrmada a payload came from.
type Instance struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	URL     *string `json:"url"`
}

// webhookEnvelope is the JSON a webhook connection is sent. Field order here is the order
// on the wire. See docs/n8n.md for what each one means.
type webhookEnvelope struct {
	Version    int            `json:"version"`
	Event      string         `json:"event"`
	EventLabel string         `json:"event_label"`
	Group      string         `json:"group"`
	Severity   string         `json:"severity"`
	OccurredAt string         `json:"occurred_at"`
	Test       bool           `json:"test"`
	Title      string         `json:"title"`
	Message    string         `json:"message"`
	URL        *string        `json:"url"`
	Path       *string        `json:"path"`
	ImageURL   *string        `json:"image_url"`
	Data       map[string]any `json:"data"`
	Instance   Instance       `json:"instance"`
}

// webhookInput is what a payload is built from.
type webhookInput struct {
	key  string
	data map[string]any // the event's payload; nil when the producer sent a ready message
	msg  Message
	base string // the outside address ("" = not set)
	at   time.Time
	test bool
}

// buildWebhookPayload writes the JSON for one event. title and message are the catalog's
// own words, neutralized like every chat message (a Plex user can't @everyone through
// them); the values under data are raw, so a workflow that posts them to chat escapes them
// itself.
func buildWebhookPayload(in webhookInput) ([]byte, error) {
	env := webhookEnvelope{
		Version: WebhookVersion, Event: in.key, EventLabel: in.key, Group: "",
		Severity: "info", OccurredAt: in.at.UTC().Format(time.RFC3339), Test: in.test,
		Title: jsonText(neutralize(in.msg.Title)), Message: jsonText(neutralize(in.msg.Body)),
		Data:     map[string]any{},
		Instance: Instance{Name: "Arrmada", Version: buildinfo.Version},
	}
	if def, ok := Lookup(in.key); ok {
		env.EventLabel, env.Group = def.Label, def.Group
		env.Severity = severityOf(in.key, in.data)
		env.Data = webhookData(in.key, in.data)
	} else if in.key == "test" {
		env.EventLabel = "Test"
	}
	if in.base != "" {
		b := in.base
		env.Instance.URL = &b
	}
	if rel := in.msg.Link; inAppPath(rel) {
		env.Path = &rel
		if in.base != "" {
			abs := in.base + rel
			env.URL = &abs
		}
	}
	if AllowedAttachURL(in.msg.Attach) {
		img := in.msg.Attach
		env.ImageURL = &img
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// "<", ">" and "&" stay as they are, so a receiver that re-serialises the parsed body
	// (n8n's JSON.stringify) gets the same bytes the signature was made over.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// inAppPath reports whether rel is an in-app path ("/movies/12") a link may be made from.
func inAppPath(rel string) bool {
	return rel != "" && strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "//") && !strings.ContainsAny(rel, " \t\r\n\\")
}

// jsonText makes a value safe to put in the payload: control characters other than line
// breaks and tabs are dropped, U+2028/U+2029 become spaces (Go escapes them and
// JavaScript doesn't, which would break a signature check over re-serialised JSON), and
// it's capped in length.
func jsonText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == 0x2028 || r == 0x2029: // line and paragraph separators
			return ' '
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxWebhookString {
		s = string(r[:maxWebhookString-1]) + "…"
	}
	return s
}

// Sign is the X-Arrmada-Signature value for body sent at timestamp (unix seconds):
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newWebhookClient is the client webhook sends go through: a bounded wait and no
// redirects (a redirect would carry the auth header to wherever it points).
func newWebhookClient() *http.Client {
	return &http.Client{
		Timeout:       webhookTimeout,
		Transport:     http.DefaultTransport.(*http.Transport).Clone(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// deliverWebhook POSTs a payload to a webhook connection. The error is in plain words and
// never quotes the URL's path, the auth or the secret.
func (s *Service) deliverWebhook(ctx context.Context, c Connection, body []byte, event, deliveryID string) error {
	cfg, err := webhookConfigOf(PresetFields(c))
	if err != nil {
		return fmt.Errorf("this connection isn't set up right: %v", err)
	}
	ts := strconv.FormatInt(s.now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("the webhook URL couldn't be used")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Arrmada/"+buildinfo.Version)
	req.Header.Set(HeaderEvent, event)
	req.Header.Set(HeaderDelivery, deliveryID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(cfg.secret, ts, body))
	switch cfg.auth {
	case webhookAuthHeader:
		req.Header.Set(cfg.headerName, cfg.headerValue)
	case webhookAuthBasic:
		req.SetBasicAuth(cfg.username, cfg.password)
	}
	resp, err := s.webhook.Do(req)
	if err != nil {
		// The raw error can quote what the other end sent (a non-HTTP service's banner),
		// so it stays in the debug log; the delivery log and Test get fixed words.
		s.log.Debug("notify: webhook send failed", "connection", c.Name, "err", cfg.scrub(err.Error()))
		return errors.New(cfg.scrub(webhookSendError(err, req.URL.Host)))
	}
	defer resp.Body.Close()
	// The answer's body isn't read into anything: n8n's 404 quotes the webhook's path.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return webhookStatusError(resp.StatusCode)
}

// webhookSendError says why a request didn't get an answer, in fixed words that name the
// server (which the connection's hint shows anyway) and nothing else: never the URL, and
// never the error's own text, which can carry what the other end sent back (an SSH or SMTP
// banner on the wrong port reads as `malformed HTTP response "<banner>"`).
func webhookSendError(err error, host string) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var (
		ne        net.Error
		dnsErr    *net.DNSError
		opErr     *net.OpError
		recordErr tls.RecordHeaderError
		certErr   *tls.CertificateVerificationError
		authErr   x509.UnknownAuthorityError
		nameErr   x509.HostnameError
		invalid   x509.CertificateInvalidError
		alertErr  tls.AlertError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return fmt.Sprintf("%s didn't answer within %d seconds", host, int(webhookTimeout.Seconds()))
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("couldn't look up %s — check the server name", hostOnly(host))
	case errors.As(err, &recordErr):
		return fmt.Sprintf("%s didn't answer with HTTPS — use http:// for it, or check the port", host)
	case errors.As(err, &nameErr):
		return fmt.Sprintf("%s's certificate is for a different name — use the name on its certificate", host)
	case errors.As(err, &certErr) || errors.As(err, &authErr) || errors.As(err, &invalid):
		return fmt.Sprintf("%s's certificate isn't trusted (self-signed or expired?) — use a trusted certificate, or http:// on your own network", host)
	case errors.As(err, &alertErr):
		return fmt.Sprintf("%s refused the secure connection (TLS) — check it's the right address and port", host)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return fmt.Sprintf("couldn't connect to %s — is n8n running, and can Arrmada reach it from its container?", host)
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET):
		return fmt.Sprintf("%s closed the connection without answering — is that the right address and port?", host)
	}
	return fmt.Sprintf("%s answered with something that isn't HTTP — is that the right address and port?", host)
}

// hostOnly is host without its port.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// webhookStatusError is a refusal in words, with the likely fix for the answers n8n gives.
func webhookStatusError(code int) error {
	msg := fmt.Sprintf("the webhook answered %d %s", code, http.StatusText(code))
	switch {
	case code == http.StatusNotFound:
		msg += " — is the n8n workflow active? Its test URL (/webhook-test/…) only answers while “Listen for test event” is on; use the Production URL"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		msg += " — check the authentication matches the Webhook node's"
	case code >= 300 && code < 400:
		msg += " — it redirects, which isn't followed; use the address it redirects to"
	case code == http.StatusMethodNotAllowed:
		msg += " — set the Webhook node's HTTP Method to POST"
	}
	return errors.New(msg)
}

// fallbackWebhookPayload is a payload for a queued row that has none — one queued before
// its connection was switched to a webhook. It carries the message without the data.
func fallbackWebhookPayload(event string, m Message, at time.Time, base string) []byte {
	b, err := buildWebhookPayload(webhookInput{key: event, msg: m, base: base, at: at})
	if err != nil {
		return []byte("{}")
	}
	return b
}

// severityOf is "info", "warning" or "error": for a problem, the level its payload says
// (the Needs-you and health problems carry one), else the event's own default.
func severityOf(key string, d map[string]any) string {
	if levelSeverity[key] {
		switch str(d, "level") {
		case "error":
			return "error"
		case "warning":
			return "warning"
		}
	}
	if spec, ok := webhookEvents[key]; ok && spec.severity != "" {
		return spec.severity
	}
	return "info"
}
