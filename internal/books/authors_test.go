package books

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

func authorsService(t *testing.T, meta metadata.BookProvider) (*Service, context.Context) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB(), meta, slog.New(slog.NewTextHandler(io.Discard, nil))), context.Background()
}

// mustBook creates a library row directly (no author link: the shape of a library
// from before authors were rows).
func mustBook(t *testing.T, s *Service, ctx context.Context, key, title, author string) Book {
	t.Helper()
	b, err := s.repo.Create(ctx, Book{OLKey: key, Title: title, Author: author})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func authorOf(t *testing.T, s *Service, ctx context.Context, bookID int64) int64 {
	t.Helper()
	id, err := bookAuthorID(ctx, s.repo.db, bookID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func authorCount(t *testing.T, s *Service, ctx context.Context) int {
	t.Helper()
	var n int
	if err := s.repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM authors`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuthorNameIdentity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"J.K. Rowling", "J. K. Rowling", true},
		{"J.K. Rowling", "Rowling, J.K.", true},
		{"J.K. Rowling", "Rowling", true},    // a missing set of initials doesn't disagree
		{"J. Rowling", "J.K. Rowling", true}, // nor does a shorter one
		{"J. Smith", "K. Smith", false},      // different initials are different people
		{"George R. R. Martin", "Martin, George R.R.", true},
		{"Stephen King", "Tabitha King", false},
		{"José Saramago", "Jose Saramago", true}, // accents fold
		{"", "", false},
	} {
		if got := SameAuthorName(c.a, c.b); got != c.same {
			t.Errorf("SameAuthorName(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

// The backfill joins spelling variants into one author, keeps namesakes whose catalogue
// keys differ apart, leaves a name that could be either of two people unlinked, never
// changes a book's author string, and does nothing new on a second run.
func TestBackfillAuthors(t *testing.T) {
	s, ctx := authorsService(t, nil)
	hp1 := mustBook(t, s, ctx, "OL1W", "Harry Potter and the Philosopher's Stone", "J.K. Rowling")
	hp2 := mustBook(t, s, ctx, "OL2W", "Harry Potter and the Chamber of Secrets", "J. K. Rowling")
	hp3 := mustBook(t, s, ctx, "OL3W", "Harry Potter and the Prisoner of Azkaban", "Rowling, J.K.")
	// Two different people the old photo cache keyed apart.
	sm1 := mustBook(t, s, ctx, "OL4W", "Book by the novelist", "John Smith")
	sm2 := mustBook(t, s, ctx, "OL5W", "Book by the historian", "Smith, John")
	// Initials that disagree, and a bare surname that could be either.
	js := mustBook(t, s, ctx, "OL6W", "Jay's book", "J. Doe")
	ks := mustBook(t, s, ctx, "OL7W", "Kay's book", "K. Doe")
	bare := mustBook(t, s, ctx, "OL8W", "Whose book", "Doe")
	blank := mustBook(t, s, ctx, "OL9W", "Anonymous pamphlet", "")
	for _, row := range [][3]string{
		{"John Smith", "hc:a:1", "https://img/novelist.jpg"},
		{"Smith, John", "hc:a:2", ""},
		{"J.K. Rowling", "hc:a:7", "https://img/jkr.jpg"},
	} {
		if err := s.repo.setAuthorImage(ctx, row[0], row[1], row[2], ""); err != nil {
			t.Fatal(err)
		}
	}

	linked, unlinked, err := s.BackfillAuthors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 7 || unlinked != 1 {
		t.Errorf("linked %d, left unlinked %d; want 7 and 1", linked, unlinked)
	}
	rowling := authorOf(t, s, ctx, hp1.ID)
	if rowling == 0 || authorOf(t, s, ctx, hp2.ID) != rowling || authorOf(t, s, ctx, hp3.ID) != rowling {
		t.Errorf("Rowling spellings → %d %d %d, want one author", rowling, authorOf(t, s, ctx, hp2.ID), authorOf(t, s, ctx, hp3.ID))
	}
	if a1, a2 := authorOf(t, s, ctx, sm1.ID), authorOf(t, s, ctx, sm2.ID); a1 == 0 || a2 == 0 || a1 == a2 {
		t.Errorf("Smiths with different keys → %d and %d, want two authors", a1, a2)
	}
	if a1, a2 := authorOf(t, s, ctx, js.ID), authorOf(t, s, ctx, ks.ID); a1 == 0 || a2 == 0 || a1 == a2 {
		t.Errorf("J. Doe / K. Doe → %d and %d, want two authors", a1, a2)
	}
	if got := authorOf(t, s, ctx, bare.ID); got != 0 {
		t.Errorf("bare 'Doe' linked to %d, want unlinked (it could be either)", got)
	}
	if got := authorOf(t, s, ctx, blank.ID); got != 0 {
		t.Errorf("a book with no author linked to %d", got)
	}
	a, err := s.GetAuthor(ctx, rowling)
	if err != nil {
		t.Fatal(err)
	}
	if a.CatalogueKey != "hc:a:7" || a.ImageURL != "https://img/jkr.jpg" || a.Books != 3 || len(a.Names) != 3 {
		t.Errorf("Rowling author = %+v, want key hc:a:7, the cached photo, 3 books under 3 spellings", a)
	}
	// Every book keeps its own author string.
	for _, b := range []Book{hp1, hp2, hp3, sm1, sm2, js, ks, bare, blank} {
		got, err := s.repo.Get(ctx, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Author != b.Author {
			t.Errorf("book %d author %q → %q", b.ID, b.Author, got.Author)
		}
	}

	before := authorCount(t, s, ctx)
	linked, unlinked, err = s.BackfillAuthors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 0 || unlinked != 1 || authorCount(t, s, ctx) != before {
		t.Errorf("second run linked %d (unlinked %d), authors %d → %d; want nothing new", linked, unlinked, before, authorCount(t, s, ctx))
	}
}

// keyedCatalogue answers GetBook from a map, author keys included.
type keyedCatalogue struct {
	metadata.BookProvider
	byKey  map[string]metadata.BookResult
	detail map[string]*metadata.AuthorResult
}

func (c keyedCatalogue) GetBook(_ context.Context, key string) (*metadata.BookDetails, error) {
	r, ok := c.byKey[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return &metadata.BookDetails{BookResult: r}, nil
}

func (c keyedCatalogue) AuthorDetail(_ context.Context, key string) (*metadata.AuthorResult, error) {
	if d, ok := c.detail[key]; ok {
		return d, nil
	}
	return nil, metadata.ErrNotSupported
}
func (c keyedCatalogue) SeriesBooks(context.Context, string) (*metadata.SeriesInfo, error) {
	return nil, metadata.ErrNotSupported
}
func (c keyedCatalogue) SimilarBooks(context.Context, string) ([]metadata.BookResult, error) {
	return nil, metadata.ErrNotSupported
}
func (c keyedCatalogue) BookByISBN(context.Context, string) (*metadata.BookResult, error) {
	return nil, metadata.ErrNotSupported
}

// Add links a new book to its author by the catalogue's author key first, then by name;
// a namesake whose key differs in the same catalogue is someone else.
func TestAddLinksAuthorByKeyThenName(t *testing.T) {
	s, ctx := authorsService(t, keyedCatalogue{byKey: map[string]metadata.BookResult{
		"hc:1": {Key: "hc:1", Title: "The Final Empire", Author: "Brandon Sanderson", AuthorKey: "hc:a:5"},
		"hc:2": {Key: "hc:2", Title: "The Well of Ascension", Author: "Sanderson, Brandon", AuthorKey: "hc:a:5"},
		"hc:3": {Key: "hc:3", Title: "Elantris", Author: "Brandon Sanderson"},
		"hc:4": {Key: "hc:4", Title: "Another Brandon's Memoir", Author: "Brandon Sanderson", AuthorKey: "hc:a:9"},
	}})
	add := func(key string) Book {
		t.Helper()
		b, err := s.Add(ctx, key, "", true, metadata.BookResult{})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b1, b2, b3, b4 := add("hc:1"), add("hc:2"), add("hc:3"), add("hc:4")
	if b1.AuthorID == 0 || b2.AuthorID != b1.AuthorID || b3.AuthorID != b1.AuthorID {
		t.Errorf("authors %d %d %d, want one author for the first three", b1.AuthorID, b2.AuthorID, b3.AuthorID)
	}
	if b4.AuthorID == 0 || b4.AuthorID == b1.AuthorID {
		t.Errorf("a namesake with another Hardcover id → %d, want a new author (not %d)", b4.AuthorID, b1.AuthorID)
	}
	a, err := s.GetAuthor(ctx, b1.AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	if a.CatalogueKey != "hc:a:5" || a.Books != 3 {
		t.Errorf("author = %+v, want key hc:a:5 with 3 books", a)
	}
	// The library list carries the link.
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.AuthorID == 0 {
			t.Errorf("listed book %q has no author_id", b.Title)
		}
	}
	// A hand-corrected author follows the new name.
	if err := s.OverrideMetadata(ctx, b3.ID, "Elantris", "Someone Else", 2005, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := authorOf(t, s, ctx, b3.ID); got == 0 || got == b1.AuthorID {
		t.Errorf("after correcting the author the book is linked to %d, want a new author", got)
	}
}

// 'Add author' follows the author (monitored, new books on), links every book it adds —
// a co-written one listed under the co-author too — and links the author's books that
// were already in the library and unlinked.
func TestAddAuthorWorksLinksEveryBook(t *testing.T) {
	s, ctx := authorsService(t, nil)
	held := mustBook(t, s, ctx, "OL1W", "Mistborn: The Final Empire", "Brandon Sanderson")
	id, err := s.UpsertByKey(ctx, "OL1394865A", "Brandon Sanderson")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitoring(ctx, id, true, true, "book-ebook"); err != nil {
		t.Fatal(err)
	}
	works := []metadata.BookResult{
		{Key: "OL1W", Title: "Mistborn: The Final Empire", Author: "Brandon Sanderson"},
		{Key: "OL2W", Title: "The Way of Kings", Author: "Brandon Sanderson", AuthorKey: "OL1394865A"},
		{Key: "OL3W", Title: "The Wheel of Time: A Memory of Light", Author: "Robert Jordan"},
	}
	added, skipped := s.AddAuthorWorks(ctx, id, works, "book-ebook", true)
	if len(added) != 2 || skipped != 1 {
		t.Fatalf("added %d skipped %d, want 2 and 1", len(added), skipped)
	}
	for _, b := range added {
		if got := authorOf(t, s, ctx, b.ID); got != id {
			t.Errorf("%q linked to %d, want %d", b.Title, got, id)
		}
	}
	if got := authorOf(t, s, ctx, held.ID); got != id {
		t.Errorf("the book already held is linked to %d, want %d", got, id)
	}
	a, err := s.GetAuthor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Monitored || !a.MonitorNew || a.QualityProfile != "book-ebook" || a.Books != 3 || a.Wanted != 2 {
		t.Errorf("author = %+v, want monitored, monitor_new, profile book-ebook, 3 books, 2 wanted", a)
	}
	// Upserting the same key again is the same row.
	if again, err := s.UpsertByKey(ctx, "OL1394865A", "B. Sanderson"); err != nil || again != id {
		t.Errorf("UpsertByKey again = %d %v, want %d", again, err, id)
	}
	if _, err := s.UpsertByKey(ctx, "nonsense", "x"); !errors.Is(err, ErrBadAuthorKey) {
		t.Errorf("a non-key: err = %v, want ErrBadAuthorKey", err)
	}
}

// Picking a catalogue author sets the key and fills the photo; picking one another row
// already holds folds this row into it, keeping the follow settings.
func TestSetCatalogueKey(t *testing.T) {
	s, ctx := authorsService(t, keyedCatalogue{detail: map[string]*metadata.AuthorResult{
		"hc:a:1": {Key: "hc:a:1", Name: "J.K. Rowling", ImageURL: "https://img/jkr.jpg", Bio: "Author."},
	}})
	a := mustBook(t, s, ctx, "OL1W", "Harry Potter and the Philosopher's Stone", "J.K. Rowling")
	b := mustBook(t, s, ctx, "OL2W", "The Casual Vacancy", "Joanne Rowling")
	if _, _, err := s.BackfillAuthors(ctx); err != nil {
		t.Fatal(err)
	}
	ida, idb := authorOf(t, s, ctx, a.ID), authorOf(t, s, ctx, b.ID)
	if ida == 0 || idb == 0 || ida == idb {
		t.Fatalf("authors %d %d, want two rows", ida, idb)
	}
	if _, err := s.SetCatalogueKey(ctx, ida, "not a key"); !errors.Is(err, ErrBadAuthorKey) {
		t.Errorf("bad key: err = %v", err)
	}
	if kept, err := s.SetCatalogueKey(ctx, ida, "hc:a:1"); err != nil || kept != ida {
		t.Fatalf("SetCatalogueKey = %d %v, want %d", kept, err, ida)
	}
	got, err := s.GetAuthor(ctx, ida)
	if err != nil {
		t.Fatal(err)
	}
	if got.CatalogueKey != "hc:a:1" || got.ImageURL != "https://img/jkr.jpg" || got.Bio != "Author." {
		t.Errorf("after the pick = %+v, want key, photo and bio", got)
	}
	if err := s.SetMonitoring(ctx, idb, true, true, "book-audio"); err != nil {
		t.Fatal(err)
	}
	kept, err := s.SetCatalogueKey(ctx, idb, "hc:a:1")
	if err != nil || kept != ida {
		t.Fatalf("picking a key another row holds = %d %v, want the row that holds it (%d)", kept, err, ida)
	}
	if got := authorOf(t, s, ctx, b.ID); got != ida {
		t.Errorf("the folded row's book is linked to %d, want %d", got, ida)
	}
	if _, err := s.GetAuthor(ctx, idb); !errors.Is(err, ErrAuthorNotFound) {
		t.Errorf("the folded row still exists: %v", err)
	}
	merged, _ := s.GetAuthor(ctx, ida)
	if !merged.Monitored || !merged.MonitorNew || merged.QualityProfile != "book-audio" || merged.Books != 2 {
		t.Errorf("kept row = %+v, want followed with the folded row's settings and both books", merged)
	}
	// Old links resolve by any spelling on the author's books.
	named, err := s.AuthorsNamed(ctx, "Rowling, Joanne")
	if err != nil || len(named) != 1 || named[0].ID != ida {
		t.Errorf("AuthorsNamed = %+v %v, want just %d", named, err, ida)
	}
}

// A merge carries the dropped book's author to a keeper that has none, and leaves a
// keeper's own author alone.
func TestMergeCarriesAuthor(t *testing.T) {
	f := newMergeFixture(t)
	ctx := f.ctx
	herbert, err := f.s.UpsertByName(ctx, "Frank Herbert")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.UpsertByName(ctx, "Brian Herbert")
	if err != nil {
		t.Fatal(err)
	}
	keep, drop := f.book("OL1W", "Dune"), f.book("hc:1", "Dune")
	if err := setBookAuthor(ctx, f.s.repo.db, drop.ID, herbert); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Merge(ctx, keep.ID, drop.ID, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, keep.ID); got.AuthorID != herbert {
		t.Errorf("keeper's author = %d, want the dropped book's %d", got.AuthorID, herbert)
	}

	keep2, drop2 := f.book("OL2W", "Dune Messiah"), f.book("hc:2", "Dune Messiah")
	if err := setBookAuthor(ctx, f.s.repo.db, keep2.ID, herbert); err != nil {
		t.Fatal(err)
	}
	if err := setBookAuthor(ctx, f.s.repo.db, drop2.ID, other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Merge(ctx, keep2.ID, drop2.ID, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, keep2.ID); got.AuthorID != herbert {
		t.Errorf("keeper's own author replaced: %d, want %d", got.AuthorID, herbert)
	}
}
