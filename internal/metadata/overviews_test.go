package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// amelieBody is a movie detail body with the titles and translations appended: French has
// no title of its own (the film is French) but has an overview; Japanese has both.
const amelieBody = `{"id":194,"title":"Amélie","original_title":"Le Fabuleux Destin d'Amélie Poulain","original_language":"fr",
	"release_date":"2001-04-25","overview":"A shy waitress changes lives.",
	"alternative_titles":{"titles":[]},
	"translations":{"translations":[
		{"iso_3166_1":"FR","iso_639_1":"fr","data":{"title":"","overview":"Amélie, une serveuse, change des vies."}},
		{"iso_3166_1":"CA","iso_639_1":"fr","data":{"title":"","overview":"Amélie change la vie des gens."}},
		{"iso_3166_1":"JP","iso_639_1":"ja","data":{"title":"アメリ","overview":"内気なウェイトレス。"}},
		{"iso_3166_1":"US","iso_639_1":"en","data":{"title":"Amélie","overview":"A shy waitress changes lives."}},
		{"iso_3166_1":"DE","iso_639_1":"de","data":{"title":"Die fabelhafte Welt der Amélie","overview":""}}]}}`

// TitlesIn reads a title's overviews in one language from the same request as its names,
// under a cache entry of its own, keeping only that language; plain Titles reads and
// keeps none, so nothing changes for installs that don't describe titles in a language.
func TestTitlesInKeepsOneLanguagesOverviews(t *testing.T) {
	var calls atomic.Int32
	var appended string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		appended = r.URL.Query().Get("append_to_response")
		_, _ = w.Write([]byte(amelieBody))
	}))
	defer srv.Close()
	tm := NewTMDB("k")
	tm.base = srv.URL
	tm.SetDiskCache(testDiskCache(t))
	ctx := context.Background()

	plain, err := tm.Titles(ctx, TitlesMovie, 194)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Overviews) != 0 || plain.OverviewLang != "" {
		t.Errorf("Titles kept overviews: %+v", plain.Overviews)
	}
	fr, err := tm.TitlesIn(ctx, TitlesMovie, 194, "FR")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || appended != "alternative_titles,translations" {
		t.Errorf("%d requests appending %q: the French overviews need their own entry, from the same request", calls.Load(), appended)
	}
	if fr.OverviewLang != "fr" || len(fr.Overviews) != 2 ||
		fr.Overviews[0] != (TranslatedOverview{Lang: "fr", Country: "FR", Overview: "Amélie, une serveuse, change des vies."}) ||
		fr.Overviews[1].Country != "CA" {
		t.Errorf("French overviews = %+v (an empty French title still has its overview)", fr.Overviews)
	}
	if len(fr.Translations) != 3 { // ja, en and de; French's empty title is left out as before
		t.Errorf("translations = %+v", fr.Translations)
	}
	if _, err := tm.TitlesIn(ctx, TitlesMovie, 194, "fr"); err != nil || calls.Load() != 2 {
		t.Errorf("second French read: %v after %d requests, want it from the cache", err, calls.Load())
	}
	ja, err := tm.TitlesIn(ctx, TitlesMovie, 194, "ja")
	if err != nil || len(ja.Overviews) != 1 || ja.Overviews[0].Lang != "ja" {
		t.Errorf("Japanese = %+v, %v", ja, err)
	}
	en, err := tm.TitlesIn(ctx, TitlesMovie, 194, "de")
	if err != nil || len(en.Overviews) != 0 {
		t.Errorf("German has a blank overview: %+v, %v", en, err)
	}
}

// plainTitles is a TitlesSource that can't read overviews, answering one shared record.
type plainTitles struct{ t *Titles }

func (p plainTitles) Titles(context.Context, string, int) (*Titles, error) { return p.t, nil }
func (p plainTitles) SearchLocalized(context.Context, string, string, string) ([]LocalizedHit, error) {
	return nil, nil
}

