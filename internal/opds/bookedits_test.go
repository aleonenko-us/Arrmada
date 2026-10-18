package opds

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
)

// Details edited by hand on the book page are what e-readers see: the title, author and
// series in the entry, search and the Series feed, the language, publisher and ISBN as
// Dublin Core, and a newer <updated> so a reader's cached catalogue takes them.
func TestEntriesShowHandEdits(t *testing.T) {
	x := newHarness(t)
	b := x.book("Harry Potter and the Philosopher's Stone", "J.K. Rowling", "hp.epub", nil)
	before := x.feed("/opds/new", "acquisition").Entries[0].Updated
	time.Sleep(1100 * time.Millisecond) // <updated> has whole seconds

	raw := func(v any) json.RawMessage { out, _ := json.Marshal(v); return out }
	if _, err := x.bs.EditDetails(x.ctx, b.ID, books.EditRequest{Set: map[string]json.RawMessage{
		books.FieldTitle: raw("Harry Potter à l'école des sorciers"), books.FieldAuthor: raw("Joanne Rowling"),
		books.FieldSeriesName: raw("Harry Potter (FR)"), books.FieldSeriesPosition: raw(1),
		books.FieldDescription: raw("Édition française."), books.FieldLanguage: raw("French"),
		books.FieldPublisher: raw("Gallimard"), books.FieldISBN: raw("978-0-590-35340-3"),
	}}); err != nil {
		t.Fatal(err)
	}

	f := x.feed("/opds/new", "acquisition")
	e := f.Entries[0]
	if e.Title != "Harry Potter à l'école des sorciers" || strings.Join(e.Authors, "|") != "Joanne Rowling" {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.Summary, "Series: Harry Potter (FR) #1") || !strings.Contains(e.Summary, "Édition française.") {
		t.Fatalf("summary = %q", e.Summary)
	}
	if e.Updated <= before {
		t.Fatalf("<updated> %s didn't move past %s", e.Updated, before)
	}
	body := x.do("/opds/new", "reader", "reader-pass-1").Body.String()
	for _, want := range []string{"<dc:language>fr</dc:language>", "<dc:publisher>Gallimard</dc:publisher>", "<dc:identifier>urn:isbn:9780590353403</dc:identifier>"} {
		if !strings.Contains(body, want) {
			t.Errorf("feed lacks %s", want)
		}
	}

	if got := x.feed("/opds/search?q="+url.QueryEscape("ecole sorciers"), "acquisition").titles(); len(got) != 1 {
		t.Fatalf("search by the edited title = %v", got)
	}
	if got := strings.Join(x.feed("/opds/series", "navigation").titles(), "|"); got != "Harry Potter (FR)" {
		t.Fatalf("series = %s", got)
	}
}
