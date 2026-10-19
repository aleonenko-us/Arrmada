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
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// stallQbit serves one qBittorrent's torrent list.
func stallQbit(t *testing.T, torrents ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			fmt.Fprint(w, "["+strings.Join(torrents, ",")+"]")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The feed marks a download stalled by the same rule the stall fail-over's clock follows
// (automation.QueueStall) — an Arrmada grab by its stall clock, anything else by the
// client's own stalled state, each held for the grace — with when it stopped, and the
// stage agrees. Each row names the download client it's in, by its card's name.
func TestDownloadsFeedStalledAndClient(t *testing.T) {
	const (
		grabStalled = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1" // a grab, no progress for 2h
		blip        = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2" // stalledDL, data a minute ago
		noFileList  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb3" // nobody's, metaDL for 3h
		moving      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb4" // a grab with an old clock, data arriving
		paused      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb5" // a grab with an old clock, paused
	)
	now := time.Now()
	unix := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	torrent := func(hash, name, state string, dlspeed, lastActivity, addedOn int64) string {
		return fmt.Sprintf(`{"hash":%q,"name":%q,"state":%q,"progress":0.4,"size":1000,"amount_left":600,"dlspeed":%d,"last_activity":%d,"added_on":%d,"category":"arrmada"}`,
			hash, name, state, dlspeed, lastActivity, addedOn)
	}
	one := stallQbit(t,
		torrent(grabStalled, "Arrival.2016.1080p", "stalledDL", 0, unix(time.Hour), unix(3*time.Hour)), // last data an hour ago, after the grab clock
		torrent(blip, "Some.Show.S01E01.1080p", "stalledDL", 0, unix(time.Minute), unix(3*time.Hour)),
		torrent(paused, "Up.2009.1080p", "pausedDL", 0, 0, unix(5*time.Hour)),
	)
	two := stallQbit(t,
		torrent(noFileList, "Some.Linux.ISO", "metaDL", 0, 0, unix(3*time.Hour)),
		torrent(moving, "Heat.1995.1080p", "downloading", 2_000_000, unix(0), unix(4*time.Hour)),
	)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	dl := download.NewService(st.DB(), log)
	first, err := dl.Create(ctx, download.Client{Name: "qBittorrent", Kind: download.KindQbittorrent, URL: one, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := dl.Create(ctx, download.Client{Name: "qBittorrent 2", Kind: download.KindQbittorrent, URL: two, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mv := movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log)
	a := &api{deps: Deps{
		Store: st, Log: log, Downloads: dl, Movies: mv, Quality: quality.NewService(st.DB()),
		Automation: automation.New(mv, nil, dl, nil, st.DB(), nil, log, ""),
		Config:     config.Config{DownloadsDir: t.TempDir(), DownloadCategory: "arrmada"},
	}}
	clock := now.Add(-2 * time.Hour)
	for i, hash := range []string{grabStalled, moving, paused} {
		if _, err := st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored) VALUES (?, ?, ?, 1)`, i+1, i+100, fmt.Sprint("Film ", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`INSERT INTO grabs (movie_id, version_id, title, indexer, media_type, info_hash, status, progress, progress_at)
			VALUES (?, 0, 'x', 'x', 'movie', ?, 'grabbed', 0.4, ?)`, i+1, hash, clock.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	a.handleDownloadsFeed(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads", nil))
	var feed struct {
		Downloads []struct {
			Hash         string `json:"hash"`
			Stage        string `json:"stage"`
			Why          string `json:"why"`
			Stalled      bool   `json:"stalled"`
			StalledSince int64  `json:"stalled_since"`
			ClientID     int64  `json:"client_id"`
			ClientName   string `json:"client_name"`
		} `json:"downloads"`
		Totals struct {
			Stalled int `json:"stalled"`
		} `json:"totals"`
		Clients clientsState `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	type row struct {
		stage   string
		stalled bool
		since   int64 // 0: not checked
		client  int64
		name    string
	}
	want := map[string]row{
		grabStalled: {"stalled", true, unix(time.Hour), first.ID, "qBittorrent"}, // the later sign of life
		blip:        {"downloading", false, 0, first.ID, "qBittorrent"},
		paused:      {"paused", false, 0, first.ID, "qBittorrent"},
		noFileList:  {"stalled", true, unix(3 * time.Hour), second.ID, "qBittorrent 2"},
		moving:      {"downloading", false, 0, second.ID, "qBittorrent 2"},
	}
	seen := 0
	for _, d := range feed.Downloads {
		w, ok := want[d.Hash]
		if !ok {
			continue
		}
		seen++
		if d.Stage != w.stage || d.Stalled != w.stalled || d.ClientID != w.client || d.ClientName != w.name {
			t.Errorf("%s: stage %q stalled %v client %d %q, want %q %v %d %q", d.Hash, d.Stage, d.Stalled, d.ClientID, d.ClientName, w.stage, w.stalled, w.client, w.name)
		}
		if w.stalled && (d.StalledSince < w.since-1 || d.StalledSince > w.since+1) {
			t.Errorf("%s: stalled since %d, want %d", d.Hash, d.StalledSince, w.since)
		}
		if !w.stalled && d.StalledSince != 0 {
			t.Errorf("%s: stalled_since %d on a download that isn't stalled", d.Hash, d.StalledSince)
		}
		if d.Hash == blip && d.Why != "Waiting for peers" {
			t.Errorf("a moment without data says %q, want Waiting for peers", d.Why)
		}
	}
	if seen != len(want) {
		t.Fatalf("feed has %d of the %d downloads: %s", seen, len(want), rec.Body.String())
	}
	if feed.Totals.Stalled != 2 {
		t.Errorf("totals.stalled = %d, want 2 (by the same rule as the rows)", feed.Totals.Stalled)
	}
	if feed.Clients.Configured != 2 || feed.Clients.Enabled != 2 {
		t.Errorf("clients = %+v, want two configured and enabled", feed.Clients)
	}
}
