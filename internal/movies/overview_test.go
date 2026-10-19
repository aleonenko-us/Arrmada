package movies

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// overviewTitles is fakeTitles that can also read overviews in a language (TMDB's
// TitlesIn), recording the languages it was asked for.
type overviewTitles struct {
	*fakeTitles
	overviews map[int][]metadata.TranslatedOverview
	mu        sync.Mutex
	asked     []string
}

func (f *overviewTitles) TitlesIn(ctx context.Context, media string, id int, lang string) (*metadata.Titles, error) {
	f.mu.Lock()
	f.asked = append(f.asked, lang)
	f.mu.Unlock()
	t, err := f.Titles(ctx, media, id)
	if err != nil {
		return nil, err
	}
	cp := *t
	cp.OverviewLang = lang
	cp.Overviews = nil
	for _, o := range f.overviews[id] {
		if o.Lang == lang {
			cp.Overviews = append(cp.Overviews, o)
		}
	}
	return &cp, nil
}

// overviewFixture is a library of two films: Amélie, which TMDB describes in French, and
// Spirited Away, which it describes only in English and Japanese.
func overviewFixture(t *testing.T) (*Service, *overviewTitles, *displaytitle.Policy) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	meta := fakeMeta{films: []metadata.MovieResult{
		{TMDBID: 194, Title: "Amélie", Year: 2001, Overview: "A shy waitress decides to change the lives of those around her."},
		{TMDBID: 129, Title: "Spirited Away", Year: 2001, Overview: "A young girl wanders into a world ruled by gods."},
	}}
	s := NewService(st.DB(), meta, nil, t.TempDir(), "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src := &overviewTitles{
		fakeTitles: &fakeTitles{titles: map[int]*metadata.Titles{
			194: {Title: "Amélie", OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain", OriginalLanguage: "fr"},
			129: {Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し", OriginalLanguage: "ja",
				Translations: []metadata.TranslatedTitle{{Lang: "fr", Country: "FR", Title: "Le Voyage de Chihiro"}}},
		}},
		overviews: map[int][]metadata.TranslatedOverview{
			194: {{Lang: "fr", Country: "FR", Overview: "Amélie, une jeune serveuse, décide de changer la vie des autres."}},
			129: {{Lang: "ja", Country: "JP", Overview: "10歳の少女千尋は神々の世界に迷い込む。"}},
		},
	}
	s.SetTitlesSource(src)
	p := displaytitle.ParsePolicy("", "", "")
	s.SetDisplayPolicy(func() displaytitle.Policy { return p })
	return s, src, &p
}

// overviewsByTMDB is each film's overview and display overview as Get answers them.
func overviewsByTMDB(t *testing.T, s *Service) map[int][2]string {
	t.Helper()
	all, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[int][2]string{}
	for _, m := range all {
		got, err := s.Get(context.Background(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[m.TMDBID] = [2]string{got.Overview, got.DisplayOverview}
	}
	return out
}

// GitHub #35: "My language: French" describes a film in French when TMDB has its overview
// in French, and in English otherwise; the English overview stays the film's own. The
// backfill reads the overviews with the names (one request per film, asking TMDB for
// French), the English and original modes keep English descriptions, and so does a film
// whose names were read before overviews were kept, until the backfill reads it again.
func TestDisplayOverviewFollowsLanguage(t *testing.T) {
	ctx := context.Background()
	s, src, p := overviewFixture(t)
	for _, id := range []int{194, 129} {
		if _, err := s.AddWith(ctx, id, AddOptions{QualityProfile: "hd"}); err != nil {
			t.Fatal(err)
		}
	}
	// The default setting asks TMDB nothing and shows the English overview.
	if src.calls.Load() != 0 {
		t.Fatalf("the default setting asked TMDB %d times", src.calls.Load())
	}
	for id, o := range overviewsByTMDB(t, s) {
		if o[1] != "" {
			t.Errorf("film %d: display overview %q under the default setting", id, o[1])
		}
	}

	// Names read by the shipped display titles, before overviews were kept: covered for
	// titles, not for overviews.
	*p = displaytitle.ParsePolicy("language", "fr", "")
	all, _ := s.List(ctx)
	for _, m := range all {
		old := displaytitle.FromTitles(src.titles[m.TMDBID], p.TranslationLangs())
		if err := s.setDisplayNames(ctx, m.ID, old); err != nil {
			t.Fatal(err)
		}
	}
	if fresh, err := s.Get(ctx, all[0].ID); err != nil || !needsDisplayNames(fresh, *p) {
		t.Errorf("names read before overviews were kept count as covered (%v): the backfill would never read them", err)
	}
	for id, o := range overviewsByTMDB(t, s) {
		if o[1] != "" {
			t.Errorf("film %d: display overview %q before any overview was read", id, o[1])
		}
	}

	src.calls.Store(0)
	n, err := s.BackfillAltTitles(ctx, time.Millisecond)
	if err != nil || n != 2 || src.calls.Load() != 2 {
		t.Fatalf("backfill filled %d (%v) with %d calls, want 2 and 2", n, err, src.calls.Load())
	}
	for _, lang := range src.asked {
		if lang != "fr" {
			t.Errorf("titles read with overviews in %q, want fr", lang)
		}
	}
	got := overviewsByTMDB(t, s)
	if got[194] != [2]string{"A shy waitress decides to change the lives of those around her.", "Amélie, une jeune serveuse, décide de changer la vie des autres."} {
		t.Errorf("Amélie = %q, want the English overview kept and the French one shown", got[194])
	}
	if got[129][1] != "" || got[129][0] != "A young girl wanders into a world ruled by gods." {
		t.Errorf("Spirited Away = %q: no French overview must fall back to English", got[129])
	}
	// Read once: a second pass asks TMDB nothing.
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || src.calls.Load() != 2 {
		t.Errorf("second pass filled %d (%d calls), want nothing", n, src.calls.Load())
	}
	// Only the chosen language is stored.
	var blob string
	if err := s.repo.db.QueryRowContext(ctx, `SELECT extra_json FROM movies WHERE tmdb_id = 129`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "千尋は") {
		t.Errorf("a Japanese overview was stored for a French setting: %s", blob)
	}

	// Original titles keep English descriptions: a Japanese overview is unreadable to most.
	*p = displaytitle.ParsePolicy("original", "", "")
	for id, o := range overviewsByTMDB(t, s) {
		if o[1] != "" {
			t.Errorf("original mode: film %d shows %q, want the English overview", id, o[1])
		}
	}
	// "Original titles for French": titles only, overviews stay English.
	*p = displaytitle.ParsePolicy("english", "", "fr")
	if o := overviewsByTMDB(t, s)[194]; o[1] != "" {
		t.Errorf("original titles for French: Amélie shows %q, want English", o[1])
	}
	// Back to French: the stored overview shows again without asking TMDB.
	*p = displaytitle.ParsePolicy("language", "fr", "")
	before := src.calls.Load()
	if o := overviewsByTMDB(t, s)[194]; o[1] == "" || src.calls.Load() != before {
		t.Errorf("French again: %q after %d calls, want the stored French overview and no request", o[1], src.calls.Load()-before)
	}
}
