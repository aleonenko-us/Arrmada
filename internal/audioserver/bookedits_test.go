package audioserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
)

// Details edited by hand on the book page are what listening apps see — title, author,
// series and position, narrator, and the subtitle, publisher, ISBN and language only an
// edit fills — and the edit moves the item's updatedAt so apps refetch it.
func TestItemShowsHandEdits(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	key := itemKeyFor(h.book.ID, 0)
	before, ok := h.json("GET", "/api/items/"+key, nil)["updatedAt"].(float64)
	if !ok {
		t.Fatal("updatedAt isn't a number")
	}
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	if _, err := h.srv.books.EditDetails(ctx, h.book.ID, books.EditRequest{Set: map[string]json.RawMessage{
		books.FieldTitle: raw("Carl's Doomsday Scenario"), books.FieldAuthor: raw("Matt Dinniman"),
		books.FieldSeriesName: raw("Dungeon Crawler Carl"), books.FieldSeriesPosition: raw(2.5),
		books.FieldNarrator: raw("Jeff Hays"), books.FieldSubtitle: raw("A LitRPG Adventure"),
		books.FieldPublisher: raw("Soundbooth Theater"), books.FieldISBN: raw("978-0-590-35340-3"), books.FieldLanguage: raw("fr"),
	}}); err != nil {
		t.Fatal(err)
	}
	h.srv.Invalidate() // what the edit route does; without it the catalogue is a little stale

	item := h.json("GET", "/api/items/"+key+"?expanded=1", nil)
	if after := item["updatedAt"].(float64); after <= before {
		t.Fatalf("updatedAt %v → %v after an edit", before, after)
	}
	md := obj1(t, obj1(t, item["media"])["metadata"])
	for k, want := range map[string]string{
		"title": "Carl's Doomsday Scenario", "subtitle": "A LitRPG Adventure", "publisher": "Soundbooth Theater",
		"isbn": "9780590353403", "language": "French",
	} {
		if got, _ := md[k].(string); got != want {
			t.Errorf("metadata.%s = %v, want %q", k, md[k], want)
		}
	}
	if a := obj1(t, list1(t, md["authors"])[0])["name"]; a != "Matt Dinniman" {
		t.Errorf("author = %v", a)
	}
	if n := list1(t, md["narrators"]); len(n) != 1 || n[0] != "Jeff Hays" {
		t.Errorf("narrators = %v", n)
	}
	s := obj1(t, list1(t, md["series"])[0])
	if s["name"] != "Dungeon Crawler Carl" || s["sequence"] != "2.5" {
		t.Errorf("series = %v", s)
	}

	// The list says the same.
	items := h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0", nil)
	lmd := obj1(t, obj1(t, obj1(t, list1(t, items["results"])[0])["media"])["metadata"])
	if lmd["title"] != "Carl's Doomsday Scenario" || lmd["seriesName"] != "Dungeon Crawler Carl #2.5" || lmd["narratorName"] != "Jeff Hays" {
		t.Errorf("list metadata = %v", lmd)
	}
}
