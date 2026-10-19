package series

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// overviewTitles is fakeTitles that also reads overviews in a language (TMDB's TitlesIn),
// recording the languages asked for.
type overviewTitles struct {
	*fakeTitles
	overviews []metadata.TranslatedOverview
	asked     []string
}

func (f *overviewTitles) TitlesIn(ctx context.Context, media string, id int, lang string) (*metadata.Titles, error) {
	f.asked = append(f.asked, lang)
	t, err := f.Titles(ctx, media, id)
	if err != nil {
		return nil, err
	}
	cp := *t
	cp.OverviewLang, cp.Overviews = lang, nil
	for _, o := range f.overviews {
		if o.Lang == lang {
			cp.Overviews = append(cp.Overviews, o)
		}
	}
	return &cp, nil
}

// GitHub #35 for shows: "My language: French" describes the show in French (Canada's
// French when that is the pick, France's otherwise), read by the backfill and kept by a
// refresh; English when TMDB has no overview in the language, and in the original mode.
func TestSeriesDisplayOverview(t *testing.T) {
	ctx := context.Background()
	meta := &fakeMeta{d: metadata.SeriesDetails{
		SeriesResult: metadata.SeriesResult{TMDBID: 61068, Title: "Gomorrah", Year: 2014, Overview: "Ciro and Genny fight for Naples."},
		OriginalName: "Gomorra - La serie", OriginalLang: "it",
		OriginCountry: []string{"IT"}, Seasons: listing(1),
	}}
	var langs []string
	s := localSeriesService(t, meta, t.TempDir(), &langs)
	src := &overviewTitles{
		fakeTitles: &fakeTitles{titles: map[int]*metadata.Titles{61068: {
			Title: "Gomorrah", OriginalTitle: "Gomorra - La serie", OriginalLanguage: "it",
		}}},
		overviews: []metadata.TranslatedOverview{
			{Lang: "fr", Country: "FR", Overview: "Ciro et Genny se battent pour Naples."},
			{Lang: "fr", Country: "CA", Overview: "Ciro et Genny luttent pour Naples."},
			{Lang: "it", Country: "IT", Overview: "Ciro e Genny lottano per Napoli."},
		},
	}
	s.SetTitlesSource(src)
	p := displaytitle.ParsePolicy("", "", "")
	s.SetDisplayPolicy(func() displaytitle.Policy { return p })
	sr, err := s.AddWith(ctx, 61068, "hd", AddOptions{Monitored: true, Preset: PresetAll})
	if err != nil {
		t.Fatal(err)
	}
	overview := func() (string, string) {
		got, err := s.Get(ctx, sr.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Overview, got.DisplayOverview
	}
	if _, shown := overview(); shown != "" {
		t.Errorf("default setting shows %q, want the English overview", shown)
	}

	p = displaytitle.ParsePolicy("language", "fr", "")
	if n, err := s.BackfillLocalTitles(ctx, time.Millisecond); err != nil || n != 1 {
		t.Fatalf("backfill filled %d (%v), want 1", n, err)
	}
	if len(src.asked) != 1 || src.asked[0] != "fr" {
		t.Errorf("TMDB asked for overviews in %v, want [fr]", src.asked)
	}
	if en, shown := overview(); shown != "Ciro et Genny se battent pour Naples." || en != "Ciro and Genny fight for Naples." {
		t.Errorf("French: overview %q, shown %q", en, shown)
	}
	// A refresh reads the names again with the overviews and keeps showing French.
	if _, _, err := s.Refresh(ctx, sr.ID, RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, shown := overview(); shown != "Ciro et Genny se battent pour Naples." {
		t.Errorf("after refresh: shown %q", shown)
	}
	// Canadian French prefers Canada's overview, from the names already stored.
	p = displaytitle.ParsePolicy("language", "fr-ca", "")
	if _, shown := overview(); shown != "Ciro et Genny luttent pour Naples." {
		t.Errorf("fr-CA: shown %q", shown)
	}
	// German: nothing in German is stored (or on TMDB), so English.
	p = displaytitle.ParsePolicy("language", "de", "")
	if _, shown := overview(); shown != "" {
		t.Errorf("German: shown %q, want the English overview", shown)
	}
	// The original mode keeps English descriptions, even for an Italian show.
	p = displaytitle.ParsePolicy("original", "", "")
	if _, shown := overview(); shown != "" {
		t.Errorf("original mode: shown %q, want the English overview", shown)
	}
}
