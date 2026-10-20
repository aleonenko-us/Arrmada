package music

import (
	"context"
	"log/slog"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// MUS-38: a release named by an alias is the artist's, but only as the release's whole
// artist part. An alias that is just part of another artist's name never lets that
// artist's release in.
func TestArtistNamesReleaseIsForAlbum(t *testing.T) {
	utada := ArtistNames{Name: "宇多田ヒカル", Aliases: []string{"Utada Hikaru", "Hikaru Utada"}}
	beyonce := ArtistNames{Name: "Beyoncé", Aliases: []string{"Beyoncé Knowles"}}
	kanye := ArtistNames{Name: "Kanye West", Aliases: []string{"Ye", "Yeezy"}}
	cases := []struct {
		names        ArtistNames
		release, alb string
		want         bool
		why          string
	}{
		{utada, "Utada Hikaru - First Love (1999) [FLAC]", "First Love", true, "the romanised alias as the artist part"},
		{utada, "Hikaru Utada - First Love", "First Love", true, "the other order"},
		{utada, "Utada_Hikaru-First_Love-2CD-FLAC-2004-GRP", "First Love", true, "a scene name's artist part"},
		{utada, "宇多田ヒカル - First Love", "First Love", true, "the name itself"},
		{utada, "Utada Hikaru Tribute Band - First Love", "First Love", false, "an alias inside another artist's name"},
		{utada, "The Best of Utada Hikaru - First Love", "First Love", false, "an alias as free words"},
		{utada, "Utada Hikaru - Distance", "First Love", false, "the album still has to be named"},
		{utada, "Utada Hikaru - Discography 1998-2010", "Discography", false, "a discography is never one album"},
		{beyonce, "Beyonce - Lemonade", "Lemonade", true, "accents fold on the name"},
		{beyonce, "Beyonce Knowles - Dangerously in Love", "Dangerously in Love", true, "the legal name"},
		{kanye, "Yeezy - Donda (2021)", "Donda", true, "a one-word alias as the whole artist part"},
		{kanye, "Ye - Donda (2021)", "Donda", false, "an alias too short to trust"},
		{kanye, "Yeezy Tribute - Donda", "Donda", false, "a one-word alias inside another name"},
		{kanye, "Ye Vagabonds - Donda", "Donda", false, "a one-word alias inside another band's name"},
		{kanye, "Donda by Ye", "Donda", false, "no artist part to compare"},
	}
	for _, c := range cases {
		if got := c.names.ReleaseIsForAlbum(c.release, c.alb); got != c.want {
			t.Errorf("%s: %q for %q = %v, want %v", c.why, c.release, c.alb, got, c.want)
		}
	}
	if !utada.Is("utada hikaru") || !utada.Is("宇多田ヒカル") || utada.Is("Utada Hikaru Tribute Band") {
		t.Error("Is should take the name or an alias, whole")
	}
}

// The index drops an alias that is another artist's name, or that two artists share, and
// orders the rest: primary first, Latin ones first for a name in another script. Search
// hints and names under three letters never match or become search queries.
func TestBuildNameIndex(t *testing.T) {
	ix := buildNameIndex(map[int64]string{1: "宇多田ヒカル", 2: "Prince", 3: "The Artist", 4: "Kanye West", 5: "Jay-Z"},
		[]aliasRow{
			{ArtistID: 1, Name: "Utada", Type: "Search hint"},
			{ArtistID: 1, Name: "Hikaru Utada", Type: "Artist name", Locale: "en"},
			{ArtistID: 1, Name: "Utada Hikaru", Type: "Artist name", Primary: true},
			{ArtistID: 2, Name: "The Artist", Type: "Artist name"}, // another artist's name
			{ArtistID: 2, Name: "Prince Rogers Nelson", Type: "Legal name"},
			{ArtistID: 2, Name: "TA", Type: "Artist name"}, // too short to match by
			{ArtistID: 4, Name: "Yeezy", Type: "Artist name"},
			{ArtistID: 5, Name: "Yeezy", Type: "Legal name"},  // shared: nobody gets it
			{ArtistID: 5, Name: "JAY Z", Type: "Artist name"}, // folds to the name itself
		})
	u := ix.Names(1, "")
	if len(u.Aliases) != 2 || u.Aliases[0] != "Utada Hikaru" || u.Aliases[1] != "Hikaru Utada" {
		t.Errorf("utada aliases = %v, want the primary first and no search hint", u.Aliases)
	}
	if len(u.Search) != 2 || u.Search[0] != "Utada Hikaru" {
		t.Errorf("utada search aliases = %v, want the two artist names, no search hint", u.Search)
	}
	if p := ix.Names(2, ""); len(p.Aliases) != 1 || p.Aliases[0] != "Prince Rogers Nelson" {
		t.Errorf("prince aliases = %v, want another artist's name left out", p.Aliases)
	}
	if k := ix.Names(4, ""); len(k.Aliases) != 0 {
		t.Errorf("kanye aliases = %v, want a shared alias left out", k.Aliases)
	}
	if j := ix.Names(5, ""); len(j.Aliases) != 0 {
		t.Errorf("jay-z aliases = %v, want an alias that is the name itself left out", j.Aliases)
	}
	if got := ix.ArtistsNamed("utada hikaru"); len(got) != 1 || got[0] != 1 {
		t.Errorf("ArtistsNamed(utada hikaru) = %v", got)
	}
	if got := ix.ArtistsNamed("The Artist"); len(got) != 1 || got[0] != 3 {
		t.Errorf("ArtistsNamed(The Artist) = %v, want only the artist of that name", got)
	}
	if got := ix.Names(99, "Unknown"); got.Name != "Unknown" || len(got.Aliases) != 0 {
		t.Errorf("an artist not in the index = %+v", got)
	}
}

// A tag naming the artist by an alias fits as well as the credit does.
func TestArtistDistUsesAliases(t *testing.T) {
	if d := artistDist("Utada Hikaru", "宇多田ヒカル", nil); d < 0.5 {
		t.Fatalf("without aliases the distance should be large, got %v", d)
	}
	if d := artistDist("Utada Hikaru", "宇多田ヒカル", []string{"Hikaru Utada", "Utada Hikaru"}); d != 0 {
		t.Errorf("with the alias the distance = %v, want 0", d)
	}
}

// aliasProvider answers one artist with the aliases set on it.
type aliasProvider struct {
	aliases []metadata.ArtistAlias
}

func (p *aliasProvider) Available() bool { return true }
func (p *aliasProvider) SearchArtists(context.Context, string) ([]metadata.ArtistResult, error) {
	return []metadata.ArtistResult{{MBID: "utada", Name: "宇多田ヒカル", Aliases: p.aliases}}, nil
}
func (p *aliasProvider) GetArtist(_ context.Context, mbid string) (*metadata.ArtistDetails, error) {
	return &metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: mbid, Name: "宇多田ヒカル", Aliases: p.aliases}}, nil
}
func (p *aliasProvider) ArtistAlbums(context.Context, string) ([]metadata.AlbumResult, error) {
	return []metadata.AlbumResult{{MBID: "rg-first-love", Title: "First Love", Type: "Album"}}, nil
}
func (p *aliasProvider) AlbumTracks(context.Context, string) ([]metadata.TrackResult, error) {
	return nil, nil
}

