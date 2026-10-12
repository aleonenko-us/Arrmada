package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
)

// BOOK-08: My shelf's "Your requests" says where each request has got to — a book the
// searches keep missing reads "Not found yet" with the next check, not "Searching".
func TestMyBooksRequestCarriesStageAndNote(t *testing.T) {
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Downloads = download.NewService(db, d.Log) // no clients: an empty queue
		d.Quality = quality.NewService(db)
		mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
		sr := series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, mv, sr, d.Books, nil, d.Quality, nil, "", d.Log)
	})
	ctx := context.Background()
	u, cookie := s.user(t, "reader@example.com", auth.RoleRequester)
	b, err := books.NewRepo(s.st.DB()).Create(ctx, books.Book{OLKey: "OL1W", Title: "Dune", Author: "Frank Herbert", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.st.DB().Exec(`UPDATE books SET last_search_at = ?, search_misses = 3 WHERE id = ?`,
		last.Format("2006-01-02 15:04:05"), b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB().Exec(`INSERT INTO requests (media_type, ol_key, title, author, status, requested_by, book_id)
		VALUES ('book', 'OL1W', 'Dune', 'Frank Herbert', 'approved', ?, ?)`, u.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	rec := s.do("GET", "/api/v1/me/books", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Requests []MyRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := last.Add(books.SearchWait(3)).Format(time.RFC3339)
	if len(got.Requests) != 1 {
		t.Fatalf("requests = %+v, want Dune", got.Requests)
	}
	if r := got.Requests[0]; r.Stage != requests.StageSearching || r.Note != "Not found yet" || r.NextCheckAt != want {
		t.Errorf("request = %+v, want searching / Not found yet / %s", r, want)
	}

	// The staff detail API carries the same search state.
	_, adminCookie := s.user(t, "admin@example.com", auth.RoleAdmin)
	rec = s.do("GET", "/api/v1/books/"+strconv.FormatInt(b.ID, 10), adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("book detail: HTTP %d: %s", rec.Code, rec.Body)
	}
	var detail books.Book
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SearchMisses != 3 || detail.LastSearchAt != last.Format(time.RFC3339) || detail.NextSearchAt != want {
		t.Errorf("detail search state = %q / %d / %q", detail.LastSearchAt, detail.SearchMisses, detail.NextSearchAt)
	}
}

// APP-19: the shelf's book sheet and series rows get the description, the series and the
// place in it apart, and the first three subjects; a request carries its id and the whole
// tracking (the Requests page's shape), so the card can say "Not found yet · next check".
func TestMyBooksCarriesSheetFields(t *testing.T) {
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Downloads = download.NewService(db, d.Log)
		d.Quality = quality.NewService(db)
		mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
		sr := series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, mv, sr, d.Books, nil, d.Quality, nil, "", d.Log)
	})
	ctx := context.Background()
	u, cookie := s.user(t, "reader@example.com", auth.RoleRequester)
	repo := books.NewRepo(s.st.DB())
	b, err := repo.Create(ctx, books.Book{OLKey: "OL9W", Title: "Children of Dune", Author: "Frank Herbert"})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("Leto and Ghanima inherit an empire. ", 80) // ~2,900 characters
	if err := repo.UpdateMeta(ctx, b.ID, long, "", []string{"Science fiction", "Dune", "Politics", "Desert"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSeries(ctx, b.ID, "Dune Chronicles", 3); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetEdition(ctx, b.ID, books.KindEbook, filepath.Join(root, "children.epub"), "EPUB", 1, 1); err != nil {
		t.Fatal(err)
	}
	wanted, err := repo.Create(ctx, books.Book{OLKey: "OL10W", Title: "God Emperor of Dune", Author: "Frank Herbert", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.st.DB().Exec(`INSERT INTO requests (media_type, ol_key, title, author, status, requested_by, book_id)
		VALUES ('book', 'OL10W', 'God Emperor of Dune', 'Frank Herbert', 'approved', ?, ?)`, u.ID, wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	reqID, _ := res.LastInsertId()

	rec := s.do("GET", "/api/v1/me/books", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Books      []MyBook    `json:"books"`
		Requests   []MyRequest `json:"requests"`
		QueueKnown bool        `json:"queue_known"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Books) != 1 {
		t.Fatalf("books = %+v", got.Books)
	}
	mb := got.Books[0]
	if mb.SeriesName != "Dune Chronicles" || mb.SeriesPosition != 3 || mb.Series != "Dune Chronicles #3" {
		t.Errorf("series = %q / %v / %q", mb.SeriesName, mb.SeriesPosition, mb.Series)
	}
	if len(mb.Subjects) != 3 || mb.Subjects[0] != "Science fiction" {
		t.Errorf("subjects = %v, want the first three", mb.Subjects)
	}
	if n := len([]rune(mb.Description)); n == 0 || n > shelfDescriptionMax+1 || !strings.HasPrefix(mb.Description, "Leto and Ghanima") || !strings.HasSuffix(mb.Description, "…") {
		t.Errorf("description of %d runes: %q", n, mb.Description)
	}
	if mb.Ebook == nil || !mb.Ebook.Kindle {
		t.Errorf("an EPUB must be offered for Kindle: %+v", mb.Ebook)
	}
	if !got.QueueKnown || len(got.Requests) != 1 {
		t.Fatalf("queue_known %v, requests %+v", got.QueueKnown, got.Requests)
	}
	r := got.Requests[0]
	if r.ID != reqID || r.Tracking == nil || r.Tracking.Stage != requests.StageSearching {
		t.Errorf("request = %+v (tracking %+v), want id %d and a searching stage", r, r.Tracking, reqID)
	}
}

func TestShelfDescription(t *testing.T) {
	if got := shelfDescription("  Short.  "); got != "Short." {
		t.Errorf("short = %q", got)
	}
	got := shelfDescription(strings.Repeat("word ", 1000))
	if r := []rune(got); len(r) > shelfDescriptionMax+1 || !strings.HasSuffix(got, "word…") {
		t.Errorf("long cut = %q (%d runes)", got, len(r))
	}
}
