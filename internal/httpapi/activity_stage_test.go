package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// A finished torrent that hasn't imported is never shown as a healthy seed: the feed says
// which Finishing stage it's in and why — held for review, an import being retried — and
// only an imported one is seeding.
func TestDownloadsFeedStages(t *testing.T) {
	const (
		held     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
		retrying = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2"
		seeding  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3"
		running  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4"
		stranger = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa5"
		waiting  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa6"
	)
	done := func(hash, name, cat string) string {
		return fmt.Sprintf(`{"hash":%q,"name":%q,"state":"uploading","progress":1,"size":100,"amount_left":0,"category":%q}`, hash, name, cat)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			fmt.Fprint(w, "["+strings.Join([]string{
				done(held, "Alien.1979.1080p", "arrmada"),
				done(retrying, "Heat.1995.1080p", "arrmada"),
				done(seeding, "Dune.2021.2160p", "arrmada"),
				fmt.Sprintf(`{"hash":%q,"name":"Arrival.2016.1080p","state":"downloading","progress":0.5,"size":100,"amount_left":50,"category":"arrmada"}`, running),
				done(stranger, "Some.Linux.ISO", "linux"),
				done(waiting, "Up.2009.1080p", "arrmada"),
			}, ",")+"]")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mv := movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log)
	a := &api{deps: Deps{
		Store: st, Log: log, Downloads: dl, Movies: mv, Quality: quality.NewService(st.DB()),
		Automation: automation.New(mv, nil, dl, nil, st.DB(), nil, log, ""),
		Config:     config.Config{DownloadsDir: t.TempDir(), DownloadCategory: "arrmada"},
		ImportFailures: func() []library.FailureInfo {
			return []library.FailureInfo{{Hash: retrying, Attempts: 3, LastErr: "permission denied", NextRetry: time.Now().Add(8 * time.Minute)}}
		},
	}}
	db := st.DB()
	for i, g := range []struct{ hash, status string }{{held, "held"}, {retrying, "grabbed"}, {seeding, "imported"}, {running, "grabbed"}, {waiting, "grabbed"}} {
		if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored) VALUES (?, ?, ?, 1)`, i+1, i+100, fmt.Sprint("Film ", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO grabs (movie_id, version_id, title, indexer, media_type, info_hash, status) VALUES (?, 0, 'x', 'x', 'movie', ?, ?)`, i+1, g.hash, g.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO import_reviews (hash, name, media_type, expected_id, expected_title, reason, reason_code) VALUES (?, 'Alien.1979.1080p', 'movie', 1, 'Aliens', 'looks like Alien (1979)', 'mismatch')`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO imports (download_hash, source_path, target_path, title, size_bytes) VALUES (?, '', '/lib/Dune.mkv', 'Dune', 100)`, seeding); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	a.handleDownloadsFeed(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads", nil))
	var feed struct {
		Downloads []struct {
			Hash  string `json:"hash"`
			Stage string `json:"stage"`
			Why   string `json:"why"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, d := range feed.Downloads {
		got[d.Hash] = [2]string{d.Stage, d.Why}
	}
	want := map[string][2]string{
		held:     {"needs_review", "Held for review: it doesn't look like Aliens"},
		retrying: {"import_retrying", "Import failed 3× (permission denied) — next try in"},
		seeding:  {"seeding", ""},
		running:  {"downloading", ""},
		stranger: {"not_managed", "Not managed by Arrmada"},
		waiting:  {"importing", "Waiting for the next import check"},
	}
	for h, w := range want {
		g, ok := got[h]
		if !ok {
			t.Errorf("%s missing from the feed", h)
			continue
		}
		if g[0] != w[0] || !strings.HasPrefix(g[1], w[1]) || (w[1] == "" && g[1] != "") {
			t.Errorf("%s: stage %q why %q, want %q %q", h, g[0], g[1], w[0], w[1])
		}
	}
}
