package requests

import (
	"encoding/json"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// A book request reads as the library shows the book once its title is edited by hand on
// the book page; the request keeps the title it was made under.
func TestBookRequestShowsEditedTitle(t *testing.T) {
	s, repo, _, ctx := bookLinkFixture(t, catalogue{byKey: map[string]metadata.BookResult{}})
	b, err := repo.Create(ctx, books.Book{OLKey: "OL1W", Title: "Harry Potter and the Philosopher's Stone", Author: "J.K. Rowling"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := s.repo.Create(ctx, Request{MediaType: "book", OLKey: "OL1W", Title: b.Title, Author: b.Author,
		Status: StatusApproved, RequestedBy: 7, BookID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := s.List(ctx, ListFilter{})
	if err != nil || len(list) != 1 || list[0].DisplayTitle != "" {
		t.Fatalf("before an edit: %v %+v", err, list)
	}
	raw, _ := json.Marshal("Harry Potter and the Sorcerer's Stone")
	if _, err := s.books.(*books.Service).EditDetails(ctx, b.ID, books.EditRequest{Set: map[string]json.RawMessage{books.FieldTitle: raw}}); err != nil {
		t.Fatal(err)
	}
	list, _, err = s.List(ctx, ListFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list[0].DisplayTitle != "Harry Potter and the Sorcerer's Stone" || list[0].Title != "Harry Potter and the Philosopher's Stone" {
		t.Fatalf("request reads %q (made as %q)", list[0].DisplayTitle, list[0].Title)
	}
	if got := s.withShownTitle(ctx, req); got.Title != "Harry Potter and the Sorcerer's Stone" {
		t.Fatalf("a notice would name it %q", got.Title)
	}
}
