package books

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/listening"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// editFixture is one book added from a catalogue that knows it, ready to be edited.
func editFixture(t *testing.T) (*Service, *refreshCatalogue, Book, context.Context) {
	t.Helper()
	cat := &refreshCatalogue{books: map[string]*metadata.BookDetails{}}
	s, ctx := refreshService(t, cat)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{{Key: "hc:1", Title: "Harry Potter and the Philosopher's Stone", Author: "J.K. Rowling", Year: 1997}}, "", true)
	if len(added) != 1 {
		t.Fatalf("seeded %d", len(added))
	}
	b := added[0]
	if err := s.repo.UpdateMeta(ctx, b.ID, "A boy wizard.", "", []string{"Fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.SetSeriesRef(ctx, b.ID, "Harry Potter", 1, "hc:s:1"); err != nil {
		t.Fatal(err)
	}
	cat.books["hc:1"] = &metadata.BookDetails{
		BookResult:  metadata.BookResult{Key: "hc:1", Title: "Harry Potter and the Philosopher's Stone", Author: "J.K. Rowling", Year: 1997},
		Description: "A boy wizard.", Subjects: []string{"Fantasy"}, SeriesName: "Harry Potter", SeriesPosition: 1, SeriesKey: "hc:s:1",
	}
	b, err := s.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, cat, b, ctx
}

func set(fields map[string]any) EditRequest {
	out := map[string]json.RawMessage{}
	for k, v := range fields {
		out[k] = jsonOf(v)
	}
	return EditRequest{Set: out}
}

func matchedOf(t *testing.T, b Book, field string) string {
	t.Helper()
	raw, ok := b.Edits[field]
	if !ok {
		t.Fatalf("%s isn't marked edited: %v", field, b.Edits)
	}
	return string(raw)
}

