package automation

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// The stored list reads back as canonical codes, in order, once each; anything the
// language table doesn't know is dropped rather than rejecting everything.
func TestParseBookLanguages(t *testing.T) {
	cases := map[string][]string{
		"en":                {"en"},
		"en,es":             {"en", "es"},
		" ENG , spa ,en":    {"en", "es"},
		"French,pt-BR":      {"fr", "pt-br"},
		"":                  {},
		"en,klingonish,de,": {"en", "de"},
	}
	for raw, want := range cases {
		if got := ParseBookLanguages(raw); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseBookLanguages(%q) = %v, want %v", raw, got, want)
		}
	}
}

// bookRelScore turns down a release stating a language outside the book languages, and
// only then: no language, an unreadable one, or no list at all pass. [BOOK-16]
func TestBookRelScoreLanguage(t *testing.T) {
	sp := quality.StoredProfile{FormatScores: map[string]int{"EPUB": 40, "M4B": 40}}
	en, enES := newBookLangs([]string{"en"}), newBookLangs([]string{"en", "es"})
	cases := []struct {
		lang    string
		allowed bookLangs
		ok      bool
	}{
		{"", en, true},
		{"en", en, true},
		{"de", en, false},
		{"es", en, false},
		{"es", enES, true},
		{"de", enES, false},
		{"de", nil, true},         // no setting wired: every language
		{"de", bookLangs{}, true}, // saved empty: any language
		{"multi", en, true},       // can't tell, so not wrong
		{"pt-br", newBookLangs([]string{"pt"}), true},
		{"pt", newBookLangs([]string{"pt-br"}), true}, // a tracker's POR is any Portuguese
	}
	for _, tc := range cases {
		rel := indexer.Release{Title: "Frank Herbert - Dune [EPUB]", Format: "EPUB", Language: tc.lang, Seeders: 10}
		_, ok := bookRelScore(sp, tc.allowed, rel)
		if ok != tc.ok {
			t.Errorf("language %q with %v: ok = %v, want %v", tc.lang, tc.allowed, ok, tc.ok)
		}
		if _, code := bookRelVerdict(sp, tc.allowed, rel); !tc.ok && code != DropWrongLanguage {
			t.Errorf("language %q: code %q, want %q", tc.lang, code, DropWrongLanguage)
		}
	}
	// The format verdict still comes first: an unwanted format in another language is
	// "not wanted", which is the more useful thing to say.
	if _, code := bookRelVerdict(sp, en, indexer.Release{Title: "Dune [PDF]", Format: "PDF", Language: "de"}); code != DropNotWanted {
		t.Errorf("unwanted format in German: code %q, want %q", code, DropNotWanted)
	}
}

// A German edition that keeps the original title wins on seeders without the filter; with
// languages = en the automatic pick is the English one.
func TestPickBestBookSkipsOtherLanguages(t *testing.T) {
	sp := quality.StoredProfile{FormatScores: map[string]int{"M4B": 40}}
	releases := []indexer.Release{
		{Title: "Frank Herbert - Dune [M4B]", Format: "M4B", Language: "de", Seeders: 900},
		{Title: "Frank Herbert - Dune [M4B]", Format: "M4B", Language: "en", Seeders: 12},
	}
	if best := pickBestBookForKind(netPolicy{}, sp, nil, releases, "audiobook", ""); best == nil || best.Language != "de" {
		t.Fatalf("without a language list the seeders decide: got %+v", best)
	}
	c := &Coordinator{}
	c.SetBookLanguages(func(context.Context) []string { return []string{"en"} })
	if best := pickBestBookForKind(netPolicy{}, sp, c.bookLanguages(context.Background()), releases, "audiobook", ""); best == nil || best.Language != "en" {
		t.Fatalf("with languages = en: got %+v, want the English release", best)
	}
	only := releases[:1]
	if best := pickBestBookForKind(netPolicy{}, sp, c.bookLanguages(context.Background()), only, "audiobook", ""); best != nil {
		t.Fatalf("a German-only result must grab nothing, got %+v", best)
	}
}

// The interactive search says why a release in another language isn't recommended.
func TestBookLangRejectReason(t *testing.T) {
	if got := bookLangRejectReason("GER"); got != "Language: de — not in your book languages" {
		t.Errorf("reason = %q", got)
	}
	if !strings.Contains(ReasonLabel(DropWrongLanguage), "book languages") {
		t.Errorf("label = %q", ReasonLabel(DropWrongLanguage))
	}
}
