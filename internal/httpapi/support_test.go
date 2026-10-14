package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/applog"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/config"
)

// logFixture is a route server whose Logs ring is persisted to a temp folder, with a
// logger that writes into it.
type logFixture struct {
	*routeServer
	ring    *applog.Ring
	log     *slog.Logger
	logPath string
	admin   *http.Cookie
}

// newLogFixture prepares the log folder (seed writes any older files first), starts
// persisting, and makes an admin.
func newLogFixture(t *testing.T, tweak func(*Deps), seed func(logPath string)) *logFixture {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "arrmada.log.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		seed(logPath)
	}
	ring := applog.NewRing(1000)
	ring.Restore(logPath)
	stop, err := ring.Persist(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	log := slog.New(applog.NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}), ring))
	s := newRouteServer(t, func(d *Deps) {
		d.Logs = ring
		d.Log = log
		if tweak != nil {
			tweak(d)
		}
	})
	_, admin := s.user(t, "owner-admin", auth.RoleAdmin)
	return &logFixture{routeServer: s, ring: ring, log: log, logPath: logPath, admin: admin}
}

// readZip returns every file in a zip response by name.
func readZip(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 'Download full log' is the whole history on disk — the rotated files and the active one,
// including a line logged a moment ago — and only an admin gets it.
func TestLogsDownloadIncludesRotated(t *testing.T) {
	f := newLogFixture(t, nil, func(p string) {
		writeLines(t, p+".2", `{"time_ms":1,"level":"INFO","msg":"oldest rotated"}`)
		writeLines(t, p+".1", `{"time_ms":2,"level":"INFO","msg":"newer rotated"}`)
	})
	f.log.Info("just now")
	_, mgr := f.user(t, "mgr", auth.RoleManager)
	if rec := f.do("GET", "/api/v1/logs/download", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("manager: HTTP %d, want 403", rec.Code)
	}
	rec := f.do("GET", "/api/v1/logs/download", f.admin)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition %q", cd)
	}
	files := readZip(t, rec.Body.Bytes())
	for name, want := range map[string]string{
		"arrmada.log.jsonl.2": "oldest rotated",
		"arrmada.log.jsonl.1": "newer rotated",
		"arrmada.log.jsonl":   "just now",
	} {
		if !strings.Contains(files[name], want) {
			t.Errorf("%s = %q, want it to hold %q", name, files[name], want)
		}
	}
}

