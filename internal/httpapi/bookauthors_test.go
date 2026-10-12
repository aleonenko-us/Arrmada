package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// authorWorksCatalogue lists two books for any author.
type authorWorksCatalogue struct{ metadata.BookProvider }

func (authorWorksCatalogue) Available() bool { return true }
func (authorWorksCatalogue) AuthorWorks(context.Context, string, int) ([]metadata.BookResult, error) {
	return []metadata.BookResult{
		{Key: "hc:1", Title: "The Final Empire", Author: "Brandon Sanderson", AuthorKey: "hc:a:5"},
		{Key: "hc:2", Title: "The Way of Kings", Author: "Brandon Sanderson", AuthorKey: "hc:a:5"},
	}, nil
}

// 'Add author' creates a followed author (monitored, new books on) and links every book
// it adds; the book-authors API reads and updates it, manager only.
func TestBookAuthorsAPI(t *testing.T) {
	fj := newFakeJobs(false) // records the batch search without running it
	s := newRouteServer(t, func(d *Deps) {
		d.Books = books.NewService(d.Store.DB(), authorWorksCatalogue{}, d.Log)
		d.Jobs = fj
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)

	rec := s.doBody("POST", "/api/v1/books/author", `{"author_key":"hc:a:5","name":"Brandon Sanderson","quality_profile":"book-ebook","monitored":true,"search_on_add":true}`, mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("add author: HTTP %d %s", rec.Code, rec.Body)
	}
	var added struct {
		Added    int   `json:"added"`
		AuthorID int64 `json:"author_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.Added != 2 || added.AuthorID == 0 {
		t.Fatalf("add author = %s, want 2 added and an author id", rec.Body)
	}
	path := fmt.Sprintf("/api/v1/book-authors/%d", added.AuthorID)

	get := func() books.Author {
		t.Helper()
		rec := s.do("GET", path, mgr)
		if rec.Code != http.StatusOK {
			t.Fatalf("get author: HTTP %d %s", rec.Code, rec.Body)
		}
		var a books.Author
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := get()
	if a.Name != "Brandon Sanderson" || a.CatalogueKey != "hc:a:5" || !a.Monitored || !a.MonitorNew ||
		a.QualityProfile != "book-ebook" || a.Books != 2 || a.Wanted != 2 {
		t.Errorf("author = %+v, want a followed Brandon Sanderson (hc:a:5) with 2 wanted books", a)
	}
	// The list it added from is the release check's record of the author: [BOOK-21]
	var seen int
	if err := s.deps.Store.DB().QueryRow(`SELECT COUNT(*) FROM author_works_seen WHERE author_id = ?`, added.AuthorID).Scan(&seen); err != nil || seen == 0 || a.WorksCheckedAt == "" {
		t.Errorf("add author recorded %d seen works (err %v), checked at %q", seen, err, a.WorksCheckedAt)
	}
	list, err := s.deps.Books.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.AuthorID != added.AuthorID {
			t.Errorf("%q linked to %d, want %d", b.Title, b.AuthorID, added.AuthorID)
		}
	}

	for _, q := range []string{"/api/v1/book-authors", "/api/v1/book-authors?name=Sanderson,%20Brandon"} {
		rec := s.do("GET", q, mgr)
		var out struct {
			Authors []books.Author `json:"authors"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusOK || len(out.Authors) != 1 || out.Authors[0].ID != added.AuthorID {
			t.Errorf("%s: HTTP %d %s, want just author %d", q, rec.Code, rec.Body, added.AuthorID)
		}
	}
	if rec := s.do("GET", "/api/v1/book-authors?name=Someone%20Else", mgr); rec.Code != http.StatusOK {
		t.Errorf("unknown name: HTTP %d", rec.Code)
	} else {
		var out struct {
			Authors []books.Author `json:"authors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Authors == nil || len(out.Authors) != 0 {
			t.Errorf("unknown name: %s, want an empty list", rec.Body)
		}
	}

	// Partial updates keep what they leave out, and persist.
	if rec := s.doBody("PUT", path, `{"monitor_new":false}`, mgr); rec.Code != http.StatusOK {
		t.Fatalf("put: HTTP %d %s", rec.Code, rec.Body)
	}
	if a := get(); !a.Monitored || a.MonitorNew || a.QualityProfile != "book-ebook" {
		t.Errorf("after turning new books off = %+v, want still monitored with its profile", a)
	}
	if rec := s.doBody("PUT", path, `{"catalogue_key":"not-a-key"}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("bad key: HTTP %d, want 400", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/book-authors/999", mgr); rec.Code != http.StatusNotFound {
		t.Errorf("unknown author: HTTP %d, want 404", rec.Code)
	}

	// Staff only.
	for _, req := range [][2]string{{"GET", "/api/v1/book-authors"}, {"GET", path}, {"PUT", path}} {
		if rec := s.doBody(req[0], req[1], `{"monitored":false}`, kid); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a requester: HTTP %d, want 403", req[0], req[1], rec.Code)
		}
	}
	if a := get(); !a.Monitored {
		t.Error("a requester's PUT changed the author")
	}
}
