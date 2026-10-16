package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Titles reads every country's alternative titles and every language's translation in
// one request, for movies ("titles", data.title) and TV ("results", data.name) alike, and
// keeps the answer in the disk cache under its own key.
func TestTitlesReadsEveryNameInOneRequest(t *testing.T) {
	var calls atomic.Int32
	var appended string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		appended = r.URL.Query().Get("append_to_response")
		switch r.URL.Path {
		case "/movie/204082":
			_, _ = w.Write([]byte(`{"id":204082,"title":"Wild","original_title":"Wild","release_date":"2014-12-05",
				"alternative_titles":{"titles":[{"iso_3166_1":"DE","title":"Der große Trip","type":""},{"iso_3166_1":"BR","title":"Livre","type":""}]},
				"translations":{"translations":[
					{"iso_3166_1":"DE","iso_639_1":"de","data":{"title":"Der große Trip – Wild"}},
					{"iso_3166_1":"FR","iso_639_1":"fr","data":{"title":""}},
					{"iso_3166_1":"ES","iso_639_1":"es","data":{"title":"Alma salvaje"}}]}}`))
		case "/tv/71446":
			_, _ = w.Write([]byte(`{"id":71446,"name":"Money Heist","original_name":"La casa de papel","first_air_date":"2017-05-02",
				"alternative_titles":{"results":[{"iso_3166_1":"US","title":"Money Heist: Part 1","type":""}]},
				"translations":{"translations":[{"iso_3166_1":"DE","iso_639_1":"de","data":{"name":"Haus des Geldes"}}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tm := NewTMDB("k")
	tm.base = srv.URL
	tm.SetDiskCache(testDiskCache(t))
	ctx := context.Background()

	wild, err := tm.Titles(ctx, TitlesMovie, 204082)
	if err != nil {
		t.Fatal(err)
	}
	if appended != "alternative_titles,translations" {
		t.Errorf("append_to_response = %q, want only the titles", appended)
	}
	if wild.Title != "Wild" || wild.Year != 2014 || len(wild.Alternatives) != 2 || len(wild.Translations) != 2 {
		t.Fatalf("movie titles = %+v (an empty translation is left out)", wild)
	}
	if de := wild.InLanguages([]string{"de"}); len(de) != 1 || de[0] != (TranslatedTitle{Lang: "de", Country: "DE", Title: "Der große Trip – Wild"}) {
		t.Errorf("German = %+v", de)
	}
	all := strings.Join(wild.All(), "|")
	for _, want := range []string{"Wild", "Der große Trip", "Livre", "Der große Trip – Wild", "Alma salvaje"} {
		if !strings.Contains(all, want) {
			t.Errorf("All() = %q, missing %q", all, want)
		}
	}
	if _, err := tm.Titles(ctx, TitlesMovie, 204082); err != nil || calls.Load() != 1 {
		t.Errorf("second read: %v after %d requests, want it from the cache", err, calls.Load())
	}

	heist, err := tm.Titles(ctx, TitlesSeries, 71446)
	if err != nil {
		t.Fatal(err)
	}
	if heist.Title != "Money Heist" || heist.OriginalTitle != "La casa de papel" || heist.Year != 2017 ||
		len(heist.Alternatives) != 1 || len(heist.Translations) != 1 || heist.Translations[0].Title != "Haus des Geldes" {
		t.Errorf("series titles = %+v", heist)
	}
	if _, err := tm.Titles(ctx, "book", 1); err == nil {
		t.Error("an unknown media kind must be refused")
	}
}

// SearchLocalized asks for the results' titles in the language and drops adult titles.
func TestSearchLocalized(t *testing.T) {
	var lang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang = r.URL.Query().Get("language")
		_, _ = w.Write([]byte(`{"results":[{"id":238,"title":"Der Pate","release_date":"1972-03-14"},
			{"id":9,"title":"Der Pate XXX","adult":true,"release_date":"1999-01-01"}]}`))
	}))
	defer srv.Close()
	tm := NewTMDB("k")
	tm.base = srv.URL
	hits, err := tm.SearchLocalized(context.Background(), TitlesMovie, "Der Pate", "de")
	if err != nil {
		t.Fatal(err)
	}
	if lang != "de" || len(hits) != 1 || hits[0] != (LocalizedHit{TMDBID: 238, Title: "Der Pate", Year: 1972}) {
		t.Errorf("language %q, hits %+v", lang, hits)
	}
}

// fakeLocalized answers SearchLocalized from a table by language.
type fakeLocalized struct {
	byLang map[string][]LocalizedHit
}

func (f fakeLocalized) Titles(context.Context, string, int) (*Titles, error) { return nil, ErrNotFound }
func (f fakeLocalized) SearchLocalized(_ context.Context, _, _, lang string) ([]LocalizedHit, error) {
	return f.byLang[lang], nil
}

// A folder matches a localized title only when exactly one title is named so (with the
// year rule); a title shared by two films, or a year that doesn't fit, matches nothing.
func TestLocalizedMatch(t *testing.T) {
	ctx := context.Background()
	src := fakeLocalized{byLang: map[string][]LocalizedHit{
		"de": {{TMDBID: 238, Title: "Der Pate", Year: 1972}, {TMDBID: 5, Title: "Das Boot", Year: 1981}, {TMDBID: 6, Title: "Das Boot", Year: 1985}},
		"fr": {{TMDBID: 238, Title: "Le Parrain", Year: 1972}},
	}}
	langs := []string{"de", "fr"}
	cases := []struct {
		title string
		year  int
		want  int
	}{
		{"Der Pate", 1972, 238},
		{"Der Pate", 0, 238},
		{"Der Pate", 1990, 0}, // the year doesn't fit
		{"Das Boot", 0, 0},    // two films: no guess
		{"Das Boot", 1981, 5}, // the year picks one
		{"Le Parrain", 1972, 238},
		{"The Godfather", 1972, 0},
		{"Up", 0, 0}, // too short to stand for a title
	}
	for _, c := range cases {
		got, ok := LocalizedMatch(ctx, src, TitlesMovie, c.title, c.year, langs)
		if (c.want == 0) == ok || got != c.want {
			t.Errorf("LocalizedMatch(%q, %d) = %d, %v; want %d", c.title, c.year, got, ok, c.want)
		}
	}
	if _, ok := LocalizedMatch(ctx, src, TitlesMovie, "Der Pate", 1972, nil); ok {
		t.Error("no languages set must match nothing")
	}
}

func TestCoversLangs(t *testing.T) {
	if !CoversLangs([]string{"de", "fr"}, []string{"fr"}) || CoversLangs([]string{"de"}, []string{"de", "fr"}) || !CoversLangs(nil, nil) {
		t.Error("CoversLangs")
	}
}