// Adding an artist stores its aliases; a refresh whose answer has none (a cached answer
// from before aliases were asked for) forgets nothing; the lookup says which alias a
// romanised query matched.
func TestAliasesStoredAndKept(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	p := &aliasProvider{aliases: []metadata.ArtistAlias{{Name: "Utada Hikaru", Type: "Artist name", Primary: true, Locale: "ja-Latn"}}}
	s := NewService(st.DB(), p, slog.Default())
	a, err := s.AddArtist(ctx, "utada", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.ArtistNames(ctx, a); len(n.Aliases) != 1 || n.Aliases[0] != "Utada Hikaru" {
		t.Fatalf("names after add = %+v", n)
	}
	p.aliases = nil
	if _, err := s.RefreshArtist(ctx, a.ID, RefreshOptions{}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.ArtistAliases(ctx, a.ID)
	if err != nil || len(stored) != 1 || stored[0].Locale != "ja-Latn" || !stored[0].Primary {
		t.Fatalf("aliases after an answer without any = %+v, %v; want them kept", stored, err)
	}
	res, err := s.Lookup(ctx, "utada hikaru")
	if err != nil || len(res) != 1 {
		t.Fatalf("lookup = %+v, %v", res, err)
	}
	p.aliases = []metadata.ArtistAlias{{Name: "Utada Hikaru"}}
	res, _ = s.Lookup(ctx, "utada hikaru")
	if res[0].AKA != "Utada Hikaru" {
		t.Errorf("lookup AKA = %q, want the alias the query matched", res[0].AKA)
	}
	if res, _ = s.Lookup(ctx, "宇多田"); res[0].AKA != "" {
		t.Errorf("lookup AKA = %q for a query the name matches, want none", res[0].AKA)
	}
}