// An edit shows in place of the catalogue's value, the catalogue's stays underneath, and
// Matched gives the catalogue's book back.
func TestEditDetailsOverridesWin(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	res, err := s.EditDetails(ctx, b.ID, set(map[string]any{
		FieldTitle: "Harry Potter and the Sorcerer's Stone", FieldSubtitle: "Year One",
		FieldSeriesPosition: "1.5", FieldYear: 1998, FieldLanguage: "English",
		FieldSubjects: []string{"Magic", "magic", " Boarding school "}, FieldISBN: "978-0-590-35340-3",
		FieldPublisher: "Scholastic", FieldNarrator: "Jim Dale",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Book
	if got.Title != "Harry Potter and the Sorcerer's Stone" || got.Subtitle != "Year One" || got.SeriesPosition != 1.5 ||
		got.Year != 1998 || got.Language != "en" || got.ISBN != "9780590353403" || got.Publisher != "Scholastic" ||
		got.AudiobookNarrator != "Jim Dale" {
		t.Fatalf("shown = %+v", got)
	}
	if strings.Join(got.Subjects, "|") != "Magic|Boarding school" {
		t.Fatalf("subjects = %v (repeats dropped, spaces trimmed)", got.Subjects)
	}
	if m := matchedOf(t, got, FieldTitle); m != `"Harry Potter and the Philosopher's Stone"` {
		t.Fatalf("title underneath = %s", m)
	}
	if m := matchedOf(t, got, FieldSubtitle); m != `""` {
		t.Fatalf("subtitle underneath = %s", m)
	}
	if got.Edited(FieldAuthor) || got.Edited(FieldDescription) {
		t.Fatalf("untouched fields marked edited: %v", got.Edits)
	}
	want := map[string]bool{FieldTitle: true, FieldSubtitle: true, FieldSeriesPosition: true, FieldYear: true, FieldLanguage: true,
		FieldSubjects: true, FieldISBN: true, FieldPublisher: true, FieldNarrator: true}
	if len(res.Changed) != len(want) {
		t.Fatalf("changed = %v", res.Changed)
	}
	m := got.Matched()
	if m.Title != "Harry Potter and the Philosopher's Stone" || m.SeriesPosition != 1 || m.Year != 1997 || m.ISBN != "" ||
		strings.Join(m.Subjects, "|") != "Fantasy" || m.AudiobookNarrator != "" {
		t.Fatalf("matched = %+v", m)
	}
	// The list everything else reads shows the edit too.
	list, _ := s.List(ctx)
	if list[0].Title != "Harry Potter and the Sorcerer's Stone" {
		t.Fatalf("list shows %q", list[0].Title)
	}
	// An event says what was edited, never the values.
	events, _ := s.Events(ctx, b.ID, 5)
	if len(events) == 0 || events[0].Event != "edited" || !strings.Contains(events[0].Detail, "title") {
		t.Fatalf("events = %+v", events)
	}
}

// A refresh and a re-match write the catalogue's new values underneath an edit and never
// over it; fields that aren't edited follow the catalogue as before.
func TestRefreshAndRematchKeepEdits(t *testing.T) {
	s, cat, b, ctx := editFixture(t)
	if _, err := s.EditDetails(ctx, b.ID, set(map[string]any{
		FieldTitle: "Sorcerer's Stone", FieldAuthor: "Joanne Rowling", FieldDescription: "Mine.", FieldSeriesName: "Wizarding World",
	})); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, b.ID)

	// Nothing new from the catalogue: nothing written, and apps see no change.
	if _, err := s.Refresh(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	same, _ := s.Get(ctx, b.ID)
	if same.MetaUpdatedAt != before.MetaUpdatedAt {
		t.Fatalf("a refresh with nothing new moved meta_updated_at %d → %d", before.MetaUpdatedAt, same.MetaUpdatedAt)
	}

	// The catalogue changes its author, description, tags and series.
	cat.books["hc:1"] = &metadata.BookDetails{
		BookResult:  metadata.BookResult{Key: "hc:1", Title: "Harry Potter and the Philosopher's Stone", Author: "J. K. Rowling"},
		Description: "A boy wizard goes to school.", Subjects: []string{"Fantasy", "Children"},
		SeriesName: "Harry Potter (Hardcover)", SeriesPosition: 1, SeriesKey: "hc:s:1",
	}
	got, err := s.Refresh(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Sorcerer's Stone" || got.Author != "Joanne Rowling" || got.Description != "Mine." || got.SeriesName != "Wizarding World" {
		t.Fatalf("refresh undid an edit: %+v", got)
	}
	if strings.Join(got.Subjects, "|") != "Fantasy|Children" {
		t.Fatalf("an unedited field didn't follow the catalogue: %v", got.Subjects)
	}
	if m := got.Matched(); m.Author != "J. K. Rowling" || m.Description != "A boy wizard goes to school." || m.SeriesName != "Harry Potter (Hardcover)" {
		t.Fatalf("catalogue values underneath = %+v", m)
	}

	// A re-match to the same book on another catalogue keeps them too, by default.
	cat.books["OL9W"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "OL9W", Title: "Harry Potter and the Philosopher's Stone", Author: "J.K. Rowling", Year: 1997}, Description: "OL description"}
	got, err = s.Rematch(ctx, b.ID, "OL9W", metadata.BookResult{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.OLKey != "OL9W" || got.Title != "Sorcerer's Stone" || got.Author != "Joanne Rowling" || got.Description != "Mine." {
		t.Fatalf("re-match undid an edit: %+v", got)
	}
	if m := got.Matched(); m.Description != "OL description" || m.Author != "J.K. Rowling" {
		t.Fatalf("re-match didn't put its values underneath: %+v", m)
	}

	// Re-matched to a different book with "clear": the edits go and the new work shows.
	cat.books["OL10W"] = &metadata.BookDetails{BookResult: metadata.BookResult{Key: "OL10W", Title: "The Hobbit", Author: "J.R.R. Tolkien", Year: 1937}, Description: "There and back."}
	got, err = s.Rematch(ctx, b.ID, "OL10W", metadata.BookResult{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edits) != 0 || got.Title != "The Hobbit" || got.Author != "J.R.R. Tolkien" || got.Description != "There and back." || got.SeriesName != "Harry Potter (Hardcover)" {
		t.Fatalf("clear left %+v (edits %v)", got, got.Edits)
	}
}

// Each field resets on its own, Reset all resets everything, and typing the catalogue's
// value back is a reset.
func TestEditResetPerFieldAndAll(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	if _, err := s.EditDetails(ctx, b.ID, set(map[string]any{FieldTitle: "Edited", FieldYear: 2001, FieldPublisher: "Bloomsbury"})); err != nil {
		t.Fatal(err)
	}
	res, err := s.EditDetails(ctx, b.ID, EditRequest{Reset: []string{FieldYear}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Book.Year != 1997 || res.Book.Edited(FieldYear) || !res.Book.Edited(FieldTitle) || res.Book.Title != "Edited" {
		t.Fatalf("after resetting the year: %+v %v", res.Book, res.Book.Edits)
	}
	if strings.Join(res.Changed, ",") != FieldYear {
		t.Fatalf("changed = %v", res.Changed)
	}
	// The catalogue's own title typed back: no edit left.
	res, err = s.EditDetails(ctx, b.ID, set(map[string]any{FieldTitle: "Harry Potter and the Philosopher's Stone"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Book.Edited(FieldTitle) {
		t.Fatalf("typing the catalogue's value left an edit: %v", res.Book.Edits)
	}
	res, err = s.EditDetails(ctx, b.ID, EditRequest{ResetAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Book.Edits) != 0 || res.Book.Publisher != "" {
		t.Fatalf("reset all left %+v", res.Book)
	}
	if _, err := s.EditDetails(ctx, 9999, EditRequest{ResetAll: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing book: %v", err)
	}
}

// What can't be saved is refused with a reason, and nothing is written.
func TestEditValidation(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	bad := []map[string]any{
		{FieldTitle: ""},
		{FieldTitle: "   "},
		{FieldTitle: strings.Repeat("x", 301)},
		{FieldTitle: "Bad‮right-to-left"},
		{FieldTitle: "C1\u009Bcontrol"},
		{FieldPublisher: "Isolate⁦d"},
		{FieldAuthor: "A\x00B"},
		{FieldSubtitle: "​​"},
		{FieldISBN: "978-0-590-35340-4"}, // wrong check digit
		{FieldISBN: "0-306-40615-3"},     // wrong check digit (ISBN-10)
		{FieldISBN: "ISBN 9780590353403"},
		{FieldISBN: "12345"},
		{FieldSeriesPosition: "two"},
		{FieldSeriesPosition: -1},
		{FieldSeriesPosition: 1e9},
		{FieldYear: 99},
		{FieldYear: "1990s"},
		{FieldLanguage: "Klingonese"},
		{FieldSubjects: "Fantasy"},
		{FieldSubjects: []string{strings.Repeat("y", 101)}},
		{FieldDescription: "bell\x07"},
		{FieldDescription: strings.Repeat("z", 20001)},
		{FieldNarrator: strings.Repeat("n", 201)},
		{"cover_url": "https://example.com/x.jpg"},
	}
	for _, f := range bad {
		if _, err := s.EditDetails(ctx, b.ID, set(f)); !errors.Is(err, ErrEditInvalid) {
			t.Errorf("%v: err = %v, want ErrEditInvalid", f, err)
		}
	}
	if _, err := s.EditDetails(ctx, b.ID, EditRequest{Set: map[string]json.RawMessage{FieldTitle: jsonOf("X")}, Reset: []string{FieldTitle}}); !errors.Is(err, ErrEditInvalid) {
		t.Errorf("set and reset together: %v", err)
	}
	if _, err := s.EditDetails(ctx, b.ID, EditRequest{Reset: []string{"ol_key"}}); !errors.Is(err, ErrEditInvalid) {
		t.Errorf("reset of a field that isn't editable: %v", err)
	}
	got, _ := s.Get(ctx, b.ID)
	if len(got.Edits) != 0 {
		t.Fatalf("a refused edit was written: %v", got.Edits)
	}

	// Allowed: a multi-line description, an ISBN-10 with an X, a series cleared, 0 years,
	// and a one-line field pasted with a line break in it (spaces collapse to one).
	res, err := s.EditDetails(ctx, b.ID, set(map[string]any{
		FieldDescription: "One.\r\n\r\nTwo.", FieldISBN: "0-8044-2957-X", FieldSeriesName: "", FieldYear: "", FieldSeriesPosition: "#2",
		FieldSubtitle: "Pasted\n  across lines",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Book.Description != "One.\n\nTwo." || res.Book.ISBN != "080442957X" || res.Book.SeriesName != "" || res.Book.Year != 0 ||
		res.Book.SeriesPosition != 2 || res.Book.Subtitle != "Pasted across lines" {
		t.Fatalf("allowed edits saved as %+v", res.Book)
	}
}

func TestCleanISBN(t *testing.T) {
	for in, want := range map[string]string{
		"978-0-590-35340-3": "9780590353403", "9780590353403": "9780590353403", "0 306 40615 2": "0306406152",
		"0-8044-2957-x": "080442957X", "": "",
	} {
		got, err := CleanISBN(in)
		if err != nil || got != want {
			t.Errorf("CleanISBN(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"9780590353404", "0306406153", "978059035340", "97805903534033", "979-0-000-00000-0x"} {
		if _, err := CleanISBN(in); !errors.Is(err, ErrEditInvalid) {
			t.Errorf("CleanISBN(%q) accepted", in)
		}
	}
}

// A series learned from a release lands underneath a series edited by hand.
func TestSeriesFromReleaseKeepsEditedSeries(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	if _, err := s.EditDetails(ctx, b.ID, set(map[string]any{FieldSeriesName: "Hogwarts", FieldSeriesPosition: 1.5})); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSeries(ctx, b.ID, "Harry Potter", 3); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.SeriesName != "Hogwarts" || got.SeriesPosition != 1.5 {
		t.Fatalf("release overwrote the edited series: %q #%v", got.SeriesName, got.SeriesPosition)
	}
	if m := got.Matched(); m.SeriesName != "Harry Potter" || m.SeriesPosition != 3 {
		t.Fatalf("underneath = %q #%v", m.SeriesName, m.SeriesPosition)
	}
	// Series grouping follows the edited series.
	sib, _ := s.SeriesSiblings(ctx, "Hogwarts")
	if len(sib) != 1 || sib[0].ID != b.ID {
		t.Fatalf("siblings under the edited series = %+v", sib)
	}
	if sib, _ := s.SeriesSiblings(ctx, "Harry Potter"); len(sib) != 0 {
		t.Fatalf("still grouped under the catalogue's series: %+v", sib)
	}
	// The catalogue's series listing is the catalogue's series, not the edited one.
	if _, ok := s.CatalogueSeries(ctx, b.ID); ok {
		t.Fatal("an edited series still used the catalogue's listing")
	}
}

// The safety net: a plain UPDATE of an edited field (a code path that doesn't go through
// setMatched) lands underneath, and the edit stays.
func TestTriggerKeepsEditsFromPlainWrites(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	if _, err := s.EditDetails(ctx, b.ID, set(map[string]any{
		FieldTitle: "Edited", FieldYear: 2000, FieldSubjects: []string{"Mine"}, FieldSeriesPosition: 2.5,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.db.ExecContext(ctx,
		`UPDATE books SET title = 'Catalogue', author = 'New Author', year = 1999, subjects_json = '["A","B"]', series_position = 7 WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Title != "Edited" || got.Year != 2000 || strings.Join(got.Subjects, "|") != "Mine" || got.SeriesPosition != 2.5 {
		t.Fatalf("a plain write undid an edit: %+v", got)
	}
	if got.Author != "New Author" {
		t.Fatalf("an unedited field didn't take the write: %q", got.Author)
	}
	m := got.Matched()
	if m.Title != "Catalogue" || m.Year != 1999 || strings.Join(m.Subjects, "|") != "A|B" || m.SeriesPosition != 7 {
		t.Fatalf("underneath = %+v", m)
	}
	// Empty subjects go underneath as an empty list, not lost.
	if _, err := s.repo.db.ExecContext(ctx, `UPDATE books SET subjects_json = '' WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, b.ID)
	if !got.Edited(FieldSubjects) || string(got.Edits[FieldSubjects]) != "[]" {
		t.Fatalf("subjects underneath = %s", got.Edits[FieldSubjects])
	}
}

// An edit moves meta_updated_at (listening apps refetch the item); a catalogue value
// changing underneath an edit doesn't, since nothing an app shows changed.
func TestEditMovesMetaUpdatedAt(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	start, _ := s.Get(ctx, b.ID)
	res, err := s.EditDetails(ctx, b.ID, set(map[string]any{FieldPublisher: "Bloomsbury"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Book.MetaUpdatedAt <= start.MetaUpdatedAt {
		t.Fatalf("meta_updated_at %d → %d after an edit", start.MetaUpdatedAt, res.Book.MetaUpdatedAt)
	}
	if err := s.repo.SetMatched(ctx, b.ID, map[string]any{FieldPublisher: "Arthur A. Levine"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.MetaUpdatedAt != res.Book.MetaUpdatedAt || got.Publisher != "Bloomsbury" {
		t.Fatalf("a value underneath moved meta_updated_at or the shown value: %+v", got)
	}
}

// Releases named after either the edited title or the catalogue's match the book, and the
// longest title still wins between books.
func TestMatcherKnowsBothTitles(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	if _, err := s.EditDetails(ctx, b.ID, set(map[string]any{FieldTitle: "Harry Potter and the Sorcerer's Stone"})); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"J.K.Rowling - Harry Potter and the Sorcerer's Stone [M4B]",
		"Harry.Potter.and.the.Philosophers.Stone.EPUB",
	} {
		got, ok := s.MatchByRelease(ctx, rel)
		if !ok || got.ID != b.ID {
			t.Errorf("%q matched %v %v", rel, got.ID, ok)
		}
	}
	if _, ok := s.MatchByRelease(ctx, "Harry Potter and the Chamber of Secrets"); ok {
		t.Error("another book's release matched")
	}
}

// A merge keeps the dropped book's hand edits for the fields the kept book hasn't edited;
// the kept book's own edits win.
func TestMergeCarriesEdits(t *testing.T) {
	s, _, keep, ctx := editFixture(t)
	added, _ := s.AddWorks(ctx, []metadata.BookResult{{Key: "OL5W", Title: "Philosopher's Stone (dupe)", Author: "Rowling"}}, "", true)
	drop := added[0]
	if _, err := s.EditDetails(ctx, keep.ID, set(map[string]any{FieldPublisher: "Kept Press"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditDetails(ctx, drop.ID, set(map[string]any{FieldPublisher: "Dropped Press", FieldISBN: "9780590353403", FieldTitle: "Typed Title"})); err != nil {
		t.Fatal(err)
	}
	s.SetMergeHooks(listening.NewStore(s.repo.db), nil)
	if _, err := s.Merge(ctx, keep.ID, drop.ID, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, keep.ID)
	if got.Publisher != "Kept Press" || got.ISBN != "9780590353403" || got.Title != "Typed Title" {
		t.Fatalf("merged = %+v", got)
	}
	if m := got.Matched(); m.Title != "Harry Potter and the Philosopher's Stone" || m.ISBN != "" {
		t.Fatalf("underneath after merge = %+v", m)
	}
}

// The older all-at-once metadata route and the narrator route are hand edits too.
func TestOverrideMetadataAndNarratorAreEdits(t *testing.T) {
	s, _, b, ctx := editFixture(t)
	if err := s.OverrideMetadata(ctx, b.ID, "Typed", "J.K. Rowling", 1997, "A boy wizard.", "https://example.com/c.jpg"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Title != "Typed" || !got.Edited(FieldTitle) || got.Edited(FieldAuthor) || got.CoverURL != "https://example.com/c.jpg" {
		t.Fatalf("override = %+v %v", got, got.Edits)
	}
	if err := s.SetNarrator(ctx, b.ID, "Stephen Fry"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, b.ID); got.AudiobookNarrator != "Stephen Fry" || !got.Edited(FieldNarrator) {
		t.Fatalf("narrator = %q %v", got.AudiobookNarrator, got.Edits)
	}
	if err := s.SetNarrator(ctx, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, b.ID); got.AudiobookNarrator != "" || got.Edited(FieldNarrator) {
		t.Fatalf("cleared narrator = %q %v", got.AudiobookNarrator, got.Edits)
	}
}
