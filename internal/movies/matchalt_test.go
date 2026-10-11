package movies

import (
	"context"
	"testing"
)

// MOV-24: the import side matches a release named by a film's original or alternative
// title to that film, with the same year check — and an alternative title that is another
// library film's own title never takes that film's releases.
func TestMatchAltTitles(t *testing.T) {
	s := titlesService(t, nil)
	ctx := context.Background()
	leon, err := s.repo.Create(ctx, Movie{TMDBID: 101, Title: "Léon: The Professional", Year: 1994,
		Extra: &MovieExtra{OriginalTitle: "Léon", AltTitles: []string{"The Professional"}, TitlesRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want int64 // 0 = no match
	}{
		{"Leon.The.Professional.1994.1080p.BluRay.x264-GRP", leon.ID},
		{"Leon.1994.Extended.Cut.1080p.BluRay.x264-GRP", leon.ID},
		{"The.Professional.1994.720p.BluRay-GRP", leon.ID},
		{"The.Professional.1981.1080p.BluRay-GRP", 0}, // another film by that name
		{"Leon.2026.1080p.WEB-DL-GRP", 0},              // a remake
	}
	for _, c := range cases {
		m, ok := s.MatchRelease(ctx, c.name)
		if got := map[bool]int64{true: m.ID}[ok]; got != c.want {
			t.Errorf("MatchRelease(%q) = %d, want %d", c.name, got, c.want)
		}
	}
	// Matcher (the downloads feed's) agrees.
	all, _ := s.repo.List(ctx)
	if m, ok := s.Matcher(all)("The Professional", 1994); !ok || m.ID != leon.ID {
		t.Errorf("Matcher by alternative title = %v, %v", m.ID, ok)
	}
}

func TestMatchAltTitleCollisionIgnored(t *testing.T) {
	s := titlesService(t, nil)
	ctx := context.Background()
	dune, err := s.repo.Create(ctx, Movie{TMDBID: 438631, Title: "Dune", Year: 2021})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.repo.Create(ctx, Movie{TMDBID: 693134, Title: "Dune: Part Two", Year: 2021,
		Extra: &MovieExtra{AltTitles: []string{"Dune", "Dune Part 2"}, TitlesRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := s.MatchRelease(ctx, "Dune.2021.2160p.WEB-DL-GRP"); !ok || m.ID != dune.ID {
		t.Errorf("Dune's release matched %d (%v), want Dune", m.ID, ok)
	}
	// Year-less: Dune: Part Two's alternative "Dune" is dropped, so the title is not
	// ambiguous and goes to the film that is called that.
	if m, ok := s.Match(ctx, "Dune", 0); !ok || m.ID != dune.ID {
		t.Errorf("year-less Dune release matched %d (%v), want Dune", m.ID, ok)
	}
	if m, ok := s.MatchRelease(ctx, "Dune.Part.2.2021.1080p-GRP"); !ok || m.ID != two.ID {
		t.Errorf("its own alternative title matched %d (%v), want Dune: Part Two", m.ID, ok)
	}
	keys := TitleKeys(two, NewPrimaryKeys([]Movie{dune, two}))
	for _, k := range keys {
		if k == normalizeTitle("Dune") {
			t.Errorf("keys %v carry another film's own title", keys)
		}
	}
}

// Short and non-Latin alternatives never become keys; the own title always is.
func TestTitleKeysFilters(t *testing.T) {
	m := Movie{ID: 1, Title: "Up", Extra: &MovieExtra{OriginalTitle: "カールじいさんの空飛ぶ家", AltTitles: []string{"Oben", "UP", "Go"}}}
	keys := TitleKeys(m, nil)
	if len(keys) != 2 || keys[0] != "up" || keys[1] != normalizeTitle("Oben") {
		t.Errorf("TitleKeys = %v, want [up oben]", keys)
	}
}
