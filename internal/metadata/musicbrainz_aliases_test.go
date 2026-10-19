package metadata

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// MUS-38: the artist lookup asks for aliases in the same call (inc=aliases+genres) and
// keeps each alias's locale, sort name, type and primary flag; MusicBrainz's nulls read as
// empty, and blanks and repeats are dropped.
func TestGetArtistReadsAliases(t *testing.T) {
	var inc string
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		inc = r.URL.Query().Get("inc")
		_, _ = io.WriteString(w, `{"id":"utada","name":"宇多田ヒカル","sort-name":"Utada, Hikaru",
			"genres":[{"name":"j-pop","count":3}],
			"aliases":[
				{"name":"Utada Hikaru","sort-name":"Utada, Hikaru","locale":"ja-Latn","type":"Artist name","primary":true},
				{"name":"Hikaru Utada","sort-name":"Utada, Hikaru","locale":"en","type":"Artist name","primary":null},
				{"name":"Utada","sort-name":"Utada","locale":null,"type":null,"primary":null},
				{"name":"  ","sort-name":"","locale":null,"type":null,"primary":null},
				{"name":"Utada","sort-name":"Utada","locale":null,"type":"Search hint","primary":false}
			]}`)
	})
	d, err := m.GetArtist(context.Background(), "utada")
	if err != nil {
		t.Fatal(err)
	}
	if inc != "aliases genres" {
		t.Errorf("inc = %q, want the aliases asked for with the genres", inc)
	}
	if len(d.Genres) != 1 || d.Genres[0] != "j-pop" {
		t.Errorf("genres = %v", d.Genres)
	}
	want := []ArtistAlias{
		{Name: "Utada Hikaru", SortName: "Utada, Hikaru", Locale: "ja-Latn", Type: "Artist name", Primary: true},
		{Name: "Hikaru Utada", SortName: "Utada, Hikaru", Locale: "en", Type: "Artist name"},
		{Name: "Utada", SortName: "Utada"},
	}
	if len(d.Aliases) != len(want) {
		t.Fatalf("aliases = %+v, want %+v", d.Aliases, want)
	}
	for i := range want {
		if d.Aliases[i] != want[i] {
			t.Errorf("alias %d = %+v, want %+v", i, d.Aliases[i], want[i])
		}
	}
}

// Search results carry their aliases too, which is how the add dialog can say which alias
// a romanised query found.
func TestSearchArtistsReadsAliases(t *testing.T) {
	m := mbStub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"artists":[{"id":"utada","name":"宇多田ヒカル","sort-name":"Utada, Hikaru",
			"aliases":[{"name":"Utada Hikaru","sort-name":"Utada, Hikaru","locale":null,"type":null,"primary":null}]}]}`)
	})
	res, err := m.SearchArtists(context.Background(), "utada hikaru")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || len(res[0].Aliases) != 1 || res[0].Aliases[0].Name != "Utada Hikaru" {
		t.Fatalf("results = %+v", res)
	}
}
