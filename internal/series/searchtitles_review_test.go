package series

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// A translated title is also some other show's name. Ghosts (US) is "Fantasmas" in
// Spanish, and so is HBO's English "Fantasmas", which the library has never heard of: a
// release by that name needs its language, the year or the country to say it's Ghosts.
func TestSearchFitNeedsEvidenceBeyondTheName(t *testing.T) {
	ghosts := Series{ID: 1, TMDBID: 1, Title: "Ghosts", Year: 2021, Extra: &SeriesExtra{OriginCountry: []string{"US"},
		LocalLangs: []string{"es"}, LocalTitles: []metadata.TranslatedTitle{{Lang: "es", Title: "Fantasmas"}}}}
	severance := Series{ID: 2, TMDBID: 2, Title: "Severance", Year: 2022, Extra: &SeriesExtra{OriginCountry: []string{"US"},
		LocalLangs: []string{"es"}, LocalTitles: []metadata.TranslatedTitle{{Lang: "es", Title: "Separación"}}}}
	fit := newSearchFit([]Series{ghosts, severance}, []string{"es"})
	for release, want := range map[string]bool{
		"Fantasmas.S01E03.1080p.WEB.h264-GRP":           false, // HBO's show: nothing says otherwise
		"Fantasmas.S01E03.1080p.AMZN.WEB-DL.DDP5.1-GRP": false,
		"Fantasmas.2024.S01E03.1080p.WEB.h264-GRP":      false, // another year's show
		"Fantasmas.S01E03.GERMAN.1080p.WEB.h264-GRP":    false, // another language
		"Fantasmas.S01E03.SPANISH.1080p.WEB.h264-GRP":   true,  // the title's language
		"Fantasmas.S01E03.MULTi.1080p.WEB.h264-GRP":     true,
		"Fantasmas.2021.S01E03.1080p.WEB.h264-GRP":      true, // the show's year
		"Fantasmas.US.S01E03.1080p.WEB.h264-GRP":        true, // the show's country
	} {
		f := fit.Fit(parser.Parse(release), ghosts)
		if f.OK != want {
			t.Errorf("%s: %+v, want OK=%v", release, f, want)
		}
		if !f.OK && f.Why == "" {
			t.Errorf("%s: refused without saying why", release)
		}
	}
}

// Letters, not bytes. "Тьма" is four letters (eight bytes): one short word, so it needs the
// year or country; a two-letter Cyrillic name never counts.
func TestSearchFitCountsLetters(t *testing.T) {
	if !shortWordTitle("Тьма") || shortWordTitle("Haus des Geldes") || shortWordTitle("Geister") {
		t.Fatal("shortWordTitle counts bytes")
	}
	if keyLen(parser.TitleKey("Он")) >= minLocalKeyLen {
		t.Fatal("a two-letter Cyrillic name counts as long enough")
	}
	dark := Series{ID: 1, TMDBID: 1, Title: "Dark", Year: 2017, Extra: &SeriesExtra{OriginCountry: []string{"DE"},
		LocalLangs: []string{"ru"}, LocalTitles: []metadata.TranslatedTitle{{Lang: "ru", Title: "Тьма"}, {Lang: "ru", Title: "Он"}}}}
	fit := newSearchFit([]Series{dark}, []string{"ru"})
	for release, want := range map[string]bool{
		"Тьма.S01E01.RUS.WEB-DL.1080p-GRP":      false, // the language isn't enough for a short word
		"Тьма.2017.S01E01.RUS.WEB-DL.1080p-GRP": true,
		"Он.2017.S01E01.RUS.WEB-DL.1080p-GRP":   false,
	} {
		if f := fit.Fit(parser.Parse(release), dark); f.OK != want {
			t.Errorf("%s: %+v, want OK=%v", release, f, want)
		}
	}
}

// An indexer is only ever asked by a title it can be asked by — Latin script — the first
// such in the setting's order.
func TestSearchFitQueryTitleLatinOnly(t *testing.T) {
	heist := Series{ID: 1, TMDBID: 1, Title: "Money Heist", Year: 2017, Extra: &SeriesExtra{LocalLangs: []string{"ru", "de"},
		LocalTitles: []metadata.TranslatedTitle{{Lang: "ru", Title: "Бумажный дом"}, {Lang: "de", Title: "Haus des Geldes"}}}}
	nine := Series{ID: 2, TMDBID: 2, Title: "9-1-1", Year: 2018, Extra: &SeriesExtra{LocalLangs: []string{"ru", "de"},
		LocalTitles: []metadata.TranslatedTitle{{Lang: "ru", Title: "911 служба спасения"}}}}
	fit := newSearchFit([]Series{heist, nine}, []string{"ru", "de"})
	if got := fit.QueryTitle(heist); got != "Haus des Geldes" {
		t.Errorf("Money Heist: %q, want the German title (the Russian one can't be asked by)", got)
	}
	if got := fit.QueryTitle(nine); got != "" {
		t.Errorf("9-1-1: %q, want none", got)
	}
}

// The search rule's own read of the library carries what identity needs (aliases
// included), and a library not yet read for the languages is said once in the log, naming
// the shows.
func TestServiceSearchFitReadsLibrary(t *testing.T) {
	ctx := context.Background()
	langs := []string{"de"}
	var logs strings.Builder
	s := localSeriesService(t, nil, t.TempDir(), &langs)
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	heist, err := s.repo.Create(ctx, germanShow(0, "Money Heist", 2017, "Haus des Geldes"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.repo.Create(ctx, Series{TMDBID: 99, Title: "Casa", Year: 2004, Extra: &SeriesExtra{LocalLangs: []string{"de"}}})
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.Get(ctx, heist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f := s.SearchFit(ctx).Fit(parser.Parse(heistDE), full); !f.OK {
		t.Fatalf("Money Heist's German release: %+v", f)
	}
	// Another show's alias is the German title: the read must carry aliases, or this passes.
	if _, err := s.AddAlias(ctx, other.ID, "Haus des Geldes", 0); err != nil {
		t.Fatal(err)
	}
	lib, err := s.repo.searchLibrary(ctx)
	if err != nil || len(lib) != 2 {
		t.Fatalf("searchLibrary = %d shows, %v", len(lib), err)
	}
	if f := s.SearchFit(ctx).Fit(parser.Parse(heistDE), full); f.OK {
		t.Errorf("a name another show has as an alias was taken: %+v", f)
	}

	// A show whose titles aren't read yet holds the tier off, and says so once.
	if _, err := s.repo.Create(ctx, Series{TMDBID: 7, Title: "Unread Show", Year: 2010}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if f := s.SearchFit(ctx).Fit(parser.Parse("Haus.des.Geldes.2017.S01E01.German-GRP"), full); f.OK {
			t.Errorf("matched with a show unread: %+v", f)
		}
	}
	if n := strings.Count(logs.String(), "until every show"); n != 1 || !strings.Contains(logs.String(), "Unread Show") {
		t.Errorf("log %q: want one line naming the unread show", logs.String())
	}
}
