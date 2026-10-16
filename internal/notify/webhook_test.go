package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the webhook golden file and the docs' event reference")

// A signing secret as the page makes one: 32 random bytes, base64url.
const testSecret = "q3Zf1y7VJ0cK6m2Xr9T4bNw8LhPdUa5EsGiYoCjRkMt"

// hookCall is one request the test server got.
type hookCall struct {
	header http.Header
	body   []byte
}

// hookServer is a stand-in n8n: it records every request and answers with the next
// status in line (200 once they run out).
type hookServer struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []hookCall
	statuses []int
}

func newHookServer(t *testing.T, statuses ...int) *hookServer {
	t.Helper()
	h := &hookServer{statuses: statuses}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.calls = append(h.calls, hookCall{header: r.Header.Clone(), body: b})
		code := http.StatusOK
		if len(h.statuses) > 0 {
			code, h.statuses = h.statuses[0], h.statuses[1:]
		}
		h.mu.Unlock()
		if code == http.StatusNotFound {
			// n8n's own 404 quotes the webhook path; it must not reach the delivery log.
			http.Error(w, `{"message":"The requested webhook \"POST `+r.URL.Path+`\" is not registered."}`, code)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hookServer) got() []hookCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hookCall(nil), h.calls...)
}

// webhookConn is an n8n connection to url, subscribed to events, with extra fields.
func webhookConn(url string, extra map[string]string, events ...string) Connection {
	f := map[string]string{"url": url, "auth": "none", "signing_secret": testSecret}
	for k, v := range extra {
		f[k] = v
	}
	return Connection{Name: "n8n", Kind: KindN8N, URL: url, Enabled: true, Events: events, Config: PresetConfigFor(KindN8N, f)}
}

// verify checks a request's signature the way docs/n8n.md tells a workflow to.
func verify(t *testing.T, c hookCall, secret string) {
	t.Helper()
	ts := c.header.Get(HeaderTimestamp)
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Fatalf("timestamp header %q", ts)
	}
	if got, want := c.header.Get(HeaderSignature), Sign(secret, ts, c.body); got != want || !strings.HasPrefix(got, "sha256=") {
		t.Fatalf("signature %q, want %q", got, want)
	}
}

// Every catalog event has its webhook fields and a sample that formats; every declared
// field is in the data, and nothing undeclared is.
func TestEveryEventHasWebhookFields(t *testing.T) {
	keys := map[string]bool{}
	for _, def := range Catalog() {
		keys[def.Key] = true
		spec, ok := webhookEvents[def.Key]
		if !ok {
			t.Errorf("%s: no webhook fields in webhook_events.go", def.Key)
			continue
		}
		if _, ok := def.Format(WebhookSample(def.Key)); !ok {
			t.Errorf("%s: its sample doesn't format into a message", def.Key)
		}
		data := webhookData(def.Key, WebhookSample(def.Key))
		if len(data) != len(spec.fields) {
			t.Errorf("%s: %d data fields for %d declared (a name used twice?)", def.Key, len(data), len(spec.fields))
		}
		for _, f := range spec.fields {
			switch f.Type {
			case "string", "integer", "boolean", "string[]", "time":
			default:
				t.Errorf("%s.%s: unknown type %q", def.Key, f.Name, f.Type)
			}
			if strings.TrimSpace(f.Doc) == "" {
				t.Errorf("%s.%s: no doc", def.Key, f.Name)
			}
		}
		switch spec.severity {
		case "", "info", "warning", "error":
		default:
			t.Errorf("%s: severity %q", def.Key, spec.severity)
		}
	}
	for key := range webhookEvents {
		if !keys[key] {
			t.Errorf("webhook_events.go declares %s, which the catalog doesn't have", key)
		}
	}
}

