package series

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/store"
)

// fakeTitles is TMDB's every-name lookup and localized search from tables, counting calls.
type fakeTitles struct {
	titles map[int]*metadata.Titles
	hits   map[string][]metadata.LocalizedHit // by language
	err    error
	calls  atomic.Int32
}

func (f *fakeTitles) Titles(_ context.Context, _ string, id int) (*metadata.Titles, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	if t, ok := f.titles[id]; ok {
		return t, nil
	}
	return nil, metadata.ErrNotFound
}

func (f *fakeTitles) SearchLocalized(_ context.Context, _, _, lang string) ([]metadata.LocalizedHit, error) {
	return f.hits[lang], nil
}

func germanShow(id int64, title string, year int, local ...string) Series {
	s := Series{ID: id, TMDBID: int(id), Title: title, Year: year, Extra: &SeriesExtra{LocalLangs: []string{"de"}}}
	for _, l := range local {
		s.Extra.LocalTitles = append(s.Extra.LocalTitles, metadata.TranslatedTitle{Lang: "de", Title: l})
	}
	return s
}

// The extra title languages in import routing: off, nothing changes; on, a show's
// translated title routes a release to it — never a name another show goes by, never one
// two shows share (both come back as candidates), and always with the year check.
func TestMatchReleaseTranslatedTitles(t *testing.T) {
	heist := germanShow(1, "Money Heist", 2017, "Haus des Geldes")
	haus := germanShow(2, "Haus", 2004)
	other := germanShow(3, "Other House", 2010, "Haus") // its German title is another show's own
	boatA := germanShow(4, "The Boat", 2010, "Das Boot")
	boatB := germanShow(5, "Boat Life", 2019, "Das Boot")
	all := []Series{heist, haus, other, boatA, boatB}

	match := func(release string, langs []string) (int64, []Series) {
		s, ok, cands := matchRelease(all, parser.Parse(release), langs)
		if !ok {
			return 0, cands
		}
		return s.ID, nil
	}
	if id, _ := match("Haus.des.Geldes.S01E01.German.1080p.WEB-GRP", nil); id != 0 {
		t.Errorf("off: matched %d", id)
	}
	if id, _ := match("Haus.des.Geldes.S01E01.German.1080p.WEB-GRP", []string{"fr"}); id != 0 {
		t.Errorf("another language: matched %d", id)
	}
	de := []string{"de"}
	if id, _ := match("Haus.des.Geldes.S01E01.German.1080p.WEB-GRP", de); id != heist.ID {
		t.Errorf("German: matched %d, want Money Heist", id)
	}
	if id, _ := match("Haus.des.Geldes.1990.S01E01.German.1080p.WEB-GRP", de); id != 0 {
		t.Errorf("the year guard: matched %d", id)
	}
	if id, _ := match("Haus.2010.S01E01.German.1080p.WEB-GRP", de); id != 0 {
		t.Errorf("another show's own title (another year) went to the show translated so: %d", id)
	}
	if id, _ := match("Haus.S01E01.1080p.WEB-GRP", de); id != haus.ID {
		t.Errorf("the show's own title: matched %d", id)
	}
	id, cands := match("Das.Boot.S01E01.German.1080p.WEB-GRP", de)
	if id != 0 || len(cands) != 2 {
		t.Errorf("a title two shows share: matched %d with %d candidates, want none and both named", id, len(cands))
	}
	if id, _ := match("Das.Boot.2019.S01E01.German.1080p.WEB-GRP", de); id != boatB.ID {
		t.Errorf("the year picks one: matched %d", id)
	}
}

// KnownAs: any name TMDB has for the show, in any language, with the year check; an error
// rather than a guess when the names can't be read.
func TestSeriesKnownAs(t *testing.T) {
	ctx := context.Background()
	var langs []string
	s := localSeriesService(t, nil, t.TempDir(), &langs)
	heist := Series{ID: 1, TMDBID: 71446, Title: "Money Heist", Year: 2017}
	if _, err := s.KnownAs(ctx, heist, parser.Parse("Haus.des.Geldes.S01E01.German-GRP")); !errors.Is(err, ErrNoTitles) {
		t.Errorf("no titles source: %v", err)
	}
	src := &fakeTitles{titles: map[int]*metadata.Titles{71446: {
		Title: "Money Heist", OriginalTitle: "La casa de papel",
		Alternatives: []metadata.AltTitle{{Title: "La Casa de Papel", Country: "ES"}},
		Translations: []metadata.TranslatedTitle{{Lang: "de", Title: "Haus des Geldes"}},
	}}}
	s.SetTitlesSource(src)
	for release, want := range map[string]bool{
		"Haus.des.Geldes.S01E01.German.1080p-GRP":      true,
		"La.Casa.de.Papel.S02E01.SPANISH.1080p-GRP":    true,
		"Haus.des.Geldes.2005.S01E01.German.1080p-GRP": false, // another year's show
		"Haus.des.Wahnsinns.S01E01.German.1080p-GRP":   false,
	} {
		got, err := s.KnownAs(ctx, heist, parser.Parse(release))
		if err != nil || got != want {
			t.Errorf("KnownAs(%q) = %v, %v; want %v", release, got, err, want)
		}
	}
	src.err = errors.New("tmdb: HTTP 503")
	if ok, err := s.KnownAs(ctx, heist, parser.Parse("Haus.des.Geldes.S01E01.German-GRP")); ok || err == nil {
		t.Errorf("TMDB down: %v, %v", ok, err)
	}
}

