package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Free-form names are sniffed strictly: full names and 3-letter codes count, two-letter
// words don't, so a subtitle called "Its.A.Trap" isn't tagged Italian.
func TestDetectLangStrict(t *testing.T) {
	for stem, want := range map[string]string{
		"2_english":                  "en",
		"3_swedish":                  "sv",
		"polish":                     "pl",
		"turkish.sdh":                "tr",
		"hindi":                      "hi",
		"brazilian.portuguese":       "pt-br",
		"eng":                        "en",
		"en":                         "en",    // a name that is nothing but a tag
		"pt-br":                      "pt-br", // likewise, regional
		"its.a.trap":                 "",
		"the.cat.in.the.hat":         "",
		"it.is.no.to.be":             "",
		"show.s01e01.web.dl":         "",
		"some.release.name-group":    "",
		"spanish (latin america)":    "es-419", // the whole name is one of the table's names
		"chinese.traditional.forced": "zh",
	} {
		if got := detectLang(stem); got != want {
			t.Errorf("detectLang(%q) = %q, want %q", stem, got, want)
		}
	}
}

func TestSubLangForSharedTable(t *testing.T) {
	cases := []struct {
		stem, lang string
	}{
		{"movie.en", "en"},
		{"movie.eng", "en"},
		{"movie.english", "en"},
		{"movie.sv", "sv"},
		{"movie.pl", "pl"},
		{"movie.tr", "tr"},
		{"movie.hi", "hi"},
		{"movie.da", "da"},
		{"movie.pt-br", "pt-br"},
		{"movie.en.forced", "en"},
		{"movie.en.hi", "en"}, // Bazarr's hearing-impaired English, not Hindi
		{"movie.proper", "proper"},
		{"movie", ""},
	}
	for _, c := range cases {
		got, ok := subLangFor(c.stem, "movie", false)
		if !ok || got != c.lang {
			t.Errorf("subLangFor(%q) = %q, %v; want %q", c.stem, got, ok, c.lang)
		}
	}
}

// Release subtitles in languages the old table lacked are imported with their tag, and a
// free-form name that only looks like a code is not.
func TestImportSubsNewLanguages(t *testing.T) {
	src := t.TempDir()
	lib := t.TempDir()
	name := "Dune.Part.Two.2024.1080p.WEB-DL-FLUX"
	writeFile(t, filepath.Join(src, name, name+".mkv"), 60<<20)
	writeFile(t, filepath.Join(src, name, name+".sv.srt"), 41)
	writeFile(t, filepath.Join(src, name, "Subs", "Polish.srt"), 42)
	writeFile(t, filepath.Join(src, name, "Subs", "3_Turkish.srt"), 43)
	writeFile(t, filepath.Join(src, name, "Subs", "Its.A.Trap.srt"), 44)

	im := NewImporter(lib, quiet())
	res, err := im.Import(name, filepath.Join(src, name))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	base := strings.TrimSuffix(res.TargetPath, filepath.Ext(res.TargetPath))
	for _, want := range []string{base + ".sv.srt", base + ".pl.srt", base + ".tr.srt", base + ".srt"} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected subtitle at %q: %v", want, err)
		}
	}
	if _, err := os.Stat(base + ".it.srt"); err == nil {
		t.Error("Its.A.Trap.srt was tagged Italian")
	}
}