// What a webhook may carry: no field that reads like a credential, a contact or anyone's
// listening (the audiobook privacy rule), and no catalog event about listening.
func TestWebhookFieldsKeepPrivacyRules(t *testing.T) {
	banned := []string{"token", "password", "secret", "api_key", "apikey", "email", "listen", "progress", "position", "ip", "user_id", "download_url", "info_hash"}
	for key, spec := range webhookEvents {
		if strings.Contains(key, "listen") || strings.Contains(key, "audio.") {
			t.Errorf("event %s is about listening", key)
		}
		for _, f := range spec.fields {
			for _, b := range banned {
				if f.Name == b || strings.Contains(f.Name, b+"_") || strings.HasSuffix(f.Name, "_"+b) {
					t.Errorf("%s.%s looks like it carries %q", key, f.Name, b)
				}
			}
		}
	}
	// A producer's extra keys never pass through: only declared fields are read.
	d := WebhookSample("plex.stream.started")
	d["user_id"], d["ip"], d["thumb"] = "12345", "203.0.113.9", "/library/metadata/1/thumb/2"
	for k := range webhookData("plex.stream.started", d) {
		if k == "user_id" || k == "ip" || k == "thumb" {
			t.Errorf("undeclared %s passed through", k)
		}
	}
}

// The payload for every event, built from its sample at a fixed time, matches the golden
// file. go test ./internal/notify -run TestWebhookPayloadGolden -update rewrites it; review
// the diff — a changed field is a change for everyone's workflows.
func TestWebhookPayloadGolden(t *testing.T) {
	at := time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)
	keys := []string{}
	for _, d := range Catalog() {
		keys = append(keys, d.Key)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, key := range keys {
		def, _ := Lookup(key)
		sample := WebhookSample(key)
		m, _ := def.Format(sample)
		if strings.HasSuffix(key, ".imported") && key != "music.imported" {
			m.Attach = "https://image.tmdb.org/t/p/w500/poster.jpg" // library alerts carry their poster
		}
		b, err := buildWebhookPayload(webhookInput{key: key, data: sample, msg: m, base: "https://arrmada.example.com", at: at})
		if err != nil {
			t.Fatal(err)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, b, "  ", "  "); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "  %q: %s", key, pretty.String())
		if i < len(keys)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	golden := filepath.Join("testdata", "webhook_payloads.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if got := out.String(); strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Fatalf("webhook payloads changed; run with -update and review the diff.\n%s", got)
	}
}

