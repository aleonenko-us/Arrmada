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

// bookQueryTorznab answers a search only when its text names one of the titles it holds, and
// records every search's text, so a test can see which names a book was searched by.
type bookQueryTorznab struct {
	mu      sync.Mutex
	queries []string
	offers  map[string]string // a word the query must contain → the release it returns
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
		for word, title := range f.offers {
			if strings.Contains(strings.ToLower(q), strings.ToLower(word)) {
				fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><size>%d</size><torznab:attr name="seeders" value="25"/></item>`,
					html.EscapeString(title), html.EscapeString(magnetFor(title)), int64(2<<20))
			}
		}
		b.WriteString(`</channel></rss>`)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, b.String())
	})
}

// A title edited by hand is searched first; when trackers only know the catalogue's
// title, the search goes on to it and the release — named the catalogue's way — is
// still recognised as this book and grabbed.
func TestBookSearchUsesEditedThenCatalogueNames(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	qb := &fakeQbit{}
	tz := &bookQueryTorznab{offers: map[string]string{"philosopher": "J.K. Rowling - Harry Potter and the Philosopher's Stone EPUB"}}
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

	added, _ := bk.AddWorks(ctx, []metadata.BookResult{{Key: "OL1W", Title: "Harry Potter and the Philosopher's Stone", Author: "J.K. Rowling"}}, "", true)
	if len(added) != 1 {
		t.Fatal("book not added")
	}
	raw, _ := json.Marshal("Harry Potter and the Sorcerer's Stone")
	if _, err := bk.EditDetails(ctx, added[0].ID, books.EditRequest{Set: map[string]json.RawMessage{books.FieldTitle: raw}}); err != nil {
		t.Fatal(err)
	}

	out, err := c.SearchBookNow(ctx, added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Grabbed == 0 {
		t.Fatalf("outcome = %+v, queries %q", out, tz.queries)
	}
	tz.mu.Lock()
	queries := append([]string(nil), tz.queries...)
	tz.mu.Unlock()
	if len(queries) < 2 || !strings.Contains(queries[0], "Sorcerer") {
		t.Fatalf("queries = %q, want the edited title first", queries)
	}
	last := queries[len(queries)-1]
	if !strings.Contains(last, "Philosopher") {
		t.Fatalf("queries = %q, want the catalogue's title tried after", queries)
	}
}
