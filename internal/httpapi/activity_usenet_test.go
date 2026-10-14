package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// listedUsenet is the fake usenet client with a queue.
type listedUsenet struct {
	*fakeUsenetClient
	items []download.Item
}

func (l listedUsenet) List(context.Context, download.Client) ([]download.Item, error) {
	return l.items, nil
}

// The feed marks a usenet job and carries what the page reads for one — its network, the
// bytes left, a failure's reason and kind — and says Unpacking while SABnzbd post-processes
// and Imported (never Seeding) once a finished job is in the library.
func TestDownloadsFeedUsenet(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	dl := download.NewService(st.DB(), log)
	dl.SetDownloader(download.KindSABnzbd, listedUsenet{&fakeUsenetClient{}, []download.Item{
		{Hash: "sab-down", Name: "Film.Down", State: "downloading", RawState: "Downloading", Progress: 0.4, SizeBytes: 1000, RemainingBytes: 600, Category: "arrmada"},
		{Hash: "sab-unpack", Name: "Film.Unpack", State: download.PhaseUnpacking, RawState: "Extracting", Progress: 1, SizeBytes: 1000, Category: "arrmada"},
		{Hash: "sab-done", Name: "Film.Done", State: download.StateCompleted, RawState: "Completed", Progress: 1, SizeBytes: 1000, Category: "arrmada"},
		{Hash: "sab-disk", Name: "Film.Disk", State: "error", RawState: "Failed", SizeBytes: 1000, RemainingBytes: 1000, Category: "arrmada",
			FailMessage: "Unpacking failed, write error or disk is full?"},
	}})
	if _, err := dl.Create(ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mv := movies.NewService(st.DB(), nil, nil, t.TempDir(), "", nil, log)
	a := &api{deps: Deps{
		Store: st, Log: log, Downloads: dl, Movies: mv, Quality: quality.NewService(st.DB()),
		Automation: automation.New(mv, nil, dl, nil, st.DB(), nil, log, ""),
		Config:     config.Config{DownloadsDir: t.TempDir(), DownloadCategory: "arrmada"},
	}}
	for i, g := range []struct{ key, status string }{{"sab-down", "grabbed"}, {"sab-unpack", "grabbed"}, {"sab-done", "imported"}, {"sab-disk", "grabbed"}} {
		if _, err := st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored) VALUES (?, ?, 'Film', 1)`, i+1, i+100); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, status, transport) VALUES (?, 'x', 'x', 'movie', ?, ?, 'usenet')`, i+1, g.key, g.status); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	a.handleDownloadsFeed(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads", nil))
	var feed struct {
		Downloads []struct {
			Hash           string `json:"hash"`
			Stage          string `json:"stage"`
			Why            string `json:"why"`
			Transport      string `json:"transport"`
			RemainingBytes int64  `json:"remaining_bytes"`
			FailMessage    string `json:"fail_message"`
			FailKind       string `json:"fail_kind"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for i, d := range feed.Downloads {
		got[d.Hash] = i
		if d.Transport != "usenet" {
			t.Errorf("%s transport = %q", d.Hash, d.Transport)
		}
	}
	if len(got) != 4 {
		t.Fatalf("feed = %s", rec.Body)
	}
	d := feed.Downloads
	if x := d[got["sab-down"]]; x.Stage != "downloading" || x.RemainingBytes != 600 {
		t.Errorf("downloading = %+v", x)
	}
	if x := d[got["sab-unpack"]]; x.Stage != "unpacking" || x.Why != "SABnzbd is checking and unpacking it" {
		t.Errorf("unpacking = %+v", x)
	}
	if x := d[got["sab-done"]]; x.Stage != "imported" {
		t.Errorf("imported = %+v, want imported, never seeding", x)
	}
	if x := d[got["sab-disk"]]; x.Stage != "failed" || x.FailKind != "environment" || x.FailMessage == "" || !strings.Contains(x.Why, "Free up space") {
		t.Errorf("disk full = %+v", x)
	}
}

// An uploaded .nzb is previewed — name, files, sizes — and says it's Usenet.
func TestGrabPreviewNZB(t *testing.T) {
	a := &api{deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	nzb := `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head></head>
<file subject="x - &quot;film.mkv&quot; yEnc (1/1)"><segments><segment bytes="1234" number="1">a@b</segment></segments></file></nzb>`
	body := `{"torrent":"` + base64.StdEncoding.EncodeToString([]byte(nzb)) + `","filename":"Film.2020.nzb"}`
	rec := httptest.NewRecorder()
	a.handleGrabPreview(rec, httptest.NewRequest(http.MethodPost, "/api/v1/grab/preview", strings.NewReader(body)))
	var got struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"size_bytes"`
		Network   string `json:"network"`
		Files     []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	if got.Name != "Film.2020" || got.SizeBytes != 1234 || got.Network != "usenet" || len(got.Files) != 1 || got.Files[0].Path != "film.mkv" {
		t.Errorf("preview = %+v", got)
	}
}
