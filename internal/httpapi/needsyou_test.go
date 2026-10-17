package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Needs you's Retry now drops the importer's back-off, so the next import sweep attempts
// the download at once; a download that isn't failing answers 404.
func TestRetryImportClearsTheBackoff(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lib := library.NewManager(st.DB(), t.TempDir(), nil, log)
	const hash = "cafe000000000000000000000000000000000001"
	// An import that fails: its files aren't there.
	lib.Process(context.Background(), []library.Candidate{{Hash: hash, Name: "Film.2020.1080p", ContentPath: filepath.Join(t.TempDir(), "gone")}})
	if len(lib.Failures()) != 1 {
		t.Fatalf("failures = %+v, want the one that failed", lib.Failures())
	}
	a := &api{deps: Deps{Store: st, Log: log, Library: lib}}

	call := func(h string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/imports/"+h+"/retry", nil)
		req.SetPathValue("hash", h)
		rec := httptest.NewRecorder()
		a.handleRetryImport(rec, req)
		return rec.Code
	}
	if code := call(strings.ToUpper(hash)); code != http.StatusOK {
		t.Fatalf("retry = %d, want 200", code)
	}
	if f := lib.Failures(); len(f) != 0 {
		t.Errorf("after Retry now the back-off is still there: %+v", f)
	}
	if code := call(hash); code != http.StatusNotFound {
		t.Errorf("retrying what isn't failing = %d, want 404", code)
	}
}

// Move to TV files a misfiled download under Arrmada's TV category through the client,
// and refuses any other category — a torrent can't be pushed into a category Arrmada
// doesn't own, or into one that would import it as something else.
func TestSetCategoryOnlyArrmadasTV(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			fmt.Fprint(w, `[]`)
		case "/api/v2/torrents/setAutoManagement":
		case "/api/v2/torrents/setCategory":
			_ = r.ParseForm()
			mu.Lock()
			posted = append(posted, r.FormValue("hashes")+" "+r.FormValue("category"))
			mu.Unlock()
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
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(context.Background(), download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	a := &api{deps: Deps{Store: st, Log: log, Downloads: dl, Automation: automation.New(nil, nil, dl, nil, st.DB(), nil, log, ""), Config: config.Config{}}}

	call := func(hash, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/queue/"+hash+"/category", strings.NewReader(body))
		req.SetPathValue("hash", hash)
		rec := httptest.NewRecorder()
		a.handleSetCategory(rec, req)
		return rec.Code
	}
	const hash = "beef000000000000000000000000000000000001"
	if code := call(hash, `{"category":"arrmada"}`); code != http.StatusBadRequest {
		t.Errorf("into the movie category = %d, want 400", code)
	}
	if code := call(hash, `{"category":"linux"}`); code != http.StatusBadRequest {
		t.Errorf("into a category Arrmada doesn't own = %d, want 400", code)
	}
	// The TV category, but the sweep never found this one misfiled.
	if code := call(hash, `{"category":"`+download.CategoryTV+`"}`); code != http.StatusConflict {
		t.Errorf("a download that isn't misfiled = %d, want 409", code)
	}
	if len(posted) != 0 {
		t.Errorf("the client was asked to move something: %q", posted)
	}
}
