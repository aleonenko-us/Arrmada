package movies

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// fakeTitles is TMDB's every-name lookup and localized search from tables, counting calls.
type fakeTitles struct {
	mu     sync.Mutex
	titles map[int]*metadata.Titles
	hits   map[string][]metadata.LocalizedHit // by language
	err    error
	calls  atomic.Int32
	// entered, when set, is signalled on the first Titles call, which then waits for
	// release: a backfill held mid-pass.
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *fakeTitles) Titles(_ context.Context, _ string, id int) (*metadata.Titles, error) {
	f.calls.Add(1)
	if f.entered != nil {
		f.once.Do(func() {
			close(f.entered)
			<-f.release
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	t, ok := f.titles[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	return t, nil
}

func (f *fakeTitles) SearchLocalized(_ context.Context, _, _, lang string) ([]metadata.LocalizedHit, error) {
	return f.hits[lang], nil
}

// wildTitles is TMDB's names for Wild (2014): a German alternative title, a German and a
// Spanish translation.
func wildTitles() *metadata.Titles {
	return &metadata.Titles{
		Title: "Wild", OriginalTitle: "Wild", Year: 2014,
		Alternatives: []metadata.AltTitle{{Title: "Der große Trip", Country: "DE"}},
		Translations: []metadata.TranslatedTitle{
			{Lang: "de", Country: "DE", Title: "Der große Trip – Wild"},
			{Lang: "es", Country: "ES", Title: "Alma salvaje"},
		},
	}
}

// KnownAs: a download named by any of the film's names, in any language, is the film —
// with the year guard, and an error (not a guess) when the names can't be read.
func TestKnownAs(t *testing.T) {
	ctx := context.Background()
	s := titlesService(t, nil)
	src := &fakeTitles{titles: map[int]*metadata.Titles{204082: wildTitles()}}
	wild := Movie{ID: 1, TMDBID: 204082, Title: "Wild", Year: 2014}

	if _, err := s.KnownAs(ctx, wild, "Der grosse Trip Wild", 2014); !errors.Is(err, ErrNoTitles) {
		t.Errorf("no titles source: err = %v, want ErrNoTitles", err)
	}
	s.SetTitlesSource(src)
	cases := []struct {
		title string
		year  int
		want  bool
	}{
		{"Der grosse Trip Wild", 2014, true}, // the German translation, ß folded
		{"Der grosse Trip", 2015, true},      // the German alternative title, a year off
		{"Alma salvaje", 0, true},            // the Spanish translation, no year
		{"Der grosse Trip Wild", 2010, false},
		{"Das Boot", 2014, false}, // another film's name
		{"Wi", 2014, false},       // too short to stand for a film
	}
	for _, c := range cases {
		got, err := s.KnownAs(ctx, wild, c.title, c.year)
		if err != nil || got != c.want {
			t.Errorf("KnownAs(%q, %d) = %v, %v; want %v", c.title, c.year, got, err, c.want)
		}
	}

	// A title stored for the extra languages answers without asking TMDB.
	stored := wild
	stored.Extra = &MovieExtra{LocalTitles: []metadata.TranslatedTitle{{Lang: "de", Title: "Der große Trip – Wild"}}}
	src.err = errors.New("tmdb: HTTP 503")
	if ok, err := s.KnownAs(ctx, stored, "Der.grosse.Trip.Wild", 2014); !ok || err != nil {
		t.Errorf("stored title: %v, %v", ok, err)
	}
	if ok, err := s.KnownAs(ctx, wild, "Der grosse Trip Wild", 2014); ok || err == nil {
		t.Errorf("TMDB down: %v, %v; want false with the error", ok, err)
	}
}

// localService is a service whose extra title languages are *langs.
func localService(t *testing.T, langs *[]string) *Service {
	t.Helper()
	s := titlesService(t, nil)
	s.SetTitleLanguages(func() []string { return *langs })
	return s
}

func create(t *testing.T, s *Service, m Movie) Movie {
	t.Helper()
	got, err := s.repo.Create(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func german(titles ...string) *MovieExtra {
	ex := &MovieExtra{TitlesRead: true, LocalLangs: []string{"de"}}
	for _, title := range titles {
		ex.LocalTitles = append(ex.LocalTitles, metadata.TranslatedTitle{Lang: "de", Country: "DE", Title: title})
	}
	return ex
}

// The extra title languages: off, nothing changes; on, a film's translated title matches
// it library-wide — but never a name another film goes by, and never one two films share.
func TestMatchTranslatedTitles(t *testing.T) {
	ctx := context.Background()
	langs := []string{}
	s := localService(t, &langs)
	godfather := create(t, s, Movie{TMDBID: 238, Title: "The Godfather", Year: 1972, Extra: german("Der Pate")})
	create(t, s, Movie{TMDBID: 949, Title: "Heat", Year: 1995, Extra: &MovieExtra{TitlesRead: true}})
	create(t, s, Movie{TMDBID: 1, Title: "Hitze", Year: 2010, Extra: german("Heat")}) // its German title is another film's own
	leben := create(t, s, Movie{TMDBID: 2, Title: "Leben", Year: 2017, Extra: &MovieExtra{TitlesRead: true, AltTitles: []string{"Life Again"}}})
	create(t, s, Movie{TMDBID: 3, Title: "Another Life", Year: 2017, Extra: german("Life Again")}) // another film's alternative title
	boatA := create(t, s, Movie{TMDBID: 4, Title: "The Boat", Year: 1981, Extra: german("Das Boot")})
	create(t, s, Movie{TMDBID: 5, Title: "Boat People", Year: 1985, Extra: german("Das Boot")})

	type tc struct {
		title string
		year  int
		want  int64 // 0 = no match
	}
	check := func(label string, cases []tc) {
		t.Helper()
		all, _ := s.repo.List(ctx)
		matcher := s.Matcher(all)
		for _, c := range cases {
			m, ok := s.Match(ctx, c.title, c.year)
			if (c.want == 0) == ok || m.ID != c.want {
				t.Errorf("%s: Match(%q, %d) = %d (%v), want %d", label, c.title, c.year, m.ID, ok, c.want)
			}
			m, ok = matcher(c.title, c.year)
			if (c.want == 0) == ok || m.ID != c.want {
				t.Errorf("%s: Matcher(%q, %d) = %d (%v), want %d", label, c.title, c.year, m.ID, ok, c.want)
			}
		}
	}

	check("off", []tc{{"Der Pate", 1972, 0}, {"The Godfather", 1972, godfather.ID}})

	langs = []string{"fr"}
	check("another language", []tc{{"Der Pate", 1972, 0}})

	langs = []string{"de"}
	check("German", []tc{
		{"Der Pate", 1972, godfather.ID},
		{"Der.Pate", 0, godfather.ID},
		{"Der Pate", 1990, 0},          // the year guard
		{"Heat", 2010, 0},              // Heat (1995)'s own title: never Hitze's German one
		{"Life Again", 2017, leben.ID}, // another film's alternative title stays that film's
		{"Das Boot", 0, 0},             // two films share it: no guess
		{"Das Boot", 1981, boatA.ID},   // the year picks one
		{"The Godfather", 1972, godfather.ID},
	})
}

// The backfill reads translated titles at its pace, only for films not yet read for every
// language set now; a language taken off needs no new read.
func TestBackfillLocalTitles(t *testing.T) {
	ctx := context.Background()
	langs := []string{"de"}
	s := localService(t, &langs)
	s.meta = &titlesMeta{}
	src := &fakeTitles{titles: map[int]*metadata.Titles{}}
	s.SetTitlesSource(src)
	for id := 1; id <= 3; id++ {
		src.titles[id] = &metadata.Titles{Translations: []metadata.TranslatedTitle{
			{Lang: "de", Title: "Deutsch " + itoa(id)}, {Lang: "fr", Title: "Français " + itoa(id)},
		}}
		create(t, s, Movie{TMDBID: id, Title: "Film " + itoa(id), Year: 2000 + id, Extra: &MovieExtra{TitlesRead: true}})
	}
	create(t, s, Movie{TMDBID: 99, Title: "Gone", Year: 2001, Extra: &MovieExtra{TitlesRead: true}}) // TMDB no longer has it

	start := time.Now()
	n, err := s.BackfillAltTitles(ctx, 20*time.Millisecond)
	if err != nil || n != 4 || src.calls.Load() != 4 {
		t.Fatalf("filled %d (%v) with %d calls, want 4 and 4", n, err, src.calls.Load())
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Errorf("four films took %v: the pace wasn't kept", time.Since(start))
	}
	got, _ := s.GetByTMDB(ctx, 2)
	if got.Extra == nil || len(got.Extra.LocalTitles) != 1 || got.Extra.LocalTitles[0].Title != "Deutsch C" ||
		len(got.Extra.LocalLangs) != 1 || !got.Extra.TitlesRead {
		t.Errorf("extra = %+v, want the German title stored and the rest kept", got.Extra)
	}
	if m, ok := s.Match(ctx, "Deutsch C", 2002); !ok || m.ID != got.ID {
		t.Errorf("the stored German title doesn't match: %v %v", m.ID, ok)
	}
	if gone, _ := s.GetByTMDB(ctx, 99); gone.Extra == nil || !metadata.CoversLangs(gone.Extra.LocalLangs, langs) {
		t.Errorf("a film TMDB no longer has is marked read: %+v", gone.Extra)
	}

	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || src.calls.Load() != 4 {
		t.Errorf("second run filled %d (%d calls), want nothing", n, src.calls.Load())
	}
	langs = []string{"de", "fr"}
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 4 || src.calls.Load() != 8 {
		t.Errorf("a new language: filled %d (%d calls), want every film read again", n, src.calls.Load())
	}
	langs = []string{"fr"}
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 {
		t.Errorf("a language taken off needs no read, filled %d", n)
	}
	if _, ok := s.Match(ctx, "Deutsch C", 2002); ok {
		t.Error("German titles still match after German was taken off")
	}
	if m, ok := s.Match(ctx, "Français C", 2002); !ok || m.ID != got.ID {
		t.Error("the French title doesn't match")
	}
}

// A backfill asked for while one runs doesn't start a second: it returns at once, and the
// running one goes round again so the languages saved meanwhile are read too.
func TestBackfillLocalTitlesOneAtATime(t *testing.T) {
	ctx := context.Background()
	langs := []string{"de"}
	var mu sync.Mutex
	s := titlesService(t, nil)
	s.SetTitleLanguages(func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), langs...)
	})
	s.meta = &titlesMeta{}
	src := &fakeTitles{titles: map[int]*metadata.Titles{}, entered: make(chan struct{}), release: make(chan struct{})}
	s.SetTitlesSource(src)
	for id := 1; id <= 2; id++ {
		src.titles[id] = &metadata.Titles{Translations: []metadata.TranslatedTitle{{Lang: "de", Title: "Deutsch " + itoa(id)}, {Lang: "fr", Title: "Français " + itoa(id)}}}
		create(t, s, Movie{TMDBID: id, Title: "Film " + itoa(id), Year: 2000 + id, Extra: &MovieExtra{TitlesRead: true}})
	}
	done := make(chan int)
	go func() {
		n, _ := s.BackfillAltTitles(ctx, time.Millisecond)
		done <- n
	}()
	<-src.entered // the first pass is mid-way
	mu.Lock()
	langs = []string{"de", "fr"}
	mu.Unlock()
	if n, err := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || err != nil {
		t.Errorf("a second backfill ran alongside: %d, %v", n, err)
	}
	close(src.release)
	<-done
	for id := 1; id <= 2; id++ {
		m, _ := s.GetByTMDB(ctx, id)
		if m.Extra == nil || !metadata.CoversLangs(m.Extra.LocalLangs, []string{"de", "fr"}) {
			t.Errorf("film %d read for %v, want the languages saved mid-pass too", id, m.Extra.LocalLangs)
		}
	}
}

// A library folder named in an extra title language is identified by a search in that
// language — and stays unmatched when the setting is off.
func TestScanFindsFolderNamedInExtraLanguage(t *testing.T) {
	for _, on := range []bool{false, true} {
		svc, _, _ := scanFixture(t, "Der Pate (1972)/Der.Pate.1972.1080p.mkv")
		svc.meta = fakeMeta{films: []metadata.MovieResult{{TMDBID: 238, Title: "The Godfather", Year: 1972}}}
		langs := []string{}
		if on {
			langs = []string{"de"}
		}
		svc.SetTitleLanguages(func() []string { return langs })
		svc.SetTitlesSource(&fakeTitles{hits: map[string][]metadata.LocalizedHit{
			"de": {{TMDBID: 238, Title: "Der Pate", Year: 1972}},
		}})
		res, err := svc.ScanLibrary(context.Background(), "", ScanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if on && (res.Imported != 1 || len(res.Unmatched) != 0) {
			t.Errorf("German on: %+v, want the folder imported as The Godfather", res)
		}
		if !on && (res.Imported != 0 || len(res.Unmatched) != 1) {
			t.Errorf("off: %+v, want the folder left unmatched", res)
		}
		if on {
			if m, err := svc.GetByTMDB(context.Background(), 238); err != nil || m.Title != "The Godfather" {
				t.Errorf("imported %+v, %v", m, err)
			}
		}
	}
}