// WithOverviews routes every Titles read through TitlesIn in the language, marks a plain
// source's answers as read for it, and leaves the source as it is with no language.
func TestWithOverviews(t *testing.T) {
	tm := NewTMDB("k")
	if got := WithOverviews(tm, ""); got != TitlesSource(tm) {
		t.Error("no language must leave the source as it is")
	}
	// A source that can't read overviews answers as read for the language, with none, on a
	// copy of its own record.
	shared := &Titles{Title: "Amélie"}
	plain := plainTitles{t: shared}
	got0, err := WithOverviews(plain, "fr").Titles(context.Background(), TitlesMovie, 194)
	if err != nil || got0.OverviewLang != "fr" || len(got0.Overviews) != 0 || got0.Title != "Amélie" || shared.OverviewLang != "" {
		t.Errorf("plain source = %+v, %v (its own record now %+v)", got0, err, shared)
	}
	if WithOverviews(nil, "fr") != nil {
		t.Error("no source stays no source")
	}
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		_, _ = w.Write([]byte(amelieBody))
	}))
	defer srv.Close()
	tm.base = srv.URL
	got, err := WithOverviews(tm, "fr").Titles(context.Background(), TitlesMovie, 194)
	if err != nil || got.OverviewLang != "fr" || len(got.Overviews) != 2 || asked != "/movie/194" {
		t.Errorf("wrapped read = %+v, %v (path %q)", got, err, asked)
	}
}

// The Discover detail record carries every other language's overview from the same
// request (translations appended), English left out.
func TestMediaDetailCarriesOverviews(t *testing.T) {
	tm, seen := fakeTMDB(t, amelieBody)
	d, err := tm.MediaDetails(context.Background(), "movie", 194)
	if err != nil {
		t.Fatal(err)
	}
	if q := seen(); len(q) != 1 || !strings.HasSuffix(q[0].Query().Get("append_to_response"), ",translations") {
		t.Errorf("requests = %v, want one with translations appended", q)
	}
	if len(d.Overviews) != 3 {
		t.Fatalf("overviews = %+v, want fr-FR, fr-CA and ja (no English, no blank German)", d.Overviews)
	}
	for _, o := range d.Overviews {
		if o.Lang == "en" || o.Lang == "de" {
			t.Errorf("kept %+v", o)
		}
	}
	tv, seenTV := fakeTMDB(t, `{"id":61068,"name":"Gomorrah","first_air_date":"2014-05-06","overview":"Naples.",
		"translations":{"translations":[{"iso_3166_1":"FR","iso_639_1":"fr","data":{"name":"Gomorra","overview":"Naples, en français."}}]}}`)
	ds, err := tv.MediaDetails(context.Background(), "series", 61068)
	if err != nil || len(ds.Overviews) != 1 || ds.Overviews[0].Overview != "Naples, en français." {
		t.Errorf("series overviews = %+v, %v", ds, err)
	}
	if q := seenTV(); len(q) != 1 || !strings.HasSuffix(q[0].Query().Get("append_to_response"), ",translations") {
		t.Errorf("series requests = %v", q)
	}
}

// TrendingOverviews asks TMDB for the trending list in the language and maps the
// overviews by card key; a title with no overview in it is left out.
func TestTrendingOverviewsCarriesLanguage(t *testing.T) {
	tm, seen := fakeTMDB(t, `{"page":1,"results":[
		{"id":194,"media_type":"movie","title":"Le Fabuleux Destin d'Amélie Poulain","overview":"Amélie, une serveuse.","poster_path":"/a.jpg","vote_count":900,"release_date":"2001-04-25"},
		{"id":61068,"media_type":"tv","name":"Gomorra","overview":"","poster_path":"/g.jpg","vote_count":900,"first_air_date":"2014-05-06"}]}`)
	got, err := tm.TrendingOverviews(context.Background(), "all", "fr-CA")
	if err != nil {
		t.Fatal(err)
	}
	if got["movie:194"] != "Amélie, une serveuse." || len(got) != 1 {
		t.Errorf("overviews = %v", got)
	}
	found := false
	for _, u := range seen() {
		if u.Path == "/trending/all/week" {
			found = true
			if u.Query().Get("language") != "fr-CA" {
				t.Errorf("trending asked in %q, want fr-CA", u.Query().Get("language"))
			}
		}
	}
	if !found {
		t.Errorf("no trending request in %v", seen())
	}
}
