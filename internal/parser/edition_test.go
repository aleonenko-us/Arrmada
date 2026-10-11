package parser

import "testing"

func TestNormalizeEdition(t *testing.T) {
	cases := map[string]string{
		"Director's Cut": "Director's Cut",
		"Directors.Cut":  "Director's Cut",
		"directors cut":  "Director's Cut",
		"Director’s Cut": "Director's Cut",
		"DIRECTOR CUT":   "Director's Cut",
		"Extended Cut":   "Extended",
		"Ultimate Cut":   "ultimate cut", // unknown: its own words, compared case-blind
		"  ":             "",
		"":               "",
	}
	for in, want := range cases {
		if got := NormalizeEdition(in); got != want {
			t.Errorf("NormalizeEdition(%q) = %q, want %q", in, got, want)
		}
	}
	if NormalizeEdition("Ultimate.Cut") != NormalizeEdition("ultimate cut") {
		t.Error("an unknown edition should compare equal however it's punctuated")
	}
}

func TestCanonicalEdition(t *testing.T) {
	cases := map[string]string{
		"directors cut":    "Director's Cut",
		" Ultimate  Cut  ": "Ultimate Cut",
		"imax":             "IMAX",
		"":                 "",
	}
	for in, want := range cases {
		if got := CanonicalEdition(in); got != want {
			t.Errorf("CanonicalEdition(%q) = %q, want %q", in, got, want)
		}
	}
}

// A release name with the apostrophe kept is still a Director's Cut.
func TestParseEditionApostrophe(t *testing.T) {
	for _, name := range []string{
		"Blade Runner 1982 Director's Cut 1080p BluRay x264-GRP",
		"Blade.Runner.1982.Directors.Cut.2160p.UHD.BluRay-GRP",
	} {
		if got := Parse(name).Edition; got != "Director's Cut" {
			t.Errorf("Parse(%q).Edition = %q", name, got)
		}
	}
}
