package series

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
)

const heistDE = "Haus.des.Geldes.S01E01.German.DL.1080p.WEB.x264-GRP"

// searchLibrary is a library where the translated tier has every case to get wrong: a show
// with a German title of its own, a show whose German title is another show's own title, two
// shows translated alike, and a show translated as one short word.
func searchLibrary() []Series {
	heist := germanShow(1, "Money Heist", 2017, "Haus des Geldes")
	heist.Extra.OriginCountry = []string{"ES"}
	haus := germanShow(2, "Haus", 2004)
	other := germanShow(3, "Other House", 2010, "Haus") // its German title is another show's own
	bridgeUS := germanShow(4, "The Bridge", 2013, "Die Brücke")
	bridgeDK := germanShow(5, "Bron", 2011, "Die Brücke")
	wolf := germanShow(6, "The Wolf Pack", 2023, "Wolf") // one short word
	wolf.Extra.OriginCountry = []string{"AU"}
	return []Series{heist, haus, other, bridgeUS, bridgeDK, wolf}
}

func byID(all []Series, id int64) Series {
	for _, s := range all {
		if s.ID == id {
			return s
		}
	}
	panic("no show")
}

// With "Also match titles in" empty a search's rule is FitRelease exactly — for every show
// and every release, translated names included.
func TestSearchFitOffIsFitRelease(t *testing.T) {
	all := searchLibrary()
	if f := newSearchFit(all, nil); f != nil {
		t.Fatal("the setting empty made a SearchFit")
	}
	var off *SearchFit
	releases := []string{heistDE, "Money.Heist.S01E01.1080p-GRP", "Money.Heist.2005.S01E01-GRP",
		"Haus.S01E01-GRP", "Die.Brucke.S01E01.German-GRP", "Wolf.2023.S01E01.German-GRP", "The.Bridge.US.S01E01-GRP"}
	for _, s := range all {
		for _, r := range releases {
			p := parser.Parse(r)
			if got, want := off.Fit(p, s), FitRelease(p, s); got != want {
				t.Errorf("%s / %s: %+v, FitRelease %+v", s.Title, r, got, want)
			}
		}
		if q := off.QueryTitle(s); q != "" {
			t.Errorf("%s: query %q with the setting off", s.Title, q)
		}
	}
	// A library already read in German changes nothing either: the setting decides.
	s := (&Service{}).SearchFit(context.Background())
	if s != nil {
		t.Error("a service with no title languages made a SearchFit")
	}
}

func TestSearchFitTranslatedTitles(t *testing.T) {
	all := searchLibrary()
	fit := newSearchFit(all, []string{"de"})
	judge := func(release string, id int64) ReleaseFit { return fit.Fit(parser.Parse(release), byID(all, id)) }

	t.Run("the German release is the show's", func(t *testing.T) {
		if p := parser.Parse(heistDE); p.Title != "Haus des Geldes" || p.Season != 1 {
			t.Fatalf("parsed %+v", p)
		}
		f := judge(heistDE, 1)
		if !f.OK || !f.Local || f.Alias {
			t.Fatalf("Money Heist's German release: %+v", f)
		}
		if f := judge("Haus.des.Geldes.2017.S02.German.DL.1080p.BluRay.x264-GRP", 1); !f.OK || !f.Year {
			t.Errorf("with the show's year: %+v", f)
		}
		// Its own title is judged as always.
		if f := judge("Money.Heist.S01E01.1080p-GRP", 1); !f.OK || f.Local {
			t.Errorf("own title: %+v", f)
		}
	})

	t.Run("never another show's", func(t *testing.T) {
		for _, id := range []int64{2, 3, 4, 6} {
			if f := judge(heistDE, id); f.OK || f.Local {
				t.Errorf("%s took Money Heist's German release: %+v", byID(all, id).Title, f)
			}
		}
	})

	t.Run("a name a library show goes by", func(t *testing.T) {
		// Other House is "Haus" in German, which is another library show's own title: a
		// "Haus" release is that show's in any year, never Other House's.
		for _, r := range []string{"Haus.S01E01.German-GRP", "Haus.2010.S01E01.German-GRP"} {
			if f := judge(r, 3); f.OK {
				t.Errorf("%s went to Other House: %+v", r, f)
			}
		}
		if f := judge("Haus.S01E01.German-GRP", 2); !f.OK || f.Local {
			t.Errorf("Haus's own release: %+v", f)
		}
	})

	t.Run("a title two shows share matches neither", func(t *testing.T) {
		for _, r := range []string{"Die.Brucke.S01E01.German.DL.1080p-GRP", "Die.Brucke.2013.S01E01.German-GRP"} {
			for _, id := range []int64{4, 5} {
				f := judge(r, id)
				if f.OK || !strings.Contains(f.Why, "another show") {
					t.Errorf("%s for %s: %+v", r, byID(all, id).Title, f)
				}
			}
		}
	})

	t.Run("year and country must fit", func(t *testing.T) {
		if f := judge("Haus.des.Geldes.1990.S01E01.German-GRP", 1); f.OK || f.Why == "" {
			t.Errorf("another year: %+v", f)
		}
		if f := judge("Haus.des.Geldes.US.S01E01.German-GRP", 1); f.OK {
			t.Errorf("tagged US for a Spanish show: %+v", f)
		}
		// An unknown year or origin can't be checked, so a release that names one isn't taken.
		noYear := byID(all, 1)
		noYear.Year, noYear.Extra = 0, &SeriesExtra{LocalLangs: []string{"de"}, LocalTitles: noYear.Extra.LocalTitles}
		lib := append([]Series{noYear}, all[1:]...)
		f := newSearchFit(lib, []string{"de"})
		if got := f.Fit(parser.Parse("Haus.des.Geldes.2017.S01E01.German-GRP"), noYear); got.OK {
			t.Errorf("a year against an unknown one: %+v", got)
		}
		if got := f.Fit(parser.Parse("Haus.des.Geldes.US.S01E01.German-GRP"), noYear); got.OK {
			t.Errorf("a country against an unknown origin: %+v", got)
		}
		if got := f.Fit(parser.Parse(heistDE), noYear); !got.OK {
			t.Errorf("neither named: %+v", got)
		}
	})

	t.Run("one short word needs a year or country", func(t *testing.T) {
		if f := judge("Wolf.S01E01.German.DL.1080p-GRP", 6); f.OK || !strings.Contains(f.Why, "common") {
			t.Errorf("a bare short word: %+v", f)
		}
		if f := judge("Wolf.2023.S01E01.German.DL.1080p-GRP", 6); !f.OK || !f.Year {
			t.Errorf("with the show's year: %+v", f)
		}
		if f := judge("Wolf.AU.S01E01.German.DL.1080p-GRP", 6); !f.OK || !f.Country {
			t.Errorf("with the show's country: %+v", f)
		}
		if f := judge("Wolf.1999.S01E01.German-GRP", 6); f.OK {
			t.Errorf("another year: %+v", f)
		}
	})

	t.Run("only the languages set", func(t *testing.T) {
		fr := newSearchFit(all, []string{"fr"})
		if f := fr.Fit(parser.Parse(heistDE), byID(all, 1)); f.OK {
			t.Errorf("a German title with only French set: %+v", f)
		}
	})

	t.Run("names too short never count", func(t *testing.T) {
		up := germanShow(7, "Upside Down", 2017, "Up")
		lib := append(searchLibrary(), up)
		f := newSearchFit(lib, []string{"de"})
		for _, r := range []string{"Up.S01E01.German-GRP", "Up.2017.S01E01.German-GRP"} {
			if got := f.Fit(parser.Parse(r), up); got.OK {
				t.Errorf("%s: %+v", r, got)
			}
		}
	})
}

