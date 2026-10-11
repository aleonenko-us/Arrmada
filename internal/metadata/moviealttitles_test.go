package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// GetMovie asks for alternative titles in the same round trip and keeps the ones a release
// may be named by: US, GB and the film's own countries, Latin script, distinct by title key.
func TestParseAlternativeTitles(t *testing.T) {
	var appended string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/movie/101" {
			appended = r.URL.Query().Get("append_to_response")
			_, _ = w.Write([]byte(`{"id":101,"title":"Léon: The Professional","original_title":"Léon","release_date":"1994-09-14",
				"origin_country":["FR"],
				"alternative_titles":{"titles":[
					{"iso_3166_1":"US","title":"The Professional","type":""},
					{"iso_3166_1":"FR","title":"Léon version longue","type":"Extended"},
					{"iso_3166_1":"DE","title":"Leon - Der Profi","type":""},
					{"iso_3166_1":"GB","title":"Leon: The Professional","type":""},
					{"iso_3166_1":"US","title":"레옹","type":""},
					{"iso_3166_1":"US","title":"  ","type":""}]}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	tm := NewTMDB("k")
	tm.base = srv.URL

	d, err := tm.GetMovie(context.Background(), 101)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(appended, "alternative_titles") {
		t.Errorf("append_to_response = %q, want alternative_titles", appended)
	}
	if d.OriginalTitle != "Léon" {
		t.Errorf("OriginalTitle = %q", d.OriginalTitle)
	}
	// DE isn't one of the film's countries; the GB entry keys the same as the title; the
	// Korean one isn't Latin script.
	want := []string{"The Professional", "Léon version longue"}
	if strings.Join(d.AltTitles, "|") != strings.Join(want, "|") {
		t.Errorf("AltTitles = %q, want %q", d.AltTitles, want)
	}
}

func TestMovieAltTitlesDropNonLatinOriginalAndCap(t *testing.T) {
	m := tmdbMovie{Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し", OriginCountry: []string{"JP"}}
	for i := 0; i < 30; i++ {
		m.AlternativeTitles.Titles = append(m.AlternativeTitles.Titles, struct {
			Country string `json:"iso_3166_1"`
			Title   string `json:"title"`
			Type    string `json:"type"`
		}{Country: "US", Title: "Spirited Away " + strconv.Itoa(i)})
	}
	original, alts := movieAltTitles(m)
	if original != "" {
		t.Errorf("a non-Latin original title is dropped, got %q", original)
	}
	if len(alts) != maxMovieAltTitles {
		t.Errorf("kept %d alternative titles, want the cap %d", len(alts), maxMovieAltTitles)
	}
}
