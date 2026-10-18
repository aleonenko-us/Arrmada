package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// bookEditServer is a route server with books, the importer (default book naming) and
// jobs, holding one audiobook on disk at the default scheme's place.
func bookEditServer(t *testing.T) (*routeServer, *books.Service, books.Book, string, *jobs.Runner) {
	t.Helper()
	root := t.TempDir()
	var runner *jobs.Runner
	var bs *books.Service
	s := newRouteServer(t, func(d *Deps) {
		bs = books.NewService(d.Store.DB(), nil, d.Log)
		d.Books = bs
		imp := library.NewImporter("", d.Log)
		imp.SetRoots("", "", root, root)
		c := automation.New(nil, nil, nil, nil, d.Store.DB(), nil, d.Log, "")
		c.SetSeries(nil, imp)
		c.SetBooks(bs)
		d.Automation = c
		r, err := jobs.New(context.Background(), d.Store.DB(), d.Log, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner = r
		d.Jobs = r
	})
	t.Cleanup(func() { runner.Shutdown(time.Second) })
	ctx := context.Background()
	added, _ := bs.AddWorks(ctx, []metadata.BookResult{{Key: "hc:1", Title: "Golden Son", Author: "Pierce Brown"}}, "", true)
	if len(added) != 1 {
		t.Fatal("book not added")
	}
	old := filepath.Join(root, "Pierce Brown", "Golden Son", "Golden Son.m4b")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("gs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bs.MarkImported(ctx, added[0].ID, books.KindAudiobook, old, "M4B", 2, 1); err != nil {
		t.Fatal(err)
	}
	b, _ := bs.Get(ctx, added[0].ID)
	return s, bs, b, root, runner
}

type editReply struct {
	Book            books.Book `json:"book"`
	Changed         []string   `json:"changed"`
	RenameAvailable bool       `json:"rename_available"`
}

// The edit route: staff only, refusals with a reason, the edit shown at once (⌘K too),
// and "files can be renamed" offered only when the edit moved where the naming puts them.
func TestEditBookDetailsRoute(t *testing.T) {
	s, bs, b, root, runner := bookEditServer(t)
	ctx := context.Background()
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, req := s.user(t, "req@example.com", auth.RoleRequester)
	path := "/api/v1/books/" + jsonInt(b.ID) + "/details"

	if rec := s.doJSON("PUT", path, req, `{"set":{"title":"X"}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("requester: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("PUT", path, nil, `{"set":{"title":"X"}}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("PUT", "/api/v1/books/9999/details", mgr, `{"set":{"title":"X"}}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing book: HTTP %d", rec.Code)
	}
	rec := s.doJSON("PUT", path, mgr, `{"set":{"isbn":"978-0-590-35340-4"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "check digit") {
		t.Fatalf("bad ISBN: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("PUT", path, mgr, `{"set":{"series_position":"one"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad series position: HTTP %d", rec.Code)
	}

	// The search index is warm with the catalogue's title before the edit.
	if rec := s.do("GET", "/api/v1/library/search?q=golden", mgr); rec.Code != http.StatusOK {
		t.Fatalf("search: HTTP %d", rec.Code)
	}

	// A publisher doesn't touch the naming: no rename offered.
	var got editReply
	rec = s.doJSON("PUT", path, mgr, `{"set":{"publisher":"Del Rey"}}`)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("publisher: HTTP %d %s", rec.Code, rec.Body)
	}
	if got.RenameAvailable || strings.Join(got.Changed, ",") != "publisher" || got.Book.Publisher != "Del Rey" {
		t.Fatalf("publisher edit: %+v", got)
	}
	if string(got.Book.Edits["publisher"]) != `""` {
		t.Fatalf("edited marker: %v", got.Book.Edits)
	}

	// A title does: the files can be renamed to match, and nothing moved by itself.
	rec = s.doJSON("PUT", path, mgr, `{"set":{"title":"Golden Son: Book II"}}`)
	got = editReply{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("title: HTTP %d %s", rec.Code, rec.Body)
	}
	if !got.RenameAvailable || got.Book.Title != "Golden Son: Book II" {
		t.Fatalf("title edit: %+v", got)
	}
	old := filepath.Join(root, "Pierce Brown", "Golden Son", "Golden Son.m4b")
	if fileBody(t, old) != "gs" {
		t.Fatal("an edit must never rename files")
	}
	// ⌘K finds it by the edited title at once, and still by the catalogue's.
	for _, q := range []string{"book ii", "golden son"} {
		rec := s.do("GET", "/api/v1/library/search?q="+url.QueryEscape(q), mgr)
		if !strings.Contains(rec.Body.String(), `"title":"Golden Son: Book II"`) {
			t.Errorf("search %q: %s", q, rec.Body)
		}
	}

	// The per-book preview lists just this book's editions, and applying it renames them.
	rec = s.do("GET", "/api/v1/books/organize?book_id="+jsonInt(b.ID), mgr)
	var p struct {
		Counts bookOrganizeCounts          `json:"counts"`
		Items  []automation.BookRenameItem `json:"items"`
		Token  string                      `json:"token"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil {
		t.Fatalf("preview: HTTP %d %s", rec.Code, rec.Body)
	}
	if p.Counts.Rename != 1 || len(p.Items) != 1 || p.Items[0].BookID != b.ID {
		t.Fatalf("per-book preview: %+v", p)
	}
	if rec := s.do("GET", "/api/v1/books/organize?book_id=abc", mgr); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad book_id: HTTP %d", rec.Code)
	}
	rec = s.doJSON("POST", "/api/v1/books/organize", mgr, `{"token":"`+p.Token+`","book_id":`+jsonInt(b.ID)+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: HTTP %d %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := runner.Wait(wctx, started.JobID); err != nil {
		t.Fatal(err)
	}
	if after, _ := bs.Get(ctx, b.ID); after.Audiobook.Path != p.Items[0].Proposed || !strings.Contains(after.Audiobook.Path, "Book II") {
		t.Fatalf("record at %q, want %q (the preview's)", after.Audiobook.Path, p.Items[0].Proposed)
	}

	// Reset all: back to the catalogue's details.
	rec = s.doJSON("PUT", path, mgr, `{"reset_all":true}`)
	got = editReply{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("reset all: HTTP %d %s", rec.Code, rec.Body)
	}
	if got.Book.Title != "Golden Son" || len(got.Book.Edits) != 0 || !got.RenameAvailable {
		t.Fatalf("reset all: %+v", got)
	}
}

// Change match takes "keep" (the default) or "clear" for the hand edits, nothing else.
func TestRematchEditsChoiceValidated(t *testing.T) {
	s, _, b, _, _ := bookEditServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	rec := s.doJSON("POST", "/api/v1/books/"+jsonInt(b.ID)+"/rematch", mgr, `{"ol_key":"OL1W","edits":"maybe"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "keep or clear") {
		t.Fatalf("bad edits choice: HTTP %d %s", rec.Code, rec.Body)
	}
}