// The envelope: version, event, label, severity, time, links, instance, test flag.
func TestWebhookEnvelope(t *testing.T) {
	at := time.Date(2026, 10, 16, 9, 30, 0, 0, time.FixedZone("x", 3600))
	d := WebhookSample(EventHealthProblem)
	def, _ := Lookup(EventHealthProblem)
	m, _ := def.Format(d)
	b, err := buildWebhookPayload(webhookInput{key: EventHealthProblem, data: d, msg: m, at: at, test: true})
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	if env["version"] != float64(1) || env["event"] != EventHealthProblem || env["event_label"] != "Health problem" ||
		env["severity"] != "error" || env["occurred_at"] != "2026-10-16T08:30:00Z" || env["test"] != true || env["group"] != GroupNeedsYou {
		t.Fatalf("envelope = %s", b)
	}
	// No outside address: no absolute url, but the in-app path is there.
	if env["url"] != nil || env["path"] != "/activity/needs-you" || env["image_url"] != nil {
		t.Fatalf("links = %v %v %v", env["url"], env["path"], env["image_url"])
	}
	// A resolved problem carries the problem's level; the good news is still "info".
	if got := severityOf(EventHealthResolved, map[string]any{"count": 1, "title": "x", "level": "error"}); got != "info" {
		t.Fatalf("health.resolved severity = %q", got)
	}
	if got := severityOf(EventImportHeld, map[string]any{"count": 1, "title": "x"}); got != "warning" {
		t.Fatalf("import.held without a level = %q", got)
	}
	inst, _ := env["instance"].(map[string]any)
	if inst["name"] != "Arrmada" || inst["version"] == "" || inst["url"] != nil {
		t.Fatalf("instance = %v", inst)
	}
	// "<" and "&" aren't escaped, so n8n's JSON.stringify of the parsed body gives the
	// same bytes back; U+2028 never appears.
	lineSep := string(rune(0x2028))
	escaped := func(hex string) []byte { return []byte(`\` + "u" + hex) }
	d = map[string]any{"title": "Tom & Jerry <3" + lineSep, "id": 1}
	b, _ = buildWebhookPayload(webhookInput{key: "movie.imported", data: d, msg: Message{Title: "Imported", Body: "📥 Tom & Jerry"}, at: at})
	if !bytes.Contains(b, []byte(`"title":"Tom & Jerry <3 "`)) || bytes.Contains(b, []byte(lineSep)) ||
		bytes.Contains(b, escaped("2028")) || bytes.Contains(b, escaped("0026")) || bytes.Contains(b, escaped("003c")) {
		t.Fatalf("payload text = %s", b)
	}
}

// An alert to a webhook connection: one signed JSON POST with the event's data and the
// delivery headers, the same bytes that were stored when it was queued.
func TestWebhookDeliverySignedWithHeaderAuth(t *testing.T) {
	s, _, _ := newTestService(t)
	s.SetPublicURL(func(context.Context) string { return "https://arrmada.example.com/" })
	hook := newHookServer(t)
	ctx := context.Background()
	mustCreate(t, s, webhookConn(hook.URL+"/webhook/arrmada-7f3k", map[string]string{"auth": "header", "header_name": "X-N8N-Key", "header_value": "hv-0123456789"}, "movie.imported"))
	if n, err := s.EmitOnce(ctx, "movie.imported", "movie:12:1", WebhookSample("movie.imported")); err != nil || n != 1 {
		t.Fatalf("EmitOnce = %d, %v", n, err)
	}
	drainNow(t, s)
	calls := hook.got()
	if len(calls) != 1 {
		t.Fatalf("%d requests", len(calls))
	}
	c := calls[0]
	verify(t, c, testSecret)
	if c.header.Get("X-N8N-Key") != "hv-0123456789" || c.header.Get("Content-Type") != "application/json" ||
		c.header.Get(HeaderEvent) != "movie.imported" || c.header.Get(HeaderDelivery) != "1" ||
		!strings.HasPrefix(c.header.Get("User-Agent"), "Arrmada/") {
		t.Fatalf("headers = %v", c.header)
	}
	var env struct {
		Event string         `json:"event"`
		URL   string         `json:"url"`
		Test  bool           `json:"test"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal(c.body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Event != "movie.imported" || env.URL != "https://arrmada.example.com/movies/12" || env.Test ||
		env.Data["title"] != "Dune" || env.Data["tmdb_id"] != float64(438631) || env.Data["resolution"] != "2160p" || env.Data["version_id"] != nil {
		t.Fatalf("payload = %s", c.body)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT payload FROM notification_deliveries WHERE id = 1`).Scan(&stored); err != nil || stored != string(c.body) {
		t.Fatalf("stored payload %q (%v) differs from what was sent", stored, err)
	}
	if status, _, _, _ := row(t, s, 1); status != StatusSent {
		t.Fatalf("status %s", status)
	}
}

// Basic auth goes as an Authorization header; "none" sends no auth at all.
func TestWebhookBasicAuthAndNone(t *testing.T) {
	s, _, _ := newTestService(t)
	hook := newHookServer(t)
	ctx := context.Background()
	basic := mustCreate(t, s, webhookConn(hook.URL+"/b", map[string]string{"auth": "basic", "username": "arr", "password": " p@ss:word "}))
	none := mustCreate(t, s, webhookConn(hook.URL+"/n", nil))
	if err := s.Test(ctx, basic, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Test(ctx, none, ""); err != nil {
		t.Fatal(err)
	}
	calls := hook.got()
	if len(calls) != 2 {
		t.Fatalf("%d requests", len(calls))
	}
	req := &http.Request{Header: calls[0].header}
	if u, p, ok := req.BasicAuth(); !ok || u != "arr" || p != " p@ss:word " {
		t.Fatalf("basic auth = %q %q %v", u, p, ok)
	}
	if calls[1].header.Get("Authorization") != "" {
		t.Fatalf("auth none sent Authorization: %v", calls[1].header)
	}
	verify(t, calls[0], testSecret)
	verify(t, calls[1], testSecret)
}

// A refused send is retried on the queue's schedule, and the error in the delivery log
// names the problem without the webhook's path (n8n's 404 quotes it) or any secret.
func TestWebhookRetriesAndKeepsSecretsOutOfErrors(t *testing.T) {
	s, _, _ := newTestService(t)
	clk := newClock()
	useClock(s, clk)
	hook := newHookServer(t, http.StatusNotFound)
	ctx := context.Background()
	path := "/webhook/secret-path-a1b2c3"
	mustCreate(t, s, webhookConn(hook.URL+path, map[string]string{"auth": "header", "header_name": "X-Key", "header_value": "header-secret-value"}, "release.grabbed"))
	if _, err := s.EmitOnce(ctx, "release.grabbed", "", WebhookSample("release.grabbed")); err != nil {
		t.Fatal(err)
	}
	drainNow(t, s)
	status, attempts, next, lastErr := row(t, s, 1)
	if status != StatusQueued || attempts != 1 || next != clk.t.Add(time.Minute).Unix() {
		t.Fatalf("after a 404: %s %d %d", status, attempts, next)
	}
	if !strings.Contains(lastErr, "404") || !strings.Contains(lastErr, "workflow active") {
		t.Fatalf("error doesn't explain: %q", lastErr)
	}
	for _, secret := range []string{"secret-path-a1b2c3", "header-secret-value", testSecret} {
		if strings.Contains(lastErr, secret) {
			t.Fatalf("error quotes a secret: %q", lastErr)
		}
	}
	clk.advance(time.Minute)
	drainNow(t, s)
	if status, attempts, _, _ := row(t, s, 1); status != StatusSent || attempts != 2 {
		t.Fatalf("after retry: %s %d", status, attempts)
	}
	calls := hook.got()
	if len(calls) != 2 || !bytes.Equal(calls[0].body, calls[1].body) || calls[0].header.Get(HeaderDelivery) != calls[1].header.Get(HeaderDelivery) {
		t.Fatalf("a retry should send the same body under the same delivery id")
	}
	verify(t, calls[1], testSecret)

	// Nothing listening: the error names the server, never the path.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	c := webhookConn(deadURL+path, nil)
	err := s.Test(ctx, c, "")
	if err == nil || !strings.Contains(err.Error(), "couldn't connect to") || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("dead server: %v", err)
	}
}

// A redirect isn't followed: the auth header would go wherever it points.
func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	s, _, _ := newTestService(t)
	other := newHookServer(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	err := s.Test(context.Background(), webhookConn(redir.URL+"/hook", map[string]string{"auth": "header", "header_name": "X-Key", "header_value": "abcdef"}), "")
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect: %v", err)
	}
	if n := len(other.got()); n != 0 {
		t.Fatalf("the redirect was followed (%d requests)", n)
	}
}

// "Send test" for an event sends that event's realistic sample, marked as a test, and
// records it in the connection's log.
func TestWebhookTestSendsEventSample(t *testing.T) {
	s, _, _ := newTestService(t)
	hook := newHookServer(t)
	c := mustCreate(t, s, webhookConn(hook.URL+"/webhook/x", nil, "request.created"))
	if err := s.Test(context.Background(), c, "request.created"); err != nil {
		t.Fatal(err)
	}
	calls := hook.got()
	if len(calls) != 1 {
		t.Fatalf("%d requests", len(calls))
	}
	var env map[string]any
	_ = json.Unmarshal(calls[0].body, &env)
	data, _ := env["data"].(map[string]any)
	if env["event"] != "request.created" || env["test"] != true || env["title"] != "New request" || data["requested_by"] != "Sam" || data["request_id"] != float64(42) {
		t.Fatalf("test payload = %s", calls[0].body)
	}
	if !strings.HasPrefix(calls[0].header.Get(HeaderDelivery), "test-") {
		t.Fatalf("delivery id %q", calls[0].header.Get(HeaderDelivery))
	}
	list, _ := s.Deliveries(context.Background(), c.ID, 5)
	if len(list) != 1 || list[0].EventKey != "test" || list[0].Status != StatusSent {
		t.Fatalf("log = %+v", list)
	}
	// A plain test (no event) still goes, as event "test".
	if err := s.Test(context.Background(), c, ""); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(hook.got()[1].body, &env)
	if env["event"] != "test" || env["event_label"] != "Test" {
		t.Fatalf("plain test = %s", hook.got()[1].body)
	}
}

// A row queued before its connection became a webhook (no payload stored) still goes,
// with the message and no data.
func TestWebhookFallbackPayload(t *testing.T) {
	s, _, _ := newTestService(t)
	hook := newHookServer(t)
	ctx := context.Background()
	c := mustCreate(t, s, Connection{Name: "a", Kind: PresetCustom, URL: "ntfy://a", Enabled: true, Events: sub("release.grabbed")})
	if _, err := s.Dispatch(ctx, "release.grabbed", Message{Title: "Grabbed", Body: "🎬 Dune", Link: "/activity/queue"}); err != nil {
		t.Fatal(err)
	}
	wc := webhookConn(hook.URL+"/h", nil, "release.grabbed")
	if err := s.Update(ctx, c.ID, wc); err != nil {
		t.Fatal(err)
	}
	drainNow(t, s)
	calls := hook.got()
	if len(calls) != 1 {
		t.Fatalf("%d requests", len(calls))
	}
	var env map[string]any
	_ = json.Unmarshal(calls[0].body, &env)
	if env["event"] != "release.grabbed" || env["message"] != "🎬 Dune" || env["path"] != "/activity/queue" {
		t.Fatalf("fallback = %s", calls[0].body)
	}
	verify(t, calls[0], testSecret)
}

// The admin policy: any http(s) host, the LAN and loopback included (n8n usually lives
// there); nothing that isn't a plain http(s) address, and no credentials in the URL.
func TestValidateWebhookURL(t *testing.T) {
	for _, ok := range []string{
		"http://192.168.1.10:5678/webhook/arrmada", "http://localhost:5678/webhook/x", "http://127.0.0.1/x",
		"https://n8n.example.com/webhook/abc?x=1", "http://n8n:5678/webhook-test/abc", "http://[fd00::1]:5678/w",
	} {
		if err := ValidateWebhookURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "ftp://host/x", "json://host/x", "http:///nohost", "http://user:pw@host/x", "http://host/a b",
		"http://host/x\ny", "http://host/x\u0000", "//host/x", "http://host:99999/x", "javascript:alert(1)",
	} {
		if err := ValidateWebhookURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWebhookConfigChecks(t *testing.T) {
	base := func(extra map[string]string) map[string]string {
		f := map[string]string{"url": "http://192.168.1.10:5678/webhook/a", "auth": "none", "signing_secret": testSecret}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	if _, err := webhookConfigOf(base(nil)); err != nil {
		t.Fatalf("plain: %v", err)
	}
	for name, f := range map[string]map[string]string{
		"no secret":       base(map[string]string{"signing_secret": ""}),
		"short secret":    base(map[string]string{"signing_secret": "abc"}),
		"odd secret":      base(map[string]string{"signing_secret": strings.Repeat("a", 31) + "!"}),
		"unknown auth":    base(map[string]string{"auth": "oauth"}),
		"header no name":  base(map[string]string{"auth": "header", "header_value": "v"}),
		"header bad name": base(map[string]string{"auth": "header", "header_name": "X Key", "header_value": "v"}),
		"header reserved": base(map[string]string{"auth": "header", "header_name": "Content-Type", "header_value": "v"}),
		"header ours":     base(map[string]string{"auth": "header", "header_name": "X-Arrmada-Signature", "header_value": "v"}),
		"header no value": base(map[string]string{"auth": "header", "header_name": "X-Key"}),
		"header CRLF":     base(map[string]string{"auth": "header", "header_name": "X-Key", "header_value": "v\r\nX-Evil: 1"}),
		"basic no user":   base(map[string]string{"auth": "basic", "password": "p"}),
		"basic colon":     base(map[string]string{"auth": "basic", "username": "a:b", "password": "p"}),
		"bad url":         base(map[string]string{"url": "gotify://x/y"}),
	} {
		if _, err := webhookConfigOf(f); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), testSecret) || strings.Contains(err.Error(), "v\r\n") {
			t.Errorf("%s: error quotes a value: %v", name, err)
		}
	}
	// Built through the preset: the URL is the webhook address itself.
	u, err := BuildURL(KindN8N, base(map[string]string{"auth": "basic", "username": "u", "password": "p"}))
	if err != nil || u != "http://192.168.1.10:5678/webhook/a" {
		t.Fatalf("BuildURL = %q, %v", u, err)
	}
}

// Only the chosen authentication's fields are kept, and every secret is masked and
// listed for the support bundle.
func TestWebhookFieldsStoredAndMasked(t *testing.T) {
	f := map[string]string{
		"url": "http://n8n:5678/webhook/p4th", "auth": "header", "header_name": "X-Key", "header_value": "hv-secret",
		"username": "left-over", "password": "left-over-pw", "signing_secret": testSecret,
	}
	cfg := PresetConfigFor(KindN8N, f)
	c := Connection{Kind: KindN8N, Config: cfg}
	got := PresetFields(c)
	if got["username"] != "" || got["password"] != "" || got["header_value"] != "hv-secret" {
		t.Fatalf("stored fields = %v", got)
	}
	masked := MaskedFields(KindN8N, got)
	raw, _ := json.Marshal(masked)
	for _, secret := range []string{"p4th", "hv-secret", testSecret} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("masked fields show %q: %s", secret, raw)
		}
	}
	if masked["header_name"] != "X-Key" || masked["auth"] != "header" || masked["signing_secret_set"] != true || masked["url_set"] != true {
		t.Fatalf("masked = %s", raw)
	}
	vals := SecretValues(KindN8N, string(cfg))
	sort.Strings(vals)
	want := []string{"http://n8n:5678/webhook/p4th", "hv-secret", testSecret}
	sort.Strings(want)
	if strings.Join(vals, "|") != strings.Join(want, "|") {
		t.Fatalf("SecretValues = %v", vals)
	}
	if h := URLHint("http://n8n:5678/webhook/p4th"); h != "http://n8n:5678/••••" {
		t.Fatalf("hint = %q", h)
	}
}

// Sign is HMAC-SHA256 over "timestamp.body" — the value the docs' snippet computes.
func TestSignKnownAnswer(t *testing.T) {
	// printf '1700000000.{"a":1}' | openssl dgst -sha256 -hmac key
	if got := Sign("key", "1700000000", []byte(`{"a":1}`)); got != "sha256=a438e398bfafc57e4396bb7fc2304422f0f768e965d073ca313cb52e22e6ad03" {
		t.Fatalf("Sign = %q", got)
	}
	a := Sign("key", "1700000000", []byte(`{"a":1}`))
	if a == Sign("key", "1700000001", []byte(`{"a":1}`)) || a == Sign("key2", "1700000000", []byte(`{"a":1}`)) || len(a) != len("sha256=")+64 {
		t.Fatalf("the signature doesn't cover the timestamp and the key")
	}
}
