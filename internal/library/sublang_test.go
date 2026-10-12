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
		"other.name.de":              "de", // a dotted name ending in a tag
		"other.name.pt-br.forced":    "pt-br",
		"the.cat.in.the.hat":         "",
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
		"it.is.no.to.be":             "",
		"show.s01e01.web.dl":         "",
		"some.release.name-group":    "",
		"spanish (latin america)":    "es-419", // the whole name is one of the table's names
		"chinese.traditional.forced": "zh",
	} {
		if got, _ := detectLang(stem); got != want {
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
		got, _, ok := subLangFor(c.stem, "movie", false)
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

func TestSubLangForQualifiers(t *testing.T) {
	cases := []struct {
		stem    string
		own     bool
		lang    string
		variant string
	}{
		{"movie.en", false, "en", ""},
		{"movie.en.forced", false, "en", "forced"},
		{"movie.en.sdh", false, "en", "sdh"},
		{"movie.en.hi", false, "en", "sdh"}, // Bazarr's hearing-impaired English
		{"movie.en.cc", false, "en", "sdh"},
		{"movie.hi", false, "hi", ""}, // on its own, Hindi
		{"movie.eng.forced", false, "en", "forced"},
		{"movie.en.sdh.forced", false, "en", "forced"},
		{"movie.forced", false, "", "forced"},
		{"movie.proper.forced", false, "proper", "forced"},
		{"2_english_forced", true, "en", "forced"},
		{"3_english_sdh", true, "en", "sdh"},
		{"4_english_hi", true, "en", "sdh"},
		{"english", true, "en", ""},
		{"hi", true, "hi", ""},
		{"forced", true, "", "forced"},
		{"other.name.en.forced", true, "en", "forced"},
	}
	for _, c := range cases {
		lang, variant, ok := subLangFor(c.stem, "movie", c.own)
		if !ok || lang != c.lang || variant != c.variant {
			t.Errorf("subLangFor(%q, own=%v) = (%q, %q, %v), want (%q, %q)", c.stem, c.own, lang, variant, ok, c.lang, c.variant)
		}
	}
}

// Both a forced and a full English subtitle in a release land under their own names,
// whichever the folder lists first.
func TestImportSidecarSubsKeepsForced(t *testing.T) {
	src := t.TempDir()
	lib := t.TempDir()
	name := "Dune.Part.Two.2024.1080p.WEB-DL-FLUX"
	writeFile(t, filepath.Join(src, name, name+".mkv"), 60<<20)
	writeFile(t, filepath.Join(src, name, name+".en.forced.srt"), 900) // sorts before .en.srt
	writeFile(t, filepath.Join(src, name, name+".en.srt"), 50000)
	writeFile(t, filepath.Join(src, name, name+".en.sdh.srt"), 52000)
	writeFile(t, filepath.Join(src, name, name+".de.hi.srt"), 51000)

	im := NewImporter(lib, quiet())
	res, err := im.Import(name, filepath.Join(src, name))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	base := strings.TrimSuffix(res.TargetPath, filepath.Ext(res.TargetPath))
	for suffix, size := range map[string]int64{".en.srt": 50000, ".en.forced.srt": 900, ".en.sdh.srt": 52000, ".de.sdh.srt": 51000} {
		fi, err := os.Stat(base + suffix)
		if err != nil {
			t.Errorf("expected %s: %v", suffix, err)
			continue
		}
		if fi.Size() != size {
			t.Errorf("%s holds the %d-byte file, want the %d-byte one", suffix, fi.Size(), size)
		}
	}
	if _, err := os.Stat(base + ".en.2.srt"); err == nil {
		t.Error("the full subtitle was pushed to .en.2.srt")
	}
}

// A season pack ships the forced track unlabelled: 2_English.srt (12 KB) beside
// 3_English.srt (80 KB) is the forced one, by size.
func TestImportPackSubsSizeHeuristic(t *testing.T) {
	src := t.TempDir()
	lib := t.TempDir()
	rel := filepath.Join(src, "Show.S01.1080p.WEB-DL")
	video := filepath.Join(rel, "Show.S01E01.1080p.mkv")
	writeFile(t, video, 3000)
	writeFile(t, filepath.Join(rel, "Show.S01E02.1080p.mkv"), 3000)
	writeFile(t, filepath.Join(rel, "Subs", "Show.S01E01.1080p", "2_English.srt"), 12<<10)
	writeFile(t, filepath.Join(rel, "Subs", "Show.S01E01.1080p", "3_English.srt"), 80<<10)
	writeFile(t, filepath.Join(rel, "Subs", "Show.S01E01.1080p", "4_Spanish.srt"), 9<<10) // alone in its language: full

	im := NewImporter(lib, quiet())
	res, ok, err := im.ImportEpisode("Show", 0, video)
	if err != nil || !ok {
		t.Fatalf("import: ok=%v err=%v", ok, err)
	}
	base := strings.TrimSuffix(res.TargetPath, filepath.Ext(res.TargetPath))
	for suffix, size := range map[string]int64{".en.srt": 80 << 10, ".en.forced.srt": 12 << 10, ".es.srt": 9 << 10} {
		fi, err := os.Stat(base + suffix)
		if err != nil {
			t.Errorf("expected %s: %v", suffix, err)
			continue
		}
		if fi.Size() != size {
			t.Errorf("%s holds %d bytes, want %d", suffix, fi.Size(), size)
		}
	}
}

// Two full-size subtitles in one language are both full: the heuristic only fires on a
// much smaller one.
func TestImportPackSubsSimilarSizesStayFull(t *testing.T) {
	src := t.TempDir()
	lib := t.TempDir()
	rel := filepath.Join(src, "Show.S01E01.1080p.WEB-DL")
	video := filepath.Join(rel, "Show.S01E01.1080p.mkv")
	writeFile(t, video, 3000)
	writeFile(t, filepath.Join(rel, "Subs", "2_English.srt"), 70<<10)
	writeFile(t, filepath.Join(rel, "Subs", "3_English.srt"), 80<<10)

	im := NewImporter(lib, quiet())
	res, ok, err := im.ImportEpisode("Show", 0, video)
	if err != nil || !ok {
		t.Fatalf("import: ok=%v err=%v", ok, err)
	}
	base := strings.TrimSuffix(res.TargetPath, filepath.Ext(res.TargetPath))
	if _, err := os.Stat(base + ".en.forced.srt"); err == nil {
		t.Error("a near-full-size subtitle was marked forced")
	}
	for _, suffix := range []string{".en.srt", ".en.2.srt"} {
		if _, err := os.Stat(base + suffix); err != nil {
			t.Errorf("expected %s: %v", suffix, err)
		}
	}
}
