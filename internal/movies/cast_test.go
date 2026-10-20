package movies

import (
	"context"
	"errors"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// castMeta answers GetMovie with a billed cast that carries TMDB person ids.
type castMeta struct{}

func (castMeta) Available() bool { return true }
func (castMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (castMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	title, year := "Heat", 1995
	if id != 949 {
		title, year = "Thief", 1981
	}
	return &metadata.MovieDetails{MovieResult: metadata.MovieResult{TMDBID: id, Title: title, Year: year}, Cast: []metadata.CastMember{
		{ID: 1158, Name: "Al Pacino", Character: "Vincent Hanna", ProfileURL: "/pacino.jpg"},
		{ID: 380, Name: "Robert De Niro", Character: "Neil McCauley"},
	}}, nil
}
func (castMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("no collections here")
}

// The library's Cast row links each actor to their Discover page, so the stored extra
// keeps the TMDB person id (#55); a film stored before ids were kept gets them on its
// next metadata refresh.
func TestStoredCastKeepsPersonIDs(t *testing.T) {
	ctx := context.Background()
	s := titlesService(t, castMeta{})
	old, err := s.repo.Create(ctx, Movie{TMDBID: 949, Title: "Heat", Year: 1995,
		Extra: &MovieExtra{Cast: []CastMember{{Name: "Al Pacino", Character: "Vincent Hanna"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, old.ID); got.Extra == nil || len(got.Extra.Cast) != 1 || got.Extra.Cast[0].ID != 0 {
		t.Fatalf("old record: %+v", got.Extra)
	}
	if _, err := s.Refresh(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []CastMember{
		{ID: 1158, Name: "Al Pacino", Character: "Vincent Hanna", ProfileURL: "/pacino.jpg"},
		{ID: 380, Name: "Robert De Niro", Character: "Neil McCauley"},
	}
	if got.Extra == nil || len(got.Extra.Cast) != len(want) {
		t.Fatalf("refreshed cast = %+v", got.Extra)
	}
	for i, c := range want {
		if got.Extra.Cast[i] != c {
			t.Errorf("cast[%d] = %+v, want %+v", i, got.Extra.Cast[i], c)
		}
	}

	// A film added now stores the ids straight away.
	added, err := s.AddWith(ctx, 500, AddOptions{QualityProfile: "hd"})
	if err != nil {
		t.Fatal(err)
	}
	if added, _ = s.Get(ctx, added.ID); added.Extra == nil || len(added.Extra.Cast) != 2 || added.Extra.Cast[0].ID != 1158 || added.Extra.Cast[1].ID != 380 {
		t.Errorf("added film's cast = %+v", added.Extra)
	}
}
