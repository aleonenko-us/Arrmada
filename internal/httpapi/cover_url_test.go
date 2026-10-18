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
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
)

// coverCatalogue answers GetBook with a fixed cover.
type coverCatalogue struct {
	metadata.BookProvider
	cover string
}

func (coverCatalogue) Available() bool { return true }

func (c coverCatalogue) GetBook(_ context.Context, key string) (*metadata.BookDetails, error) {
	return &metadata.BookDetails{BookResult: metadata.BookResult{Key: key, Title: "Dune", CoverURL: c.cover}}, nil
}

// P1/N4: a book request's cover is the catalogue's, whatever the requester sent — their
// own URL would be fetched by Arrmada and shown to staff.
func TestBookRequestCoverComesFromTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name string
		cat  metadata.BookProvider
		want string
	}{
		{"catalogue cover", coverCatalogue{cover: "https://covers.openlibrary.org/b/id/1-L.jpg"}, "https://covers.openlibrary.org/b/id/1-L.jpg"},
		{"no catalogue", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s := newRouteServer(t, func(d *Deps) {
				db := d.Store.DB()
				d.Books = books.NewService(db, tc.cat, d.Log)
				mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
				sr := series.NewService(db, nil, root, d.Log)
				d.Requests = requests.NewService(db, mv, sr, d.Books, nil, nil, nil, "", d.Log)
			})
			_, cookie := s.user(t, "reader@example.com", auth.RoleRequester)
			rec := s.doJSON("POST", "/api/v1/requests", cookie,
				`{"media_type":"book","ol_key":"OL1W","title":"Dune","author":"Frank Herbert","poster_url":"http://169.254.169.254/latest/meta-data/evil.svg"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
			}
			var got struct {
				Request requests.Request `json:"request"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got.Request.PosterURL != tc.want {
				t.Fatalf("poster = %q, want %q", got.Request.PosterURL, tc.want)
			}
		})
	}
}

// W1: a cover a manager sets must be a web address; a changed one in a metadata edit too,
// while the one already set (an upload's own path) can come back unchanged.
func TestManagerCoverURLsMustBeWebAddresses(t *testing.T) {
	var bs *books.Service
	s := newRouteServer(t, func(d *Deps) {
		bs = books.NewService(d.Store.DB(), nil, d.Log)
		d.Books = bs
	})
	ctx := context.Background()
	b, err := books.NewRepo(s.st.DB()).Create(ctx, books.Book{OLKey: "OL1W", Title: "Dune", Author: "Frank Herbert", CoverURL: "/api/v1/books/1/cover-image?v=1"})
	if err != nil {
		t.Fatal(err)
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	path := fmt.Sprintf("/api/v1/books/%d", b.ID)

	for _, u := range []string{"javascript:alert(1)", "file:///etc/passwd", "http://", "http://user:pw@example.com/x.jpg", "/elsewhere.svg"} {
		if rec := s.doJSON("PUT", path+"/cover", mgr, `{"url":"`+u+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT cover %q: HTTP %d, want 400", u, rec.Code)
		}
		if rec := s.doJSON("PUT", path+"/metadata", mgr, `{"title":"Dune","author":"Frank Herbert","cover_url":"`+u+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("metadata cover %q: HTTP %d, want 400", u, rec.Code)
		}
	}
	if rec := s.doJSON("PUT", path+"/metadata", mgr, `{"title":"Dune","author":"Frank Herbert","cover_url":"/api/v1/books/1/cover-image?v=1"}`); rec.Code != http.StatusOK {
		t.Errorf("unchanged upload path: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("PUT", path+"/cover", mgr, `{"url":"https://covers.openlibrary.org/b/id/1-L.jpg"}`); rec.Code != http.StatusOK {
		t.Errorf("web cover: HTTP %d %s", rec.Code, rec.Body)
	}
}