func localSeriesService(t *testing.T, meta metadata.SeriesProvider, root string, langs *[]string) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := NewService(st.DB(), meta, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetTitleLanguages(func() []string { return *langs })
	return s
}

// The backfill reads translated titles at its pace, only for shows not yet read for every
// language set now, keeping the rest of their extra metadata.
func TestBackfillLocalTitlesSeries(t *testing.T) {
	ctx := context.Background()
	langs := []string{"de"}
	s := localSeriesService(t, nil, t.TempDir(), &langs)
	src := &fakeTitles{titles: map[int]*metadata.Titles{}}
	s.SetTitlesSource(src)
	for id := 1; id <= 3; id++ {
		src.titles[id] = &metadata.Titles{Translations: []metadata.TranslatedTitle{{Lang: "de", Title: "Sendung " + string(rune('A'+id))}}}
		if _, err := s.repo.Create(ctx, Series{TMDBID: id, Title: "Show " + string(rune('A'+id)), Year: 2010 + id,
			Extra: &SeriesExtra{OriginCountry: []string{"US"}}}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	n, err := s.BackfillLocalTitles(ctx, 20*time.Millisecond)
	if err != nil || n != 3 || src.calls.Load() != 3 {
		t.Fatalf("filled %d (%v) with %d calls, want 3 and 3", n, err, src.calls.Load())
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Errorf("three shows took %v: the pace wasn't kept", time.Since(start))
	}
	got, err := s.GetByTMDB(ctx, 2)
	if err != nil || got.Extra == nil || len(got.Extra.LocalTitles) != 1 || len(got.Extra.OriginCountry) != 1 {
		t.Fatalf("extra = %+v, %v; want the German title stored and the origin kept", got.Extra, err)
	}
	if m, ok, _ := s.MatchRelease(ctx, parser.Parse("Sendung.C.S01E01.German.1080p-GRP")); !ok || m.ID != got.ID {
		t.Errorf("the stored German title doesn't route: %d %v", m.ID, ok)
	}
	if n, _ := s.BackfillLocalTitles(ctx, time.Millisecond); n != 0 || src.calls.Load() != 3 {
		t.Errorf("second run filled %d (%d calls), want nothing", n, src.calls.Load())
	}
	langs = nil
	if n, _ := s.BackfillLocalTitles(ctx, time.Millisecond); n != 0 {
		t.Errorf("the setting off: filled %d", n)
	}
}

// A library folder named in an extra title language is identified by a search in that
// language — only when the setting is on.
func TestSeriesScanFindsFolderNamedInExtraLanguage(t *testing.T) {
	for _, on := range []bool{false, true} {
		root := t.TempDir()
		meta := shelfMeta{shows: map[int]metadata.SeriesDetails{
			71446: {SeriesResult: metadata.SeriesResult{TMDBID: 71446, Title: "Money Heist", Year: 2017}, Seasons: listing(2)},
		}}
		langs := []string{}
		if on {
			langs = []string{"de"}
		}
		s := localSeriesService(t, meta, root, &langs)
		s.SetTitlesSource(&fakeTitles{hits: map[string][]metadata.LocalizedHit{
			"de": {{TMDBID: 71446, Title: "Haus des Geldes", Year: 2017}},
		}})
		touchEpisode(t, filepath.Join(root, "Haus des Geldes (2017)", "Season 01", "Haus.des.Geldes.S01E01.mkv"))
		res, err := s.ScanLibrary(context.Background(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if on && (res.Imported != 1 || len(res.Unmatched) != 0) {
			t.Errorf("German on: %+v, want the folder imported as Money Heist", res)
		}
		if !on && (res.Imported != 0 || len(res.Unmatched) != 1) {
			t.Errorf("off: %+v, want the folder left unmatched", res)
		}
	}
}
