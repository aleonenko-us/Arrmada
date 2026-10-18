package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// bookQueryTorznab offers one release, only to searches whose text it accepts, and records
// every search's text, so a test can see which names a book was searched by.
type bookQueryTorznab struct {
	mu      sync.Mutex
	queries []string
	accept  func(q string) bool
	release string
}

func (f *bookQueryTorznab) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query().Get("q")
		if r.URL.Query().Get("t") != "caps" {
			f.queries = append(f.queries, q)
		}
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`)
		if f.accept(strings.ToLower(q)) {
			fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><size>%d</size><torznab:attr name="seeders" value="25"/></item>`,
				html.EscapeString(f.release), html.EscapeString(magnetFor(f.release)), int64(2<<20))
		}
		b.WriteString(`</channel></rss>`)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, b.String())
	})
}

func (f *bookQueryTorznab) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

// editSearchHarness is a coordinator with the fake indexer above, a fake qBittorrent and
// an ebook profile, holding one book added as catalogued and then edited to editedTitle.
func editSearchHarness(t *testing.T, tz *bookQueryTorznab, catalogued metadata.BookResult, editedTitle string) (*Coordinator, books.Book) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	qb := &fakeQbit{}
	qsrv := httptest.NewServer(qb.handler())
	t.Cleanup(qsrv.Close)
	tsrv := httptest.NewServer(tz.handler())
	t.Cleanup(tsrv.Close)
	ix := indexer.NewService(st.DB(), log, nil)
	if _, err := ix.Create(ctx, indexer.Indexer{Name: "Fake", Kind: indexer.KindTorznab, URL: tsrv.URL, Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	dl := download.NewService(st.DB(), log)
	if _, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: qsrv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	bk := books.NewService(st.DB(), nil, log)
	c := &Coordinator{db: st.DB(), log: log, bus: eventbus.New(log), indexers: ix, downloads: dl, books: bk,
		quality: quality.NewService(st.DB()), downloadsDir: t.TempDir()}
	createDefault(t, c.quality, quality.StoredProfile{MediaType: quality.MediaBook, Name: "Ebooks", FormatScores: map[string]int{"EPUB": 40}})
	added, _ := bk.AddWorks(ctx, []metadata.BookResult{catalogued}, "", true)
	if len(added) != 1 {
		t.Fatal("book not added")
	}
	raw, _ := json.Marshal(editedTitle)
	res, err := bk.EditDetails(ctx, added[0].ID, books.EditRequest{Set: map[string]json.RawMessage{books.FieldTitle: raw}})
	if err != nil {
		t.Fatal(err)
	}
	return c, res.Book
}

// A title edited into a variant of the catalogue's (a subtitle added) is searched first;
// when trackers only know the catalogue's title, the search goes on to it, and the
// release — named the catalogue's way — is still recognised as this book and grabbed.
func TestBookSearchUsesEditedThenCatalogueVariant(t *testing.T) {
	tz := &bookQueryTorznab{
		accept:  func(q string) bool { return strings.Contains(q, "way of kings") && !strings.Contains(q, "stormlight") },
		release: "Brandon Sanderson - The Way of Kings EPUB",
	}
	c, b := editSearchHarness(t, tz, metadata.BookResult{Key: "OL1W", Title: "The Way of Kings", Author: "Brandon Sanderson"},
		"The Way of Kings: The Stormlight Archive, Book 1")
	out, err := c.SearchBookNow(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	queries := tz.seen()
	if out.Grabbed == 0 {
		t.Fatalf("outcome = %+v, queries %q", out, queries)
	}
	if len(queries) < 2 || !strings.Contains(queries[0], "Stormlight") {
		t.Fatalf("queries = %q, want the edited title first", queries)
	}
	if last := queries[len(queries)-1]; strings.Contains(last, "Stormlight") || !strings.Contains(last, "Way of Kings") {
		t.Fatalf("queries = %q, want the catalogue's title tried after", queries)
	}
}

// A title edited into another title ("Dune" → "Dune Messiah") was a correction: the
// catalogue's old title isn't searched, so the other book's releases are never found for
// this one, let alone grabbed.
func TestBookSearchSkipsCorrectedCatalogueTitle(t *testing.T) {
	tz := &bookQueryTorznab{
		accept:  func(q string) bool { return strings.Contains(q, "dune") && !strings.Contains(q, "messiah") },
		release: "Frank Herbert - Dune EPUB",
	}
	c, b := editSearchHarness(t, tz, metadata.BookResult{Key: "OL2W", Title: "Dune", Author: "Frank Herbert"}, "Dune Messiah")
	out, err := c.SearchBookNow(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	queries := tz.seen()
	if out.Grabbed != 0 {
		t.Fatalf("grabbed the other book: %+v", out)
	}
	if len(queries) == 0 {
		t.Fatal("nothing was searched")
	}
	for _, q := range queries {
		if !strings.Contains(q, "Messiah") {
			t.Fatalf("searched the corrected title: %q", queries)
		}
	}
}