// Until every show's translated titles have been read for the languages set, a show not
// yet read could share the name unseen: the translated tier waits.
func TestSearchFitWaitsForEveryShowsTitles(t *testing.T) {
	all := searchLibrary()
	unread := Series{ID: 9, TMDBID: 9, Title: "Unread", Year: 2015} // no local_langs yet
	fit := newSearchFit(append(all, unread), []string{"de"})
	if f := fit.Fit(parser.Parse(heistDE), byID(all, 1)); f.OK {
		t.Errorf("matched before the library was read: %+v", f)
	}
	if q := fit.QueryTitle(byID(all, 1)); q != "" {
		t.Errorf("query %q before the library was read", q)
	}
	// A show TMDB doesn't know is never read and never waited for.
	noTMDB := Series{ID: 9, Title: "Home Video"}
	fit = newSearchFit(append(all, noTMDB), []string{"de"})
	if f := fit.Fit(parser.Parse(heistDE), byID(all, 1)); !f.OK {
		t.Errorf("a show with no TMDB id held the tier back: %+v", f)
	}
}

// The library is read only for a release one of the show's translated titles names, once
// per SearchFit, and an unreadable library fails closed.
func TestSearchFitReadsLibraryLazily(t *testing.T) {
	all := searchLibrary()
	reads := 0
	fit := &SearchFit{langs: []string{"de"}, load: func() ([]Series, error) { reads++; return all, nil }}
	heist := byID(all, 1)
	for _, r := range []string{"Money.Heist.S01E01-GRP", "Something.Else.S01E01-GRP", "Haus.S01E01-GRP"} {
		fit.Fit(parser.Parse(r), heist)
	}
	if reads != 0 {
		t.Fatalf("%d library reads for releases no translated title names", reads)
	}
	fit.Fit(parser.Parse(heistDE), heist)
	fit.Fit(parser.Parse(heistDE), heist)
	fit.QueryTitle(heist)
	if reads != 1 {
		t.Errorf("%d library reads, want one", reads)
	}
	broken := &SearchFit{langs: []string{"de"}, load: func() ([]Series, error) { return nil, errors.New("db gone") }}
	if f := broken.Fit(parser.Parse(heistDE), heist); f.OK {
		t.Errorf("an unreadable library matched: %+v", f)
	}
}

// QueryTitle: the title a search may also ask indexers by — never one the translated tier
// couldn't take a release by.
func TestSearchFitQueryTitle(t *testing.T) {
	all := searchLibrary()
	fit := newSearchFit(all, []string{"de"})
	for id, want := range map[int64]string{
		1: "Haus des Geldes",
		2: "", // no translated title
		3: "", // "Haus" is another show's own title
		4: "", // shared with Bron
		5: "", // shared with The Bridge
		6: "", // one short word
	} {
		if got := fit.QueryTitle(byID(all, id)); got != want {
			t.Errorf("%s: %q, want %q", byID(all, id).Title, got, want)
		}
	}
	// The setting's order picks between languages.
	multi := germanShow(8, "Money Heist", 2017)
	multi.Extra.LocalLangs = []string{"de", "fr"}
	multi.Extra.LocalTitles = []metadata.TranslatedTitle{{Lang: "fr", Title: "La Maison de papier"}, {Lang: "de", Title: "Haus des Geldes"}}
	lib := []Series{multi}
	if got := newSearchFit(lib, []string{"de", "fr"}).QueryTitle(multi); got != "Haus des Geldes" {
		t.Errorf("de first: %q", got)
	}
	if got := newSearchFit(lib, []string{"fr", "de"}).QueryTitle(multi); got != "La Maison de papier" {
		t.Errorf("fr first: %q", got)
	}
}