// The Logs API answers increments and filters by module.
func TestLogsRouteIncrementalAndModule(t *testing.T) {
	f := newLogFixture(t, nil, nil)
	f.log.With("module", "convert").Info("encode started")
	f.log.With("module", "indexer").Info("search sent")

	var page struct {
		Entries  []applog.Entry `json:"entries"`
		LastSeq  uint64         `json:"last_seq"`
		Reset    bool           `json:"reset"`
		Modules  []string       `json:"modules"`
		Capacity int            `json:"capacity"`
	}
	get := func(q string) {
		t.Helper()
		page.Entries = nil
		rec := f.do("GET", "/api/v1/logs?"+q, f.admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d %s", q, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	get("module=convert")
	if len(page.Entries) != 1 || page.Entries[0].Message != "encode started" || page.Capacity != 1000 || len(page.Modules) == 0 {
		t.Fatalf("module=convert: %+v", page)
	}
	cursor := page.LastSeq
	f.log.With("module", "convert").Info("encode finished")
	get("after_seq=" + jsonNum(cursor))
	if len(page.Entries) != 1 || page.Entries[0].Message != "encode finished" || page.Reset {
		t.Fatalf("after_seq: %+v", page)
	}
	get("after_seq=99999")
	if !page.Reset {
		t.Errorf("a cursor from an earlier run should reset: %+v", page)
	}
	if rec := f.do("GET", "/api/v1/logs?after_seq=x", f.admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: HTTP %d", rec.Code)
	}
}

func jsonNum(n uint64) string { b, _ := json.Marshal(n); return string(b) }

// The support bundle is written to be shared publicly. Built from an install with
// audiobooks, people, Kindle addresses and every kind of credential — some of which have
// leaked into log lines the way real bugs leak them — none of those strings may appear
// anywhere in the zip.
func TestSupportBundleRedactsSecretsAndBooks(t *testing.T) {
	const (
		tmdbKey     = "tmdbKEY9f8e7d6c5b4a"
		plexToken   = "PlexTokZq81XwYy77"
		jellyKey    = "JellyKeyB0b0Fe77aa"
		smtpPass    = "smtpPa55wordXq"
		smtpUser    = "mailer-account-77"
		osPass      = "OpenSubsPw-551"
		indexerKey  = "IndexerKey77aa88bb"
		trackerPass = "TrackerPassQq19"
		qbitPass    = "QbitPass4455zz"
		webhookTok  = "WebhookTokenAbcdefXyz"
		appriseURL  = "pover://userkeyMarigold@apptokenSecret99"
		envPlex     = "EnvPlexToken-0099x"

		title1  = "Zephyrine Quillfeather Chronicles"
		title2  = "Brindlemoor: Saltwater Hymns"
		author  = "Oswin Thackeray-Pell"
		series  = "Lantern Cycle Gloaming"
		mark    = "Marigold Favourite Bit"
		person  = "marigoldbanks"
		plexNm  = "MarigoldOnPlex"
		kindle  = "marigold_77@kindle.com"
		titleID = "b12"
	)

	f := newLogFixture(t, func(d *Deps) {
		d.APIKeys = apikeys.NewStore(d.Settings)
		d.Config = config.Config{DataDir: d.Config.DataDir, PlexToken: envPlex}
	}, func(p string) {
		// Lines an older version wrote, before request logging used route patterns.
		writeLines(t, p+".1",
			`{"time_ms":10,"level":"INFO","msg":"audiobook server: request","attrs":"method=GET path=/api/items/b12/play query=q=zephyrine status=200 bytes=10 client=Lissen"}`,
			`{"time_ms":11,"level":"DEBUG","msg":"request","attrs":"method=GET path=/api/v1/books/42/audiobook status=200 dur_ms=3"}`,
			`{"time_ms":12,"level":"DEBUG","msg":"audiobook server: holding a jump back until playback continues from it","attrs":"user=`+person+` item=b12 saved=10 reported=2"}`,
			`torn line {"time_ms":`,
		)
	})
	ctx := context.Background()
	set := f.deps.Settings
	for k, v := range map[string]string{
		"apikey:tmdb": tmdbKey, "apikey:smtp_password": smtpPass, "apikey:smtp_username": smtpUser,
		"apikey:opensubtitles_password": osPass, "insights_plex_token": plexToken,
		"insights_plex_url": "http://192.168.1.5:32400", "apikey:jellyfin": jellyKey, "jellyfin_url": "http://192.168.1.6:8096", "convert_codec": "hevc", "module_books_enabled": "true",
	} {
		if err := set.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	db := f.st.DB()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO indexers (name, kind, url, api_key, username, password) VALUES ('T', 'torznab', 'http://t.example', ?, 'u', ?)`, indexerKey, trackerPass)
	exec(`INSERT INTO download_clients (name, kind, url, username, password) VALUES ('q', 'qbittorrent', 'http://qbit:8080', 'admin', ?)`, qbitPass)
	exec(`INSERT INTO notifications (name, kind, url) VALUES ('d', 'discord', ?)`, "discord://1234567/"+webhookTok)
	u, _ := f.user(t, person, auth.RoleRequester)
	exec(`UPDATE users SET plex_username = ?, kindle_email = ?, apprise_url = ? WHERE id = ?`, plexNm, kindle, appriseURL, u.ID)
	exec(`INSERT INTO books (ol_key, title, author, series_name) VALUES ('OL1W', ?, ?, ?)`, title1, author, series)
	exec(`INSERT INTO books (ol_key, title, author) VALUES ('OL2W', ?, ?)`, title2, author)
	exec(`INSERT INTO listen_bookmarks (user_id, item_key, time, title, created_at) VALUES (?, 'b12', 5, ?, 1)`, u.ID, mark)

	// Lines as today's code writes them, plus the leaks a careless line could add.
	books := f.log.With("module", "books")
	books.Info("book: imported", "title", title1, "edition", "audiobook", "format", "m4b")
	books.Info("book scan: imported", "title", "Brindlemoor", "audio", 1)
	f.log.With("module", "requests").Info("request created", "media", "book", "title", title2, "by", person, "auto_approve", false)
	f.log.With("module", "indexer").Debug("torznab page", "url", "http://t.example/api?t=search&apikey="+indexerKey+"&q=zephyrine+quillfeather+chronicles")
	f.log.With("module", "download").Info("grabbed", "release", "Zephyrine.Quillfeather.Chronicles.2024.M4B-GRP", "path", "/downloads/Oswin Thackeray-Pell - Brindlemoor")
	f.log.Warn("plex call failed", "err", "GET https://plex.tv/x?X-Plex-Token="+plexToken+": 401")
	f.log.Warn("jellyfin: call failed", "err", "MediaBrowser Token=\""+jellyKey+"\" refused")
	f.log.Warn("qbittorrent: login refused for password " + qbitPass)
	f.log.Info("mail: sent to "+kindle, "user", person)
	f.log.Info("notify: discord answered 404 for "+webhookTok, "who", plexNm)
	f.log.Info("audiobook server: signed in", "user", person, "client", "Lissen")
	f.log.Error("insights: plex said no", "detail", "token "+envPlex+" refused, tmdb "+tmdbKey+", smtp "+smtpUser+"/"+smtpPass+", subs "+osPass+", tracker "+trackerPass)
	f.log.Info("apprise: posting to " + appriseURL)
	f.log.Info("mail: bounce from someone@example.org")
	f.log.Info("listen: bookmark added", "bookmark", mark, "item", titleID)
	// A calendar feed link: only its hash is stored, and a careless line quoting the link
	// (or the bare token in an attr) must still not carry it out.
	feedTok, _, err := f.deps.Auth.RotateCalendarFeed(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var feedHash string
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM calendar_feeds WHERE user_id = ?`, u.ID).Scan(&feedHash); err != nil {
		t.Fatal(err)
	}
	f.log.Warn("calendar: fetch failed", "url", "https://arrmada.example.com/api/v1/calendar.ics?token="+feedTok+"&mine=1", "token", feedTok)
	f.log.Info("calendar: subscribed via https://arrmada.example.com/api/v1/calendar.ics?mine=1&token=" + feedTok + " ok")
	f.log.Info("calendar: stored " + feedHash)

	// Only the admin may build one.
	_, mgr := f.user(t, "mgr", auth.RoleManager)
	if rec := f.do("GET", "/api/v1/system/support-bundle", mgr); rec.Code != http.StatusForbidden {
		t.Errorf("manager: HTTP %d, want 403", rec.Code)
	}
	rec := f.do("GET", "/api/v1/system/support-bundle", f.admin)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	files := readZip(t, rec.Body.Bytes())
	for _, name := range []string{"README.txt", "version.json", "status.json", "health.json", "settings.json", "logs/arrmada.log.jsonl", "logs/arrmada.log.jsonl.1"} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle is missing %s (has %v)", name, keys(files))
		}
	}
	var all strings.Builder
	for name, body := range files {
		all.WriteString("== " + name + "\n" + body + "\n")
	}
	bundle := all.String()
	lower := strings.ToLower(bundle)

	forbidden := []string{
		tmdbKey, plexToken, jellyKey, smtpPass, smtpUser, osPass, indexerKey, trackerPass, qbitPass, webhookTok,
		"apptokenSecret99", "userkeyMarigold", envPlex,
		title1, title2, "Zephyrine", "Quillfeather", "Brindlemoor", "Saltwater", author, "Thackeray", series, mark,
		person, plexNm, kindle, "owner-admin",
		"/api/items/b12", "b12", "/books/42", "q=zephyrine",
		feedTok, feedHash,
	}
	for _, s := range forbidden {
		if strings.Contains(lower, strings.ToLower(s)) {
			i := strings.Index(lower, strings.ToLower(s))
			lo, hi := max(0, i-120), min(len(bundle), i+120)
			t.Errorf("bundle contains %q: …%s…", s, bundle[lo:hi])
		}
	}

	// Still useful: the lines are there and say what happened (checked in the logs alone —
	// the README names the placeholders too), and safe settings show.
	logs := files["logs/arrmada.log.jsonl.1"] + files["logs/arrmada.log.jsonl"]
	for _, want := range []string{"book: imported", "request created", "plex call failed", "GET /api/items/{id}/play", "/api/v1/books/{id}/audiobook", "«redacted»", "«user»", "«name»", "«email»", `"module":"books"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the bundle's logs should still contain %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "torn line") {
		t.Error("a torn line can't be scrubbed reliably and should be dropped")
	}
	var settings settingsDoc
	if err := json.Unmarshal([]byte(files["settings.json"]), &settings); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if settings.Included["convert_codec"] != "hevc" || settings.Included["module_books_enabled"] != "true" {
		t.Errorf("safe settings missing: %+v", settings.Included)
	}
	for _, k := range []string{"apikey:tmdb", "insights_plex_token", "insights_plex_url", "apikey:jellyfin", "jellyfin_url"} {
		if _, ok := settings.Included[k]; ok {
			t.Errorf("%s should be withheld", k)
		}
		if !slices.Contains(settings.Withheld, k) {
			t.Errorf("withheld should name %s: %v", k, settings.Withheld)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// What the bundle shows of the settings is decided by shape: a plain choice is shown, data
// and anything named like a credential is not.
func TestBundleSettingsAllowlist(t *testing.T) {
	got := bundleSettings(map[string]string{
		"convert_codec":       "hevc",
		"subtitle_languages":  "en,fr",
		"backup_hour":         "3",
		"quality_ranking":     "v1",
		"naming_movie_file":   "{Title} ({Year})",
		"library_movies_dir":  "/mnt/user/Movies",
		"mystery_value":       "a8f9c0d1e2b3a4f5c6d7",
		"apikey:omdb":         "short",
		"some_session_secret": "always",
	})
	for _, k := range []string{"convert_codec", "subtitle_languages", "backup_hour", "quality_ranking"} {
		if _, ok := got.doc.Included[k]; !ok {
			t.Errorf("%s should be included: %+v", k, got.doc)
		}
	}
	for _, k := range []string{"naming_movie_file", "library_movies_dir", "mystery_value", "apikey:omdb", "some_session_secret"} {
		if _, ok := got.doc.Included[k]; ok {
			t.Errorf("%s should be withheld", k)
		}
	}
	if len(got.doc.Withheld) != 5 {
		t.Errorf("withheld = %v", got.doc.Withheld)
	}
}
