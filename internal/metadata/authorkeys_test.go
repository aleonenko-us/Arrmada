package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A Hardcover book's author key is the writer's id — the same contributor whose name is
// the author — never the illustrator listed first.
func TestHardcoverAuthorKeyIsTheWriters(t *testing.T) {
	var b hcBook
	if err := json.Unmarshal([]byte(`{"id":1,"title":"Empire of the Vampire","contributions":[
		{"contribution":"Illustrator","author":{"id":2,"name":"Bon Orthwick"}},
		{"contribution":"","author":{"id":3,"name":"Jay Kristoff"}}]}`), &b); err != nil {
		t.Fatal(err)
	}
	if r := b.result(); r.Author != "Jay Kristoff" || r.AuthorKey != "hc:a:3" {
		t.Errorf("book author = %q %q, want Jay Kristoff hc:a:3", r.Author, r.AuthorKey)
	}

	// Search hits: ids as numbers or strings; roles pick the writer.
	for _, raw := range []string{
		`{"id":1,"title":"Empire of the Vampire","author_names":["Bon Orthwick","Jay Kristoff"],
		  "contributions":[{"contribution":"Illustrator","author":{"id":2,"name":"Bon Orthwick"}},{"contribution":null,"author":{"id":3,"name":"Jay Kristoff"}}]}`,
		`{"id":1,"title":"Empire of the Vampire","contributions":[{"contribution":"Illustrator","author":{"id":"2","name":"Bon Orthwick"}},{"author":{"id":"3","name":"Jay Kristoff"}}]}`,
	} {
		var d hcSearchDoc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		if r, _ := d.bookResult(); r.Author != "Jay Kristoff" || r.AuthorKey != "hc:a:3" {
			t.Errorf("search author = %q %q, want Jay Kristoff hc:a:3", r.Author, r.AuthorKey)
		}
	}

	// No ids, no roles, or an index shape this code doesn't know: no key, the name as before.
	for _, raw := range []string{
		`{"id":1,"title":"Dune","author_names":["Frank Herbert"],"contributions":[{"author":{"name":"Frank Herbert"}}]}`,
		`{"id":1,"title":"Dune","author_names":["Frank Herbert"],"contributions":"weird"}`,
		`{"id":1,"title":"Dune","author_names":["Frank Herbert"]}`,
	} {
		var d hcSearchDoc
		_ = json.Unmarshal([]byte(raw), &d)
		if r, _ := d.bookResult(); r.Author != "Frank Herbert" || r.AuthorKey != "" {
			t.Errorf("%s: author = %q key %q, want Frank Herbert and no key", raw, r.Author, r.AuthorKey)
		}
	}
}

// Open Library carries the first author's key on search docs (parallel to author_name)
// and on works (the author it looks the name up by). Keys in any other shape are dropped.
func TestOpenLibraryAuthorKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.json":
			_, _ = w.Write([]byte(`{"docs":[
				{"key":"/works/OL1W","title":"Dune","author_name":["Frank Herbert"],"author_key":["OL79034A"]},
				{"key":"/works/OL2W","title":"Odd","author_name":["Someone"],"author_key":["not-a-key"]},
				{"key":"/works/OL3W","title":"Nameless","author_key":["OL5A"]}]}`))
		case "/works/OL1W.json":
			_, _ = w.Write([]byte(`{"title":"Dune","authors":[{"author":{"key":"/authors/OL79034A"}}]}`))
		case "/authors/OL79034A.json":
			_, _ = w.Write([]byte(`{"name":"Frank Herbert"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ol := &OpenLibrary{http: &http.Client{Timeout: 5 * time.Second}, base: srv.URL}
	ctx := context.Background()

	res, err := ol.SearchBooks(ctx, "dune")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"OL1W": "OL79034A", "OL2W": "", "OL3W": ""}
	for _, r := range res {
		if r.AuthorKey != want[r.Key] {
			t.Errorf("%s: author key %q, want %q", r.Key, r.AuthorKey, want[r.Key])
		}
	}
	d, err := ol.GetBook(ctx, "OL1W")
	if err != nil {
		t.Fatal(err)
	}
	if d.Author != "Frank Herbert" || d.AuthorKey != "OL79034A" {
		t.Errorf("work author = %q %q, want Frank Herbert OL79034A", d.Author, d.AuthorKey)
	}
}
