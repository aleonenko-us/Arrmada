package books

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// releaseCatalogue is refreshCatalogue with an author's works list that a test can
// change between checks, and a record of which authors were asked about.
type releaseCatalogue struct {
	refreshCatalogue
	works    map[string][]metadata.BookResult
	worksErr error
	worksFor []string
}

func (c *releaseCatalogue) AuthorWorks(_ context.Context, key string, _ int) ([]metadata.BookResult, error) {
	c.worksFor = append(c.worksFor, key)
	c.used += c.spendPerCall
	if c.worksErr != nil {
		return nil, c.worksErr
	}
	return c.works[key], nil
}

func releaseService(t *testing.T) (*Service, *releaseCatalogue, context.Context) {
	t.Helper()
	cat := &releaseCatalogue{refreshCatalogue: refreshCatalogue{books: map[string]*metadata.BookDetails{}}, works: map[string][]metadata.BookResult{}}
	s, ctx := authorsService(t, cat)
	return s, cat, ctx
}

// followed creates an author row under key and follows them, adding new books.
func followed(t *testing.T, s *Service, ctx context.Context, key, name, profile string) int64 {
	t.Helper()
	id, err := s.UpsertByKey(ctx, key, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitoring(ctx, id, true, true, profile); err != nil {
		t.Fatal(err)
	}
	return id
}

// dueAgain backdates an author's last check past the weekly wait.
func dueAgain(t *testing.T, s *Service, id int64) {
	t.Helper()
	if _, err := s.repo.db.Exec(`UPDATE authors SET works_checked_at = datetime('now', '-8 days') WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func bookCount(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.repo.db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func work(key, title string) metadata.BookResult {
	return metadata.BookResult{Key: key, Title: title, Author: "Brandon Sanderson", AuthorKey: "hc:a:1", Year: time.Now().Year()}
}

// The first check of a followed author only records the catalogue; a later check adds
// the work the catalogue newly lists — monitored, with the author's profile, linked to
// the author, with a timeline line and the catalogue's release date — and the prefix
// sibling of an owned book is a book of its own. [BOOK-21]
func TestCheckAuthorReleasesSeedsThenAdds(t *testing.T) {
	s, cat, ctx := releaseService(t)
	owned := mustBook(t, s, ctx, "hc:10", "Mistborn", "Brandon Sanderson")
	author := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "ebook-only")
	cat.works["hc:a:1"] = []metadata.BookResult{
		work("hc:10", "Mistborn"),
		work("hc:11", "Elantris"), // skipped by the admin: never added
	}

	res, err := s.CheckAuthorReleases(ctx, 10, "default")
	if err != nil || res.Checked != 1 || res.Seeded != 1 || res.Added() != 0 {
		t.Fatalf("first check = %+v, %v; want one first look, nothing added", res, err)
	}
	if bookCount(t, s) != 1 {
		t.Fatal("the first check added books")
	}

	// Checked a moment ago: not due again for a week.
	cat.worksFor = nil
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Checked != 0 || len(cat.worksFor) != 0 {
		t.Fatalf("re-check inside the week = %+v, asked %v", res, cat.worksFor)
	}

	dueAgain(t, s, author)
	cat.works["hc:a:1"] = append(cat.works["hc:a:1"],
		work("hc:12", "Wind and Truth"),
		work("hc:13", "Mistborn: Secret History"),
	)
	future := time.Now().AddDate(0, 2, 0).Format("2006-01-02")
	cat.books["hc:12"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "hc:12", Title: "Wind and Truth", Author: "Brandon Sanderson", ReleaseDate: future}, Description: "Stormlight 5"}
	res, err = s.CheckAuthorReleases(ctx, 10, "default")
	if err != nil || res.Added() != 2 || len(res.Releases) != 1 {
		t.Fatalf("later check = %+v, %v; want 2 added", res, err)
	}
	rel := res.Releases[0]
	if rel.AuthorID != author || rel.Author != "Brandon Sanderson" {
		t.Errorf("release = %+v", rel)
	}
	for _, b := range rel.Books {
		if !b.Monitored || b.QualityProfile != "ebook-only" || b.AuthorID != author {
			t.Errorf("added %q: monitored %v, profile %q, author %d", b.Title, b.Monitored, b.QualityProfile, b.AuthorID)
		}
		ev, _ := s.repo.Events(ctx, b.ID, 5)
		if len(ev) == 0 || ev[len(ev)-1].Detail != "New from Brandon Sanderson" {
			t.Errorf("%q timeline = %+v", b.Title, ev)
		}
		if b.OLKey == "hc:12" && (b.ReleaseDate != future || b.Description != "Stormlight 5") {
			t.Errorf("new book wasn't refreshed before return: %+v", b)
		}
	}
	if bookCount(t, s) != 3 {
		t.Errorf("library has %d books, want 3 (Elantris stays skipped)", bookCount(t, s))
	}
	a, _ := s.GetAuthor(ctx, author)
	if a.NewBooksFound != 2 || a.WorksCheckedAt == "" {
		t.Errorf("author after the check = %+v", a)
	}
	_ = owned
}

// A book the check added and someone deleted isn't added again — not under its key,
// and not when the catalogue lists it under a new one. [BOOK-21]
func TestCheckAuthorReleasesDeletedStaysGone(t *testing.T) {
	s, cat, ctx := releaseService(t)
	author := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn")}
	if _, err := s.CheckAuthorReleases(ctx, 10, "default"); err != nil {
		t.Fatal(err)
	}
	dueAgain(t, s, author)
	cat.works["hc:a:1"] = append(cat.works["hc:a:1"], work("hc:12", "Wind and Truth"))
	res, _ := s.CheckAuthorReleases(ctx, 10, "default")
	if res.Added() != 1 {
		t.Fatalf("added %d, want 1", res.Added())
	}
	if res.Releases[0].Books[0].QualityProfile != "default" {
		t.Errorf("an author without a profile should get the default, got %q", res.Releases[0].Books[0].QualityProfile)
	}
	if err := s.repo.Delete(ctx, res.Releases[0].Books[0].ID); err != nil {
		t.Fatal(err)
	}

	dueAgain(t, s, author)
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 0 {
		t.Fatalf("a deleted book came back: %+v", res)
	}
	dueAgain(t, s, author)
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn"), work("hc:99", "Wind and Truth")}
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 0 {
		t.Fatalf("a deleted book came back under a new key: %+v", res)
	}
	if bookCount(t, s) != 0 {
		t.Errorf("library has %d books, want 0", bookCount(t, s))
	}
}

// Only authors that are followed with "add new books" on, and that have a catalogue key,
// are asked about. [BOOK-21]
func TestCheckAuthorReleasesSkipsUnfollowed(t *testing.T) {
	s, cat, ctx := releaseService(t)
	paused, _ := s.UpsertByKey(ctx, "hc:a:2", "Paused Author")
	_ = s.SetMonitoring(ctx, paused, false, true, "")
	noNew, _ := s.UpsertByKey(ctx, "hc:a:3", "No New Author")
	_ = s.SetMonitoring(ctx, noNew, true, false, "")
	keyless, _ := s.UpsertByName(ctx, "Keyless Author")
	_ = s.SetMonitoring(ctx, keyless, true, true, "")
	followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn")}

	res, err := s.CheckAuthorReleases(ctx, 10, "default")
	if err != nil || res.Checked != 1 {
		t.Fatalf("check = %+v, %v", res, err)
	}
	if len(cat.worksFor) != 1 || cat.worksFor[0] != "hc:a:1" {
		t.Errorf("asked about %v, want only the followed author", cat.worksFor)
	}
}

// Over 70% of the day's Hardcover budget the run stops before asking — nothing added,
// nobody stamped — and a budget refusal mid-run stops it the same way. [BOOK-21]
func TestCheckAuthorReleasesBudgetGate(t *testing.T) {
	s, cat, ctx := releaseService(t)
	a1 := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	followed(t, s, ctx, "hc:a:2", "Robin Hobb", "")
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn")}
	cat.works["hc:a:2"] = []metadata.BookResult{{Key: "hc:20", Title: "Assassin's Apprentice", Author: "Robin Hobb"}}
	cat.budget, cat.used = 100, 71

	res, err := s.CheckAuthorReleases(ctx, 10, "default")
	if err != nil || res.Skipped != SkippedHardcoverBudget || res.Checked != 0 || len(cat.worksFor) != 0 {
		t.Fatalf("over budget: %+v, %v, asked %v", res, err, cat.worksFor)
	}
	if a, _ := s.GetAuthor(ctx, a1); a.WorksCheckedAt != "" {
		t.Error("a skipped author was stamped")
	}

	// The first author's look pushes usage over the gate: the second waits.
	cat.used, cat.spendPerCall = 69, 2
	res, _ = s.CheckAuthorReleases(ctx, 10, "default")
	if res.Checked != 1 || res.Skipped != SkippedHardcoverBudget || len(cat.worksFor) != 1 {
		t.Fatalf("crossing the gate: %+v, asked %v", res, cat.worksFor)
	}

	cat.used, cat.spendPerCall, cat.worksErr = 0, 0, metadata.ErrHardcoverBudget
	cat.worksFor = nil
	res, _ = s.CheckAuthorReleases(ctx, 10, "default")
	if res.Skipped != SkippedHardcoverBudget || len(cat.worksFor) != 1 {
		t.Fatalf("budget refusal: %+v, asked %v", res, cat.worksFor)
	}
}

// A catalogue that can't answer (an error, or an empty list) doesn't count as the first
// look — the next successful one still only records — and the author is tried again in
// a day, not at once. [BOOK-21]
func TestCheckAuthorReleasesFailedLookIsNoSeed(t *testing.T) {
	s, cat, ctx := releaseService(t)
	author := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	cat.worksErr = errors.New(`hardcover: field "books" not found`)
	if res, err := s.CheckAuthorReleases(ctx, 10, "default"); err != nil || res.Checked != 0 {
		t.Fatalf("failed look = %+v, %v", res, err)
	}
	cat.worksErr, cat.worksFor = nil, nil
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); len(cat.worksFor) != 0 {
		t.Fatalf("retried at once: %+v", res)
	}
	var due bool
	if err := s.repo.db.QueryRow(`SELECT works_checked_at < datetime('now', '-5 days') FROM authors WHERE id = ?`, author).Scan(&due); err != nil || !due {
		t.Fatalf("retry stamp should be about six days back (err %v)", err)
	}

	dueAgain(t, s, author) // an empty list is no answer either
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Checked != 0 || res.Seeded != 0 {
		t.Fatalf("empty list = %+v", res)
	}

	dueAgain(t, s, author)
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn"), work("hc:11", "Elantris")}
	res, _ := s.CheckAuthorReleases(ctx, 10, "default")
	if res.Seeded != 1 || res.Added() != 0 || bookCount(t, s) != 0 {
		t.Fatalf("first successful look = %+v, %d books", res, bookCount(t, s))
	}
}

// Old books the catalogue only now lists are recorded, not added; and a flood of "new"
// works at once is held back as a catalogue change. [BOOK-21]
func TestCheckAuthorReleasesOldAndFlood(t *testing.T) {
	s, cat, ctx := releaseService(t)
	author := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn")}
	_, _ = s.CheckAuthorReleases(ctx, 10, "default")

	dueAgain(t, s, author)
	old := work("hc:11", "Elantris")
	old.Year = 2005
	cat.works["hc:a:1"] = append(cat.works["hc:a:1"], old)
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 0 {
		t.Fatalf("an old book was added: %+v", res)
	}

	dueAgain(t, s, author)
	list := []metadata.BookResult{work("hc:10", "Mistborn"), old}
	for i := 0; i <= maxNewWorksPerCheck; i++ {
		list = append(list, work("hc:x"+string(rune('a'+i)), "Translation "+string(rune('A'+i))))
	}
	cat.works["hc:a:1"] = list
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 0 {
		t.Fatalf("a flood of works was added: %+v", res)
	}
	// Held, not recorded: if the list settles to one real new book, it is added.
	dueAgain(t, s, author)
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn"), old, work("hc:xa", "Translation A")}
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 1 {
		t.Fatalf("after the flood settled: %+v", res)
	}
}

// "Add author" records the list it added from, so the first scheduled look already adds
// what's new; following an author again, or pointing them at another catalogue entry,
// makes the next look a first look; joining two rows keeps the record. [BOOK-21]
func TestAuthorWorksSeedAndReset(t *testing.T) {
	s, cat, ctx := releaseService(t)
	author := followed(t, s, ctx, "hc:a:1", "Brandon Sanderson", "")
	if err := s.SeedAuthorWorks(ctx, author, []metadata.BookResult{work("hc:10", "Mistborn")}); err != nil {
		t.Fatal(err)
	}
	dueAgain(t, s, author)
	cat.works["hc:a:1"] = []metadata.BookResult{work("hc:10", "Mistborn"), work("hc:12", "Wind and Truth")}
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Added() != 1 {
		t.Fatalf("after Add author's record: %+v", res)
	}

	// Unfollow, the catalogue grows, follow again: the next look only records.
	_ = s.SetMonitoring(ctx, author, false, true, "")
	cat.works["hc:a:1"] = append(cat.works["hc:a:1"], work("hc:13", "Isles of the Emberdark"))
	_ = s.SetMonitoring(ctx, author, true, true, "")
	if res, _ := s.CheckAuthorReleases(ctx, 10, "default"); res.Seeded != 1 || res.Added() != 0 {
		t.Fatalf("after following again: %+v", res)
	}
	// Changing only the profile of a followed author keeps the record.
	_ = s.SetMonitoring(ctx, author, true, true, "ebook-only")
	if a, _ := s.GetAuthor(ctx, author); !a.worksSeeded || a.WorksCheckedAt == "" {
		t.Fatalf("a profile change reset the record: %+v", a)
	}

	// Another catalogue entry: the next look only records.
	if _, err := s.SetCatalogueKey(ctx, author, "OL1A"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetAuthor(ctx, author); a.worksSeeded || a.WorksCheckedAt != "" {
		t.Fatalf("a new catalogue key kept the old record: %+v", a)
	}

	// Joining into another row moves the seen works with the books.
	other := followed(t, s, ctx, "hc:a:7", "B. Sanderson", "")
	if keep, err := s.SetCatalogueKey(ctx, author, "hc:a:7"); err != nil || keep != other {
		t.Fatalf("join = %d, %v", keep, err)
	}
	seen, _ := s.repo.worksSeen(ctx, other)
	if !seen["hc:12"] || !seen[titleSeenPrefix+IdentityOf("Wind and Truth", "").Full] {
		t.Errorf("seen works didn't follow the join: %v", seen)
	}
	if a, _ := s.GetAuthor(ctx, other); a.NewBooksFound != 1 || a.worksSeeded {
		t.Errorf("kept row after the join = %+v", a)
	}
}
