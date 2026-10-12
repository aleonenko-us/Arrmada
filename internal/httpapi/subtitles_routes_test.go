package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// Every Subtitles route is for managers: requesters have no Subtitles page, and the reads
// list every file path in the library.
func TestSubtitleRoutesRequireManager(t *testing.T) {
	rt := testRouter(t, true)
	n := 0
	for _, spec := range rt.specs {
		if !strings.HasPrefix(spec.Pattern, "/api/v1/subtitles/") {
			continue
		}
		n++
		if spec.Scope != scopeStaff && spec.Scope != scopeAdmin {
			t.Errorf("%s is open to %v, want managers only", spec, spec.Scope)
		}
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, asRole(httptest.NewRequest(spec.Method, samplePath(spec.Pattern), nil), auth.RoleRequester))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as a requester: HTTP %d, want 403", spec, rec.Code)
		}
	}
	if n == 0 {
		t.Fatal("no subtitles routes found")
	}
}

// The legacy endpoints are gone: no API route claims them, so they fall to the SPA
// catch-all, which answers any unknown /api/ path with a JSON 404 (spa()).
func TestLegacySubtitleRoutesRemoved(t *testing.T) {
	rt := testRouter(t, true)
	for _, path := range []string{
		"GET /api/v1/subtitles/movies", "GET /api/v1/subtitles/series",
		"POST /api/v1/subtitles/movies/1/search", "POST /api/v1/subtitles/series/1/search",
	} {
		method, p, _ := strings.Cut(path, " ")
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, asRole(httptest.NewRequest(method, p, nil), auth.RoleAdmin))
		if route := rec.Header().Get("X-Route"); strings.Contains(route, "/api/") {
			t.Errorf("%s reached %q; want no API route", path, route)
		}
	}
	a := &api{deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	rec := httptest.NewRecorder()
	a.spa(http.NotFoundHandler())(rec, httptest.NewRequest("GET", "/api/v1/subtitles/movies", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unclaimed /api/ path: HTTP %d, want 404", rec.Code)
	}
}

// The flat TV list (one probed row per episode) is refused before anything is read: the
// handler here has no Subtitles service at all, so reaching it would panic.
func TestLibraryFlatTVRejected(t *testing.T) {
	a := &api{deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	rec := httptest.NewRecorder()
	a.handleSubtitleLibrary(rec, httptest.NewRequest("GET", "/api/v1/subtitles/library?media=tv", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "group=series") {
		t.Errorf("HTTP %d %s, want 400 naming group=series", rec.Code, rec.Body.String())
	}
}
