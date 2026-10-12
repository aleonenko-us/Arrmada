package indexer

import (
	"strings"
	"testing"
)

func TestJoinMAMNames(t *testing.T) {
	cases := map[string]string{
		`{"123":"Brandon Sanderson"}`:               "Brandon Sanderson",
		`{"1":"Michael Kramer","2":"Kate Reading"}`: "", // order-dependent; checked below
		``:   "",
		`[]`: "",
		`{}`: "",
		`{"9":"Neil Gaiman &amp; Terry Pratchett"}`: "Neil Gaiman & Terry Pratchett",
	}
	for in, want := range cases {
		if want == "" && in == `{"1":"Michael Kramer","2":"Kate Reading"}` {
			// Two-name map: just assert both names are present (map order is random).
			got := joinMAMNames(in)
			if got == "" || !strings.Contains(got, "Michael Kramer") || !strings.Contains(got, "Kate Reading") {
				t.Errorf("joinMAMNames(%q) = %q, want both narrators", in, got)
			}
			continue
		}
		if got := joinMAMNames(in); got != want {
			t.Errorf("joinMAMNames(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinMAMSeries(t *testing.T) {
	if got := joinMAMSeries(`{"42":["The Stormlight Archive","1"]}`); got != "The Stormlight Archive #1" {
		t.Errorf("series = %q", got)
	}
	if got := joinMAMSeries(`{"42":["Mistborn"]}`); got != "Mistborn" {
		t.Errorf("series (no number) = %q", got)
	}
	if got := joinMAMSeries(``); got != "" {
		t.Errorf("empty series = %q", got)
	}
}

func TestParseHumanSize(t *testing.T) {
	mib := float64(int64(1) << 20)
	gib := float64(int64(1) << 30)
	cases := map[string]int64{
		"358.12 MB": int64(358.12 * mib),
		"1.09 GB":   int64(1.09 * gib),
		"512 KB":    512 << 10,
		"bogus":     0,
		"":          0,
		"10":        0,
	}
	for in, want := range cases {
		if got := parseHumanSize(in); got != want {
			t.Errorf("parseHumanSize(%q) = %d, want %d", in, got, want)
		}
	}
}

// MAM's lang_code is a 639-2 code; releases carry the canonical code the book language
// filter compares, with the tracker's own spelling kept in the description. [BOOK-16]
func TestMAMReleaseLanguage(t *testing.T) {
	m := &MAMSearcher{}
	cases := map[string]string{
		"ENG": "en", "GER": "de", "DEU": "de", "SPA": "es", "FRE": "fr", "FRA": "fr",
		"ITA": "it", "DUT": "nl", "NLD": "nl", "POR": "pt", "SWE": "sv", "": "", "XYZ": "xyz",
	}
	for code, want := range cases {
		rel := m.releaseFrom(Indexer{Name: "MAM"}, mamItem{ID: "1", Title: "Dune", LangCode: code})
		if rel.Language != want {
			t.Errorf("lang_code %q → %q, want %q", code, rel.Language, want)
		}
		if code != "" && !strings.Contains(rel.Description, "Language: "+code) {
			t.Errorf("lang_code %q: description %q lost the tracker's language", code, rel.Description)
		}
	}
}
