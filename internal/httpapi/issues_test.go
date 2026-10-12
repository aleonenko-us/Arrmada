package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/issues"
)

// oneMovieLibrary is a library holding one movie with a file: TMDB 603, library id 11.
type oneMovieLibrary struct{}

func (oneMovieLibrary) Find(_ context.Context, media string, tmdbID, _, _ int) (issues.Item, error) {
	if media != issues.MediaMovie || tmdbID != 603 {
		return issues.Item{}, issues.ErrNotInLibrary
	}
	return issues.Item{LibraryID: 11, TMDBID: 603, Title: "The Matrix", Year: 1999, Path: "/m/The Matrix.mkv", Size: 4000}, nil
}

func (l oneMovieLibrary) Current(ctx context.Context, t issues.Target) (issues.Item, error) {
	return l.Find(ctx, t.MediaType, 603, 0, 0)
}

func (oneMovieLibrary) EpisodesWithFiles(context.Context, int) (string, []issues.SeasonFiles, error) {
	return "", nil, issues.ErrNotInLibrary
}

// A requester reports from the title's sheet and sees only what they reported, never
// another reporter's name or note; closing an issue is staff-only.
func TestIssuesRequesterSeesOnlyOwn(t *testing.T) {
	var svc *issues.Service
	s := newRouteServer(t, func(d *Deps) {
		svc = issues.NewService(d.Store.DB(), oneMovieLibrary{}, nil)
		d.Issues = svc
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, alice := s.user(t, "alice@example.com", auth.RoleRequester)
	_, bob := s.user(t, "bob@example.com", auth.RoleRequester)
	_, carol := s.user(t, "carol@example.com", auth.RoleRequester)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleReadonly)

	report := `{"media_type":"movie","tmdb_id":603,"category":"audio","note":"alice-secret-note"}`
	if rec := s.doJSON("POST", "/api/v1/issues", viewer, report); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only report: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doJSON("POST", "/api/v1/issues", alice, `{"media_type":"movie","tmdb_id":999,"category":"audio"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("report of a title not in the library: HTTP %d, want 404", rec.Code)
	}
	rec := s.doJSON("POST", "/api/v1/issues", alice, report)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice's report: HTTP %d: %s", rec.Code, rec.Body)
	}
	rec = s.doJSON("POST", "/api/v1/issues", bob, `{"media_type":"movie","tmdb_id":603,"category":"audio","note":"bob-note"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob's report: HTTP %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		Issue  issues.Issue `json:"issue"`
		Joined bool         `json:"joined"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Joined || strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("bob's answer: joined=%v body=%s", created.Joined, rec.Body)
	}

	// Bob's list: the one issue, his own note, nothing of alice's.
	rec = s.do("GET", "/api/v1/issues", bob)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "alice") || !strings.Contains(rec.Body.String(), "bob-note") {
		t.Fatalf("bob's list: HTTP %d: %s", rec.Code, rec.Body)
	}
	// Carol reported nothing and sees nothing, even asking by id.
	rec = s.do("GET", "/api/v1/issues?id="+itoa64(created.Issue.ID), carol)
	var list struct {
		Issues []issues.Issue `json:"issues"`
		Total  int            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || list.Total != 0 || len(list.Issues) != 0 {
		t.Fatalf("carol's list: %s", rec.Body)
	}
	// Staff see who reported it and both notes.
	rec = s.do("GET", "/api/v1/issues?status=open", mgr)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "alice-secret-note") || !strings.Contains(body, "bob-note") {
		t.Fatalf("staff list: HTTP %d: %s", rec.Code, body)
	}

	// Fixes are staff-only; one whose service isn't wired says so and leaves it open.
	fix := "/api/v1/issues/" + itoa64(created.Issue.ID) + "/fix"
	if rec := s.doJSON("POST", fix, alice, `{"action":"replace"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("requester fix: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doJSON("POST", fix, mgr, `{"action":"subtitles"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired fix: HTTP %d, want 503: %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", fix, mgr, `{"action":"convert"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown fix: HTTP %d, want 400: %s", rec.Code, rec.Body)
	}

	path := "/api/v1/issues/" + itoa64(created.Issue.ID) + "/resolve"
	if rec := s.doJSON("POST", path, alice, `{"note":"done"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("requester resolve: HTTP %d, want 403", rec.Code)
	}
	if rec := s.doJSON("POST", path, mgr, `{"note":"Swapped the copy."}`); rec.Code != http.StatusOK {
		t.Fatalf("staff resolve: HTTP %d: %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("POST", path, mgr, `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("resolving twice: HTTP %d, want 409", rec.Code)
	}
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
