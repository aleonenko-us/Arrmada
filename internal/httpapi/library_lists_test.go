package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The library lists carry what the library pages show, not each title's long text: a
// show's overview and cast and a book's description were most of the bytes (1.25 MB for
// 400 shows, 2.5 MB for 1,500 books). The detail endpoints keep them.
func TestLibraryListsLeaveOutLongText(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) {
		d.Series = series.NewService(d.Store.DB(), oneShowMeta{}, t.TempDir(), d.Log)
		d.Books = books.NewService(d.Store.DB(), authorWorksCatalogue{}, d.Log)
		d.Quality = quality.NewService(d.Store.DB())
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	db := s.deps.Store.DB()
	for _, q := range []string{
		`INSERT INTO series (id, tmdb_id, title, overview, extra_json, network, monitored) VALUES (1, 77, 'Harbour Lights', 'A LONG OVERVIEW', '{"cast":[{"name":"SOME ACTOR"}],"genres":["Drama"]}', 'HBO', 1)`,
		`INSERT INTO series_aliases (series_id, title, title_key, source) VALUES (1, 'AN ALIAS', 'an alias', 'tmdb')`,
		`INSERT INTO seasons (series_id, season_number, monitored) VALUES (1, 1, 1)`,
		`INSERT INTO episodes (series_id, season_number, episode_number, air_date, monitored, has_file) VALUES (1, 1, 1, '2020-01-01', 1, 1)`,
		`INSERT INTO books (id, ol_key, title, author, description, subjects_json, ebook_path, ebook_format, monitored) VALUES (1, '/works/OL1W', 'Saltwind', 'A. Writer', 'A LONG DESCRIPTION', '["A SUBJECT"]', '/books/saltwind.epub', 'epub', 1)`,
		`INSERT INTO books (id, ol_key, title, author, keep_catalogue) VALUES (2, '/works/OL2W', 'Kept', 'A. Writer', 1)`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// The detail page still has it all.
	if got, _ := s.deps.Series.Get(context.Background(), 1); got.Overview == "" || got.Extra == nil || len(got.Aliases) == 0 {
		t.Fatalf("seed didn't take: %+v", got)
	}

	rec := s.do("GET", "/api/v1/series", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("series: HTTP %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, gone := range []string{"A LONG OVERVIEW", "SOME ACTOR", "AN ALIAS"} {
		if strings.Contains(body, gone) {
			t.Errorf("the series list still carries %q", gone)
		}
	}
	var sl struct{ Series []series.Series }
	_ = json.Unmarshal(rec.Body.Bytes(), &sl)
	if len(sl.Series) != 1 || sl.Series[0].Title != "Harbour Lights" || sl.Series[0].Network != "HBO" || sl.Series[0].Stats == nil || sl.Series[0].Stats.HaveFiles != 1 {
		t.Fatalf("what the grid shows went missing: %+v", sl.Series)
	}

	rec = s.do("GET", "/api/v1/books", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("books: HTTP %d %s", rec.Code, rec.Body)
	}
	body = rec.Body.String()
	for _, gone := range []string{"A LONG DESCRIPTION", "A SUBJECT"} {
		if strings.Contains(body, gone) {
			t.Errorf("the books list still carries %q", gone)
		}
	}
	var bl struct {
		Books      []books.Book
		Upgradable int
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bl)
	if len(bl.Books) != 2 || bl.Upgradable != 1 {
		t.Fatalf("books %d, upgradable %d — want 2 and 1 (the kept one isn't counted)", len(bl.Books), bl.Upgradable)
	}
	for _, b := range bl.Books {
		if b.ID == 1 && (b.Ebook == nil || b.Ebook.Path == "" || !b.HasFile) {
			t.Errorf("the edition the grid and delete dialog read went missing: %+v", b)
		}
	}
}
