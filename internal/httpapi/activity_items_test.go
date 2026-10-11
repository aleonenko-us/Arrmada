package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/store"
)

// ACQ-29: a transfer card shows the title its grab was for — id, title, poster, version
// and who asked — found by info hash, even when the torrent's name parses to another
// film. A torrent no grab knows gets no title, however much its name looks like one.
func TestDownloadsFeedLinksTitleByHash(t *testing.T) {
	const hash = "abcdef0123456789abcdef0123456789abcdef02"
	const stranger = "0000000000000000000000000000000000000003"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			fmt.Fprintf(w, `[{"hash":%q,"name":"Heat.1995.2160p.UHD.BluRay-GRP","state":"downloading","progress":0.4,"size":100,"amount_left":60},
				{"hash":%q,"name":"Aliens.1986.1080p.BluRay-OTHER","state":"downloading","progress":0.1,"size":100,"amount_left":90}]`, hash, stranger)
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
	ctx := context.Background()
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mv := movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log)
	coord := automation.New(mv, nil, dl, nil, st.DB(), nil, log, "")
	rq := requests.NewService(st.DB(), mv, nil, nil, coord, quality.NewService(st.DB()), nil, "", log)
	a := &api{deps: Deps{
		Store: st, Log: log, Downloads: dl, Movies: mv, Quality: quality.NewService(st.DB()),
		Automation: coord, Requests: rq,
		Config: config.Config{DownloadsDir: t.TempDir()},
	}}
	for _, q := range []string{
		// The grab is for Aliens' 4K version; the torrent is named for Heat.
		`INSERT INTO movies (id, tmdb_id, title, year, poster_url, monitored, min_availability) VALUES (1, 679, 'Aliens', 1986, 'https://img/aliens.jpg', 1, 'announced')`,
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (2, 949, 'Aliens', 1986, 1, 'announced')`,
		`INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name) VALUES ('movie', 679, 'Aliens', 'pending', 3, 'mum')`,
	} {
		if _, err := st.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	v, err := mv.AddVersion(ctx, 1, "4K", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO grabs (movie_id, version_id, title, indexer, media_type, info_hash, replaces_path)
		VALUES (1, ?, 'Aliens 1986 2160p', 'x', 'movie', ?, '/media/old.mkv')`, v.ID, hash); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	a.handleDownloadsFeed(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads", nil))
	var feed struct {
		Downloads []struct {
			Hash string        `json:"hash"`
			Item *transferItem `json:"item"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Downloads) != 2 {
		t.Fatalf("downloads = %s", rec.Body.String())
	}
	for _, d := range feed.Downloads {
		switch d.Hash {
		case hash:
			it := d.Item
			if it == nil || it.Kind != "movie" || it.ID != 1 || it.Title != "Aliens" || it.Year != 1986 || it.PosterURL != "https://img/aliens.jpg" {
				t.Fatalf("grabbed torrent's title: %+v, want movie 1 Aliens (1986) with its poster", it)
			}
			if it.Scope != "4K" || !it.Upgrade {
				t.Errorf("scope %q upgrade %v, want the version's label and an upgrade", it.Scope, it.Upgrade)
			}
			if fmt.Sprint(it.RequestedBy) != "[mum]" {
				t.Errorf("requested_by = %v, want [mum]", it.RequestedBy)
			}
		case stranger:
			if d.Item != nil {
				t.Errorf("a torrent no grab knows got a title by its name: %+v", d.Item)
			}
		}
	}
}
