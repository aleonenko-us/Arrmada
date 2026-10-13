package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTemplateRender(t *testing.T) {
	vars := map[string]string{"title": "Dune", "year": "2021", "link": "https://arrmada.example.com/movies/12", "message": "📥 Dune"}
	cases := []struct{ tmpl, want string }{
		{DefaultTemplate, "📥 Dune\nhttps://arrmada.example.com/movies/12"},
		{"🎬 {title} ({year}) — {link}", "🎬 Dune (2021) — https://arrmada.example.com/movies/12"},
		{"{title} {nosuch}!", "Dune !"}, // an unknown placeholder is left blank
		{"{title} {user}\n{link}\n", "Dune\nhttps://arrmada.example.com/movies/12"},
		{"{ title } {TITLE} {", "{ title } {TITLE} {"}, // not placeholders: left as typed
	}
	for _, c := range cases {
		if got := render(c.tmpl, vars); got != c.want {
			t.Errorf("render(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
	// No outside address: the default is just the message, with no dangling line.
	if got := render(DefaultTemplate, map[string]string{"message": "📥 Dune"}); got != "📥 Dune" {
		t.Errorf("without a link = %q", got)
	}
	// One pass: a title that looks like a placeholder stays text.
	if got := render("{title}", map[string]string{"title": "{link}", "link": "x"}); got != "{link}" {
		t.Errorf("a value was expanded again: %q", got)
	}
}

// Values from outside can't add lines, ping a whole Discord server, mention someone, or
// dress a link up as text.
func TestTemplateValuesEscaped(t *testing.T) {
	d := map[string]any{"user": "sam\n@everyone <@123> [free](https://evil.example)", "title": "Dune\r\nPart Two\x00", "year": 2024}
	vars := templateVars(eventStreamStarted, d, Message{Body: "▶️ @here started Dune"}, "")
	if strings.ContainsAny(vars["user"]+vars["title"], "\r\n\x00") {
		t.Errorf("control characters survived: %q / %q", vars["user"], vars["title"])
	}
	for _, bad := range []string{"@everyone", "<@", "](", "@here"} {
		if strings.Contains(vars["user"]+vars["message"], bad) {
			t.Errorf("%q survived: user=%q message=%q", bad, vars["user"], vars["message"])
		}
	}
	if vars["title"] != "Dune Part Two" || vars["year"] != "2024" {
		t.Errorf("title/year = %q / %q", vars["title"], vars["year"])
	}
	long := strings.Repeat("ab", 300)
	if got := cleanValue(long); len([]rune(got)) != maxValueRunes {
		t.Errorf("a long value is %d runes, want %d", len([]rune(got)), maxValueRunes)
	}
}

// End to end: with an outside address the default message carries a link that opens the
// movie; without one it sends without; a custom template on one connection changes only
// that connection's text; a push connection's text never carries the link.
func TestTemplatesPerConnection(t *testing.T) {
	s, rec, _ := newTestService(t)
	ctx := context.Background()
	plain := mustCreate(t, s, Connection{Name: "plain", URL: "ntfy://a", Events: sub("movie.imported"), Enabled: true})
	custom := mustCreate(t, s, Connection{Name: "custom", URL: "ntfy://b", Events: sub("movie.imported"), Enabled: true,
		Options: Options{Templates: map[string]string{"movie.imported": "🎬 {title} is ready ({media}) {link}"}}})
	push := mustCreate(t, s, Connection{Name: "push", Kind: KindWebPush, Events: sub("movie.imported"), Enabled: true, Config: PushConfigFor(1)})
	body := func(id int64) string {
		t.Helper()
		var b string
		if err := s.db.QueryRow(`SELECT body FROM notification_deliveries WHERE connection_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	data := map[string]any{"id": 12, "title": "Dune"}

	if n, err := s.Emit(ctx, "movie.imported", data); err != nil || n != 3 {
		t.Fatalf("Emit = %d, %v", n, err)
	}
	if got := body(plain.ID); got != "📥 Dune" {
		t.Errorf("no outside address: %q", got)
	}
	if got := body(custom.ID); got != "🎬 Dune is ready (movie)" {
		t.Errorf("custom without a link: %q", got)
	}

	s.SetPublicURL(func(context.Context) string { return "https://arrmada.example.com/" })
	if _, err := s.Emit(ctx, "movie.imported", data); err != nil {
		t.Fatal(err)
	}
	if got := body(plain.ID); got != "📥 Dune\nhttps://arrmada.example.com/movies/12" {
		t.Errorf("default with a link: %q", got)
	}
	if got := body(custom.ID); got != "🎬 Dune is ready (movie) https://arrmada.example.com/movies/12" {
		t.Errorf("custom with a link: %q", got)
	}
	if got := body(push.ID); got != "📥 Dune" {
		t.Errorf("push text: %q (the tap opens the link)", got)
	}
	var link string
	if err := s.db.QueryRow(`SELECT link FROM notification_deliveries WHERE connection_id = ? ORDER BY id DESC LIMIT 1`, push.ID).Scan(&link); err != nil || link != "/movies/12" {
		t.Errorf("push link = %q, %v (relative, whatever the outside address)", link, err)
	}
	_ = rec
}

func TestCleanTemplates(t *testing.T) {
	got, err := cleanTemplates(map[string]string{"movie.imported": "{title}\r\n{link}", "plex.buffering": "   "})
	if err != nil || len(got) != 1 || got["movie.imported"] != "{title}\n{link}" {
		t.Fatalf("cleanTemplates = %v, %v", got, err)
	}
	if _, err := cleanTemplates(map[string]string{"no.such": "x"}); err == nil {
		t.Error("an unknown event was accepted")
	}
	if _, err := cleanTemplates(map[string]string{"movie.imported": strings.Repeat("x", maxTemplateLen+1)}); err == nil {
		t.Error("an over-long template was accepted")
	}
	if _, err := cleanTemplates(map[string]string{"movie.imported": "a\x1bb"}); err == nil {
		t.Error("a control character was accepted")
	}
}

// The attachment goes before the "--" separator, joined to its flag so it can't be read
// as an option of its own, and the URLs still all follow the separator.
func TestAppriseArgsAttachBeforeSeparator(t *testing.T) {
	args := appriseArgs("T", "B", "-x/poster.jpg", []string{"discord://a/b"})
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
		}
	}
	if sep < 0 || args[sep-1] != "--attach=-x/poster.jpg" || len(args) != sep+2 || args[sep+1] != "discord://a/b" {
		t.Fatalf("args = %v", args)
	}
	if none := appriseArgs("T", "B", "", []string{"discord://a/b"}); strings.Contains(strings.Join(none, " "), "attach") {
		t.Errorf("no attachment, but args = %v", none)
	}
}

func TestAllowedAttachURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://image.tmdb.org/t/p/w500/dune.jpg":    true,
		"https://covers.openlibrary.org/b/id/1-L.jpg": true,
		"http://image.tmdb.org/t/p/w500/dune.jpg":     false, // not https
		"https://image.tmdb.org:8443/x.jpg":           false,
		"https://user@image.tmdb.org/x.jpg":           false,
		"https://169.254.169.254/latest/meta-data":    false,
		"https://image.tmdb.org.evil.example/x.jpg":   false,
		"file:///etc/passwd":                          false,
		"/library/metadata/1/thumb/2":                 false,
		"":                                            false,
	} {
		if got := AllowedAttachURL(u); got != want {
			t.Errorf("AllowedAttachURL(%q) = %v, want %v", u, got, want)
		}
	}
}

// attachRecorder records what each send was given.
type attachRecorder struct {
	mu      sync.Mutex
	attach  []string
	existed []bool
	fail    bool
}

func (r *attachRecorder) send(_ context.Context, _, _, _, attach string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attach = append(r.attach, attach)
	_, err := os.Stat(attach)
	r.existed = append(r.existed, err == nil)
	if r.fail {
		return errors.New("503")
	}
	return nil
}

const fakePlexToken = "PlexTok-abc123XYZ"

// A Plex stream's poster is fetched server-side into a temporary file, handed to Apprise
// as a file, and removed after the attempt — when it went through and when it failed. The
// Plex token is in neither the stored row nor what Apprise is given.
func TestPlexPosterTempFileCleanedUp(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, _, _ := newTestService(t)
		tmp := t.TempDir()
		s.SetTempDir(tmp)
		rec := &attachRecorder{fail: fail}
		s.SetAttachTransport(rec.send)
		var asked []string
		s.SetPosters(nil, func(_ context.Context, path string) (*http.Response, error) {
			asked = append(asked, path)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/jpeg"}},
				Body: io.NopCloser(strings.NewReader("\xff\xd8 not really a jpeg"))}, nil
		})
		ctx := context.Background()
		mustCreate(t, s, Connection{Name: "a", URL: "ntfy://a", Events: sub(eventStreamStarted), Enabled: true})
		d := streamData("42", "wan", "transcode")
		d["thumb"] = "/library/metadata/501/thumb/1700000000"
		if n, err := s.EmitOnce(ctx, eventStreamStarted, "", d); err != nil || n != 1 {
			t.Fatalf("EmitOnce = %d, %v", n, err)
		}
		var stored string
		if err := s.db.QueryRow(`SELECT attach FROM notification_deliveries`).Scan(&stored); err != nil || stored != "plex:/library/metadata/501/thumb/1700000000" {
			t.Fatalf("stored attach = %q, %v", stored, err)
		}
		drainNow(t, s)
		if len(rec.attach) != 1 || !rec.existed[0] || !strings.HasPrefix(rec.attach[0], tmp) {
			t.Fatalf("fail=%v: sends %v (existed %v), want one file under %s", fail, rec.attach, rec.existed, tmp)
		}
		for _, v := range append([]string{stored}, rec.attach...) {
			if strings.Contains(v, fakePlexToken) || strings.Contains(strings.ToLower(v), "token") {
				t.Errorf("the Plex token leaked into %q", v)
			}
		}
		if len(asked) != 1 || asked[0] != "/library/metadata/501/thumb/1700000000" {
			t.Errorf("image fetches = %v", asked)
		}
		left, _ := filepath.Glob(filepath.Join(tmp, "*"))
		if len(left) != 0 {
			t.Errorf("fail=%v: temp files left behind: %v", fail, left)
		}
	}
}

// A library alert carries the stored poster when it's from an allowed host, and nothing
// when it isn't; an alert still goes either way.
func TestLibraryPosterAttach(t *testing.T) {
	s, _, _ := newTestService(t)
	rec := &attachRecorder{}
	s.SetAttachTransport(rec.send)
	posters := map[int64]string{1: "https://image.tmdb.org/t/p/w500/dune.jpg", 2: "http://10.0.0.5/admin.jpg"}
	s.SetPosters(func(_ context.Context, kind string, id int64) string {
		if kind != "movie" {
			t.Errorf("kind = %q", kind)
		}
		return posters[id]
	}, nil)
	ctx := context.Background()
	mustCreate(t, s, Connection{Name: "a", URL: "ntfy://a", Events: sub("movie.imported"), Enabled: true})
	for _, id := range []int{1, 2} {
		if _, err := s.Emit(ctx, "movie.imported", map[string]any{"id": id, "title": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	drainNow(t, s)
	drainNow(t, s)
	if len(rec.attach) != 2 {
		t.Fatalf("sends = %v", rec.attach)
	}
	got := map[string]bool{}
	for _, a := range rec.attach {
		got[a] = true
	}
	if !got["https://image.tmdb.org/t/p/w500/dune.jpg"] || !got[""] {
		t.Errorf("attachments = %v, want the TMDB poster and none", rec.attach)
	}
}
