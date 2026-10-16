package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// unmanagedEnv is a store, a fake qBittorrent whose torrent list a test can change, and
// an api wired to both, over temp folders only.
type unmanagedEnv struct {
	a        *api
	st       *store.Store
	runner   *jobs.Runner
	mu       sync.Mutex
	torrents []string
}

func (e *unmanagedEnv) set(torrents ...string) {
	e.mu.Lock()
	e.torrents = torrents
	e.mu.Unlock()
	e.a.deps.Downloads.InvalidateSnapshot()
}

func doneTorrent(hash, name, category, path string) string {
	return fmt.Sprintf(`{"hash":%q,"name":%q,"state":"uploading","progress":1,"size":100,"amount_left":0,"category":%q,"content_path":%q}`, hash, name, category, path)
}

func newUnmanagedEnv(t *testing.T) *unmanagedEnv {
	t.Helper()
	e := &unmanagedEnv{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			e.mu.Lock()
			fmt.Fprint(w, "["+strings.Join(e.torrents, ",")+"]")
			e.mu.Unlock()
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
	e.st = st
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(context.Background(), download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := importTestDirs(t)
	mv := movies.NewService(st.DB(), nil, nil, cfg.MoviesDir, "", nil, log)
	e.a = &api{deps: Deps{
		Store: st, Log: log, Downloads: dl, Movies: mv, Settings: settings.NewService(st.DB()),
		Library:    library.NewManager(st.DB(), cfg.MoviesDir, nil, log),
		Automation: automation.New(mv, nil, dl, nil, st.DB(), nil, log, ""),
		Config:     cfg,
	}}
	// Import into… runs as a job; a real runner records it.
	runner, err := jobs.New(context.Background(), st.DB(), log, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Shutdown(5 * time.Second) })
	e.runner = runner
	e.a.deps.Jobs = runner
	return e
}

// importJob follows an Import into… answer's job to its end.
func (e *unmanagedEnv) importJob(t *testing.T, rec *httptest.ResponseRecorder) jobs.Job {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import = %d %s, want 202", rec.Code, rec.Body.String())
	}
	return waitReviewJob(t, e.runner, decodeAccepted(t, rec.Body.String()).JobID)
}

func (e *unmanagedEnv) list(t *testing.T) (hashes []string, ignored int) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.a.handleUnmanaged(rec, httptest.NewRequest(http.MethodGet, "/api/v1/downloads/unmanaged", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unmanaged = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Unmanaged []unmanagedRow `json:"unmanaged"`
		Ignored   int            `json:"ignored"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Unmanaged {
		hashes = append(hashes, r.Hash)
	}
	return hashes, out.Ignored
}

func (e *unmanagedEnv) post(t *testing.T, handler http.HandlerFunc, path, hash, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.SetPathValue("hash", hash)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

const (
	strangerA = "dd00000000000000000000000000000000000001"
	strangerB = "dd00000000000000000000000000000000000002"
	ours      = "dd00000000000000000000000000000000000003"
	inOwnCat  = "dd00000000000000000000000000000000000004"
)

// Only finished torrents nothing in Arrmada grabbed, outside its categories, are
// unmanaged. Ignoring one hides it for as long as it is in the client; once it has left,
// the stored list forgets it.
func TestUnmanagedIgnoreRoundTripsAndPrunes(t *testing.T) {
	e := newUnmanagedEnv(t)
	if _, err := e.st.DB().Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, status) VALUES (1, 'Ours', 'x', 'movie', ?, 'imported')`, ours); err != nil {
		t.Fatal(err)
	}
	dl := e.a.deps.Config.DownloadsDir
	e.set(
		doneTorrent(strangerA, "Linux.Distro.ISO", "linux", filepath.Join(dl, "a")),
		doneTorrent(strangerB, "Some.Film.2001.1080p", "", filepath.Join(dl, "b")),
		doneTorrent(ours, "Ours.2020.1080p", "linux", filepath.Join(dl, "c")),
		doneTorrent(inOwnCat, "Theirs.2020.1080p", "arrmada", filepath.Join(dl, "d")),
	)
	if got, _ := e.list(t); fmt.Sprint(got) != fmt.Sprint([]string{strangerA, strangerB}) {
		t.Fatalf("unmanaged = %v, want only the two strangers", got)
	}
	if rec := e.post(t, e.a.handleIgnoreDownload, "/", strangerA, ""); rec.Code != http.StatusOK {
		t.Fatalf("ignore = %d %s", rec.Code, rec.Body.String())
	}
	// Ignoring one of Arrmada's own isn't a thing.
	if rec := e.post(t, e.a.handleIgnoreDownload, "/", ours, ""); rec.Code != http.StatusNotFound {
		t.Errorf("ignoring Arrmada's own download = %d, want 404", rec.Code)
	}
	if got, ignored := e.list(t); fmt.Sprint(got) != fmt.Sprint([]string{strangerB}) || ignored != 1 {
		t.Errorf("after ignore: %v (ignored %d), want only B and one ignored", got, ignored)
	}
	// A leaves the client: the list is pruned on the next read.
	e.set(doneTorrent(strangerB, "Some.Film.2001.1080p", "", filepath.Join(dl, "b")))
	if _, ignored := e.list(t); ignored != 0 {
		t.Errorf("ignored = %d after A left, want 0", ignored)
	}
	if raw := e.a.deps.Settings.Get(context.Background(), ignoredHashesKey, ""); raw != "[]" {
		t.Errorf("stored list = %q, want it pruned to []", raw)
	}
}

