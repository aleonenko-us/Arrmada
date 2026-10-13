package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// slugCatalogue answers BookSlug with a fixed slug or error.
type slugCatalogue struct {
	metadata.BookProvider
	slug string
	err  error
}

func (slugCatalogue) Available() bool { return true }

func (c slugCatalogue) BookSlug(context.Context, string) (string, error) { return c.slug, c.err }

// COPY-15: a Hardcover book's badge links to its hardcover.app page when the slug is
// known, and to a search when the lookup fails — the page loads either way.
func TestBookPageLinksHardcoverSlug(t *testing.T) {
	for _, tc := range []struct {
		name string
		cat  slugCatalogue
		want string
	}{
		{"slug", slugCatalogue{slug: "dune"}, "https://hardcover.app/books/dune"},
		{"lookup failed", slugCatalogue{err: errors.New("hardcover: field 'slug' not found")}, "https://hardcover.app/search?q=Dune+Frank+Herbert"},
		{"budget spent", slugCatalogue{err: metadata.ErrHardcoverBudget}, "https://hardcover.app/search?q=Dune+Frank+Herbert"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRouteServer(t, func(d *Deps) {
				d.Books = books.NewService(d.Store.DB(), tc.cat, d.Log)
			})
			_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
			added, _ := s.deps.Books.AddWorks(context.Background(), []metadata.BookResult{{Key: "hc:1245", Title: "Dune", Author: "Frank Herbert"}}, "", false)
			if len(added) != 1 {
				t.Fatalf("add: %v", added)
			}
			rec := s.do("GET", fmt.Sprintf("/api/v1/books/%d", added[0].ID), mgr)
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
			}
			var b books.Book
			if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
				t.Fatal(err)
			}
			if b.Catalogue == nil || b.Catalogue.Name != "Hardcover" || b.Catalogue.URL != tc.want {
				t.Errorf("catalogue = %+v, want %s", b.Catalogue, tc.want)
			}
		})
	}
}
