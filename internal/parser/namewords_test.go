package parser

import (
	"reflect"
	"testing"
)

func TestNameWords(t *testing.T) {
	cases := map[string][]string{
		"Show.The.Christmas.Special.2019.1080p.WEB-DL.x264-GRP": {"show", "the", "christmas", "special", "2019"},
		"Show.Christmas.Special.mkv":                            {"show", "christmas", "special"},
		"Show.Christmas.Special-GRP":                            {"show", "christmas", "special"},
		"Show.The.Christmas.Special.2019.1080p.WEB":             {"show", "the", "christmas", "special", "2019"},
		"[SubsPlease] Show - Christmas Special (1080p) [ABCD]":  {"show", "christmas", "special"},
		"The Office (US) Christmas Party 720p":                  {"the", "office", "us", "christmas", "party"},
		"Love_&_Death_Special_720p":                             {"love", "death", "special"},
	}
	for name, want := range cases {
		if got := NameWords(name); !reflect.DeepEqual(got, want) {
			t.Errorf("NameWords(%q) = %v, want %v", name, got, want)
		}
	}
}