// Import into… reads only the download and library folders: a torrent whose files are
// anywhere else on the host is refused before anything touches them.
func TestImportUnmanagedOutsideTheRootsIsRefused(t *testing.T) {
	e := newUnmanagedEnv(t)
	outside := filepath.Join(filepath.Dir(e.a.deps.Config.DownloadsDir), "elsewhere", "Some.Film.2001.1080p")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "film.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Some Film', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	e.set(doneTorrent(strangerA, "Some.Film.2001.1080p", "", outside))
	rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("import from outside the roots = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if m, _ := e.a.deps.Movies.Get(context.Background(), 1); m.HasFile {
		t.Error("the film was imported anyway")
	}
	// Bad requests never reach the client or the disk.
	if rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"podcast","target_id":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d, want 400", rec.Code)
	}
}

// An unmanaged finished film imported into the movie the user picked lands in the
// library, and the torrent then counts as imported: it leaves the unmanaged list.
func TestImportUnmanagedIntoAMovie(t *testing.T) {
	e := newUnmanagedEnv(t)
	src := filepath.Join(e.a.deps.Config.DownloadsDir, "Some.Film.2001.1080p.BluRay-GRP")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// A sparse 60 MB file: over the importer's sample floor without writing 60 MB.
	f, err := os.Create(filepath.Join(src, "Some.Film.2001.1080p.BluRay-GRP.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(60 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := e.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Some Film', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	e.set(doneTorrent(strangerA, "Some.Film.2001.1080p.BluRay-GRP", "", src))
	defer library.ForceCopies()() // as onto another drive: the job reports the copy
	j := e.importJob(t, e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`))
	if j.Status != jobs.StatusSucceeded || j.Message != "Imported into Some Film." || j.Kind != downloadImportKind || j.Target != "torrent:"+strangerA {
		t.Fatalf("job = %+v", j)
	}
	if m, _ := e.a.deps.Movies.Get(context.Background(), 1); !m.HasFile {
		t.Error("the movie has no file after the import")
	}
	if got, _ := e.list(t); len(got) != 0 {
		t.Errorf("still unmanaged after its import: %v", got)
	}
	// A second import of the same torrent isn't offered any more.
	if rec := e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`); rec.Code != http.StatusNotFound {
		t.Errorf("second import = %d, want 404", rec.Code)
	}
}
