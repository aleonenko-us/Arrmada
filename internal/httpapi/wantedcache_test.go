package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Activity → Wanted is served from the cache while the pipeline hasn't moved, rebuilt
// once it has (a grab recorded or settled, a review, a stored search attempt) — but not
// more than every few seconds through a sweep's burst — and a kind filter always builds
// fresh.
func TestWantedCacheRebuildsWhenThePipelineMoves(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := st.DB()
	mv := movies.NewService(db, nil, nil, t.TempDir(), "", nil, log)
	dl := download.NewService(db, log)
	a := &api{deps: Deps{Store: st, Log: log, Movies: mv, Downloads: dl, Quality: quality.NewService(db),
		Settings: settings.NewService(db), Automation: automation.New(mv, nil, dl, nil, db, nil, log, "")}}
	var ver int64 = 1
	a.wanted.versionFn = func() int64 { return ver }
	get := func(query string) {
		t.Helper()
		rec := httptest.NewRecorder()
		a.handleWanted(rec, httptest.NewRequest(http.MethodGet, "/api/v1/wanted"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /wanted%s = %d %s", query, rec.Code, rec.Body.String())
		}
	}
	builds := func() int {
		a.wanted.mu.Lock()
		defer a.wanted.mu.Unlock()
		return a.wanted.builds
	}
	age := func(d time.Duration) {
		a.wanted.mu.Lock()
		a.wanted.at = time.Now().Add(-d)
		a.wanted.mu.Unlock()
	}

	get("")
	get("")
	if n := builds(); n != 1 {
		t.Fatalf("builds = %d after two idle reads, want 1", n)
	}
	// The pipeline moved, but the answer is younger than the rebuild floor: still cached.
	ver++
	get("")
	if n := builds(); n != 1 {
		t.Errorf("builds = %d inside the rebuild floor, want 1", n)
	}
	// Past the floor, a moved pipeline rebuilds.
	age(wantedRebuildEvery + time.Second)
	get("")
	if n := builds(); n != 2 {
		t.Errorf("builds = %d after the pipeline moved, want 2", n)
	}
	// Unmoved but older than a minute: rebuilt anyway (a library edit moves no counter).
	age(wantedCacheTTL + time.Second)
	get("")
	if n := builds(); n != 3 {
		t.Errorf("builds = %d after the minute, want 3", n)
	}
	// Search now resets a title's next check: the next read rebuilds.
	a.forgetWanted()
	get("")
	if n := builds(); n != 4 {
		t.Errorf("builds = %d after forgetWanted, want 4", n)
	}
	// A filtered read (the Movies Wanted view's kind) never touches the cache.
	get("?kind=movie")
	if n := builds(); n != 4 {
		t.Errorf("builds = %d after a filtered read, want 4", n)
	}
}
