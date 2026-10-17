package movies

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// displayFixture is a library service whose TMDB knows four films — Greek, Italian,
// Japanese and English — and whose "Show titles as" setting is *p.
func displayFixture(t *testing.T) (*Service, *fakeTitles, *displaytitle.Policy) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	meta := fakeMeta{films: []metadata.MovieResult{
		{TMDBID: 10, Title: "The Travelling Players", Year: 1975},
		{TMDBID: 20, Title: "The Great Beauty", Year: 2013},
		{TMDBID: 30, Title: "Spirited Away", Year: 2001},
		{TMDBID: 40, Title: "Dune", Year: 2021},
	}}
	s := NewService(st.DB(), meta, nil, t.TempDir(), "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src := &fakeTitles{titles: map[int]*metadata.Titles{
		10: {Title: "The Travelling Players", OriginalTitle: "Ο Θίασος", OriginalLanguage: "el",
			Translations: []metadata.TranslatedTitle{{Lang: "de", Title: "Die Wanderschauspieler"}}},
		20: {Title: "The Great Beauty", OriginalTitle: "La grande bellezza", OriginalLanguage: "it",
			Translations: []metadata.TranslatedTitle{{Lang: "el", Title: "Η Τέλεια Ομορφιά"}}},
		30: {Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し", OriginalLanguage: "ja",
			Translations: []metadata.TranslatedTitle{{Lang: "it", Title: "La città incantata"}}},
		40: {Title: "Dune", OriginalTitle: "Dune", OriginalLanguage: "en"},
	}}
	s.SetTitlesSource(src)
	p := displaytitle.ParsePolicy("", "", "")
	s.SetDisplayPolicy(func() displaytitle.Policy { return p })
	return s, src, &p
}

// shown is every film's shown title by TMDB id.
func shown(t *testing.T, s *Service) map[int]string {
	t.Helper()
	all, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for _, m := range all {
		out[m.TMDBID] = m.DisplayTitle
	}
	return out
}

func wantShown(t *testing.T, s *Service, want map[int]string) {
	t.Helper()
	got := shown(t, s)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("film %d shows %q, want %q", id, got[id], w)
		}
	}
}

// The reporter's case (GitHub #27): Greek and Italian films in their own language,
// everything else in English, from names read by the throttled backfill and stored — then
// "My language: Italian", then back to the default.
func TestDisplayTitlesGreekAndItalian(t *testing.T) {
	ctx := context.Background()
	s, src, p := displayFixture(t)
	for _, id := range []int{10, 20, 30, 40} {
		if _, err := s.AddWith(ctx, id, AddOptions{QualityProfile: "hd"}); err != nil {
			t.Fatal(err)
		}
	}
	// The default setting reads nothing and shows TMDB's English titles.
	if src.calls.Load() != 0 {
		t.Fatalf("the default setting asked TMDB %d times", src.calls.Load())
	}
	wantShown(t, s, map[int]string{10: "The Travelling Players", 20: "The Great Beauty", 30: "Spirited Away", 40: "Dune"})

	*p = displaytitle.ParsePolicy("english", "", "el,it")
	start := time.Now()
	n, err := s.BackfillAltTitles(ctx, 20*time.Millisecond)
	if err != nil || n != 4 || src.calls.Load() != 4 {
		t.Fatalf("backfill filled %d (%v) with %d calls, want 4 and 4", n, err, src.calls.Load())
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Errorf("four films took %v: the backfill didn't keep its pace", time.Since(start))
	}
	wantShown(t, s, map[int]string{10: "Ο Θίασος", 20: "La grande bellezza", 30: "Spirited Away", 40: "Dune"})
	// Nothing left to read: a second pass asks TMDB nothing.
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || src.calls.Load() != 4 {
		t.Errorf("second pass filled %d (%d calls), want nothing", n, src.calls.Load())
	}

	// The library list carries the shown title and sorts by it; the canonical title stays.
	sums, err := s.ListSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range sums {
		switch m.Title {
		case "The Travelling Players":
			if m.DisplayTitle != "Ο Θίασος" || m.SortTitle != "ο θίασος" {
				t.Errorf("summary = %+v, want the Greek title shown and sorted by", m)
			}
		case "The Great Beauty":
			if m.DisplayTitle != "La grande bellezza" || m.SortTitle != "la grande bellezza" {
				t.Errorf("summary = %+v, want the Italian title shown and sorted by", m)
			}
		case "Spirited Away":
			if m.DisplayTitle != "Spirited Away" || m.SortTitle != "spirited away" {
				t.Errorf("summary = %+v, want the English title", m)
			}
		}
	}

	// My language: Italian. Until the Italian names are read the best stored title shows
	// (the original); the backfill reads them once.
	*p = displaytitle.ParsePolicy("language", "it", "")
	if _, err := s.SyncDisplayTitles(ctx); err != nil {
		t.Fatal(err)
	}
	wantShown(t, s, map[int]string{30: "千と千尋の神隠し"})
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 4 {
		t.Errorf("a new language read %d films, want 4", n)
	}
	wantShown(t, s, map[int]string{10: "Ο Θίασος", 20: "La grande bellezza", 30: "La città incantata", 40: "Dune"})

	// Back to the default: every film shows its English title and nothing is stored.
	*p = displaytitle.ParsePolicy("", "", "")
	if n, err := s.SyncDisplayTitles(ctx); err != nil || n != 3 {
		t.Errorf("back to English changed %d (%v), want 3", n, err)
	}
	wantShown(t, s, map[int]string{10: "The Travelling Players", 20: "The Great Beauty", 30: "Spirited Away", 40: "Dune"})
	var stored int
	if err := s.repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies WHERE display_title <> ''`).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("%d display titles stored under the default (%v)", stored, err)
	}
}

// A per-film override beats the setting, survives a metadata refresh (which rewrites the
// extra metadata) and, cleared, hands the film back to the setting.
func TestDisplayOverrideSurvivesRefresh(t *testing.T) {
	ctx := context.Background()
	s, _, p := displayFixture(t)
	*p = displaytitle.ParsePolicy("original", "", "")
	m, err := s.AddWith(ctx, 10, AddOptions{QualityProfile: "hd"})
	if err != nil {
		t.Fatal(err)
	}
	if m.DisplayTitle != "Ο Θίασος" {
		t.Fatalf("added film shows %q, want its original title", m.DisplayTitle)
	}
	got, err := s.SetTitleOverride(ctx, m.ID, "  O Thiasos ")
	if err != nil || got.DisplayTitle != "O Thiasos" || got.TitleOverride != "O Thiasos" || got.Title != "The Travelling Players" {
		t.Fatalf("override: %+v %v", got, err)
	}
	if got, err = s.Refresh(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if got.TitleOverride != "O Thiasos" || got.DisplayTitle != "O Thiasos" || got.Title != "The Travelling Players" {
		t.Errorf("after refresh: override %q, shown %q, title %q", got.TitleOverride, got.DisplayTitle, got.Title)
	}
	if got.Extra == nil || got.Extra.DisplayNames == nil || got.Extra.DisplayNames.Original != "Ο Θίασος" {
		t.Errorf("refresh lost the display names: %+v", got.Extra)
	}
	if got, err = s.SetTitleOverride(ctx, m.ID, ""); err != nil || got.DisplayTitle != "Ο Θίασος" || got.TitleOverride != "" {
		t.Errorf("cleared: %+v %v", got, err)
	}
	if _, err := s.SetTitleOverride(ctx, m.ID, "two\nlines"); !errors.Is(err, ErrBadOverride) {
		t.Errorf("a newline: %v, want ErrBadOverride", err)
	}
	if _, err := s.SetTitleOverride(ctx, 9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown film: %v, want ErrNotFound", err)
	}
	// Choices: English, original, then TMDB's translations.
	choices, err := s.TitleChoices(ctx, m.ID)
	if err != nil || len(choices) != 3 || choices[1].Title != "Ο Θίασος" || choices[2].Title != "Die Wanderschauspieler" {
		t.Errorf("choices = %+v %v", choices, err)
	}
}

// The shown title is presentation only: the title keys a release is matched by, the
// library match itself and the rename plan (file and folder names) are exactly what they
// were before, under any setting and override.
func TestDisplayNeverAffectsMatchingOrNaming(t *testing.T) {
	ctx := context.Background()
	f := newDeleteFixture(t, "")
	before, err := f.svc.Get(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	keysBefore := TitleKeys(before, nil)
	planBefore := planByVersion(t, f)

	p := displaytitle.ParsePolicy("original", "", "el")
	f.svc.SetDisplayPolicy(func() displaytitle.Policy { return p })
	if err := f.svc.setDisplayNames(ctx, f.id, &displaytitle.Names{Original: "Ο Ληστής", OriginalLanguage: "el", Read: true}); err != nil {
		t.Fatal(err)
	}
	after, err := f.svc.Get(ctx, f.id)
	if err != nil || after.DisplayTitle != "Ο Ληστής" {
		t.Fatalf("shown %q (%v), want the original", after.DisplayTitle, err)
	}
	if _, err := f.svc.SetTitleOverride(ctx, f.id, "Heat — Director's Choice"); err != nil {
		t.Fatal(err)
	}
	after, _ = f.svc.Get(ctx, f.id)

	if got := TitleKeys(after, nil); !slices.Equal(got, keysBefore) {
		t.Errorf("title keys %v, were %v", got, keysBefore)
	}
	if m, ok := f.svc.Match(ctx, "Heat", 1995); !ok || m.ID != f.id {
		t.Error("the canonical title no longer matches")
	}
	for _, name := range []string{"Ο Ληστής", "Heat Director's Choice"} {
		if _, ok := f.svc.Match(ctx, name, 1995); ok {
			t.Errorf("the shown title %q matched a release", name)
		}
	}
	planAfter := planByVersion(t, f)
	for vid, it := range planBefore {
		if planAfter[vid].Proposed != it.Proposed {
			t.Errorf("track %d is renamed to %q, was %q", vid, planAfter[vid].Proposed, it.Proposed)
		}
	}
}
