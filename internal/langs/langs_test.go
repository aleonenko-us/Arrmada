package langs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTableShape(t *testing.T) {
	base, regional := 0, 0
	for _, l := range All() {
		if l.Regional {
			regional++
			if _, ok := byCode[l.ISO1]; !ok {
				t.Errorf("%s: base %s missing", l.Code, l.ISO1)
			}
		} else {
			base++
			if len(l.Code) != 2 || len(l.ISO2B) != 3 || len(l.ISO2T) != 3 {
				t.Errorf("%+v: malformed base row", l)
			}
		}
		if l.Name == "" {
			t.Errorf("%s: no name", l.Code)
		}
	}
	if base < 180 || regional < 7 {
		t.Errorf("table has %d base and %d regional rows; want ~183 and the 7 regional tags", base, regional)
	}
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "eng": "en", "English": "en", " ENG ": "en",
		"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr", "Français": "fr",
		"de": "de", "ger": "de", "deu": "de",
		"nl": "nl", "dut": "nl", "nld": "nl", "flemish": "nl",
		"zh": "zh", "chi": "zh", "zho": "zh",
		"da": "da", "dan": "da", "danish": "da",
		"iw": "he", "fil": "tl", "farsi": "fa",
		"pt-BR": "pt-br", "pt_br": "pt-br", "pob": "pt-br", "brazilian": "pt-br",
		"zh-Hans": "zh-cn", "zh-CN": "zh-cn", "zh-Hant": "zh-tw", "zh_TW": "zh-tw", "zh-hk": "zh-tw",
		"es-419": "es-419", "es-la": "es-419", "es-MX": "es-mx", "fr-CA": "fr-ca",
		"en-US": "en", "fr-FR": "fr", "por-xx": "pt", // an unlisted region folds to the base
		"": "", "und": "", "UND": "",
		"xx": "xx", "Klingon": "klingon", "qaa": "qaa", // unknown: normalised, unchanged
		"it-girl": "it-girl", "english-dub": "english-dub", "en-1080p": "en-1080p", // hyphenated words aren't regions
		"sr-latn": "sr", "en-001": "en", // a script or numeric area folds to the base too
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupKnownVsUnknown(t *testing.T) {
	if l, ok := Lookup("fre"); !ok || l.Code != "fr" || l.ISO2B != "fre" || l.ISO2T != "fra" || l.Name != "French" {
		t.Errorf("Lookup(fre) = %+v, %v", l, ok)
	}
	if l, ok := Lookup("pt-br"); !ok || !l.Regional || l.ISO1 != "pt" || l.ISO2B != "por" {
		t.Errorf("Lookup(pt-br) = %+v, %v", l, ok)
	}
	for _, tok := range []string{"", "und", "xx", "proper", "burgundy"} {
		if _, ok := Lookup(tok); ok {
			t.Errorf("Lookup(%q) found a language", tok)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		track, want string
		ok          bool
	}{
		{"eng", "en", true},
		{"en", "en", true},
		{"eng", "eng", true},
		{"spa", "es", true},
		{"fre", "fr", true},
		{"fra", "fr", true}, // the T code an MP4 muxer writes
		{"deu", "de", true},
		{"zho", "zh", true},
		{"nld", "nl", true},
		{"eng", "es", false},
		{"", "en", false},    // an untagged track never matches a specific language
		{"und", "en", false}, // nor does an undefined one
		{"en", "", false},
		// A base want matches any region of it.
		{"pt-br", "pt", true},
		{"por", "pt", true},
		{"zh-tw", "zh", true},
		{"en-us", "en", true},
		// A regional want matches only that region…
		{"pt-br", "pt-br", true},
		{"pob", "pt-br", true},
		{"pt-pt", "pt-br", false},
		{"zh-cn", "zh-tw", false},
		// …and never the bare base on its own (callers fall back to Base(want) explicitly).
		{"por", "pt-br", false},
		{"zh", "zh-tw", false},
		// Unknown tags still equal themselves.
		{"xx", "xx", true},
		{"xx", "en", false},
	}
	for _, c := range cases {
		if got := Match(c.track, c.want); got != c.ok {
			t.Errorf("Match(%q, %q) = %v, want %v", c.track, c.want, got, c.ok)
		}
	}
}

func TestIsTokenStrictVsLoose(t *testing.T) {
	loose := []string{"en", "it", "no", "is", "eng", "english", "pt-br", "pt_BR", "en-us", "hi", "da"}
	for _, tok := range loose {
		if !IsToken(tok, false) {
			t.Errorf("IsToken(%q, loose) = false", tok)
		}
	}
	strictYes := []string{"eng", "english", "swe", "swedish", "pol", "tur", "hin", "hindi", "ita", "italian", "brazilian", "latino", "fre", "ger"}
	for _, tok := range strictYes {
		if !IsToken(tok, true) {
			t.Errorf("IsToken(%q, strict) = false", tok)
		}
	}
	// Words that would be two-letter codes or are everyday English must not be read as
	// languages in a free-form name ("Its.A.Trap", "The.Cat.In.The.Hat").
	strictNo := []string{"it", "is", "to", "be", "no", "en", "a", "cat", "hat", "sun", "may", "ice", "run", "pt-br", "its", "trap", "the", ""}
	for _, tok := range strictNo {
		if IsToken(tok, true) {
			t.Errorf("IsToken(%q, strict) = true", tok)
		}
	}
	for _, tok := range []string{"", "proper", "burgundy", "2"} {
		if IsToken(tok, false) {
			t.Errorf("IsToken(%q, loose) = true", tok)
		}
	}
}

func TestNameISO1OpenSubtitlesSidecarTag(t *testing.T) {
	type row struct{ in, name, iso1, os, tag string }
	for _, r := range []row{
		{"en", "English", "en", "en", "en"},
		{"eng", "English", "en", "en", "en"},
		{"fre", "French", "fr", "fr", "fr"},
		{"ger", "German", "de", "de", "de"},
		{"chi", "Chinese", "zh", "zh-cn", "zh"},
		{"zho", "Chinese", "zh", "zh-cn", "zh"},
		{"Japanese", "Japanese", "ja", "ja", "ja"},
		{"pt", "Portuguese", "pt", "pt-pt", "pt"},
		{"pt-br", "Portuguese (Brazil)", "pt", "pt-br", "pt-br"},
		{"pt-pt", "Portuguese (Portugal)", "pt", "pt-pt", "pt-pt"},
		{"zh-tw", "Chinese (Traditional)", "zh", "zh-tw", "zh-tw"},
		{"zh-hant", "Chinese (Traditional)", "zh", "zh-tw", "zh-tw"},
		{"es-419", "Spanish (Latin America)", "es", "ea", "es-419"},
		{"xx", "xx", "", "xx", "xx"},
	} {
		if got := Name(r.in); got != r.name {
			t.Errorf("Name(%q) = %q, want %q", r.in, got, r.name)
		}
		if got := ISO1(r.in); got != r.iso1 {
			t.Errorf("ISO1(%q) = %q, want %q", r.in, got, r.iso1)
		}
		if got := OpenSubtitles(r.in); got != r.os {
			t.Errorf("OpenSubtitles(%q) = %q, want %q", r.in, got, r.os)
		}
		if got := SidecarTag(r.in); got != r.tag {
			t.Errorf("SidecarTag(%q) = %q, want %q", r.in, got, r.tag)
		}
	}
	if Base("pt-br") != "pt" || Base("fre") != "fr" || Base("xx") != "xx" {
		t.Error("Base mapping wrong")
	}
}

func TestParseSidecarTag(t *testing.T) {
	cases := []struct {
		segs                 string
		lang, variant, token string
	}{
		{"en", "en", VariantFull, "en"},
		{"eng", "en", VariantFull, "eng"},
		{"english", "en", VariantFull, "english"},
		{"en.forced", "en", VariantForced, "en"},
		{"en.sdh", "en", VariantSDH, "en"},
		{"en.cc", "en", VariantSDH, "en"},
		{"en.hi", "en", VariantSDH, "en"}, // Bazarr's hearing-impaired
		{"hi", "hi", VariantFull, "hi"},   // on its own, Hindi
		{"hindi.hi", "hi", VariantSDH, "hindi"},
		{"en.default", "en", VariantFull, "en"},
		{"en.default.forced", "en", VariantForced, "en"},
		{"en.sdh.forced", "en", VariantForced, "en"}, // forced wins: never the full track
		{"en.forced.sdh", "en", VariantForced, "en"},
		{"forced", "", VariantForced, ""},
		{"da", "da", VariantFull, "da"},
		{"pt-br", "pt-br", VariantFull, "pt-br"},
		{"pt-BR.forced", "pt-br", VariantForced, "pt-br"},
		{"zh-tw.sdh", "zh-tw", VariantSDH, "zh-tw"},
		{"fre", "fr", VariantFull, "fre"},
		{"proper", "", VariantFull, "proper"}, // a tag that is not a language: never credited
		{"anchorman the legend of ron burgundy", "", VariantFull, "anchorman the legend of ron burgundy"},
		{"", "", VariantFull, ""},
	}
	for _, c := range cases {
		got := ParseSidecarTag(strings.Split(c.segs, "."))
		if got.Lang != c.lang || got.Variant != c.variant || got.Token != c.token {
			t.Errorf("%q: got %+v, want {%q %q %q}", c.segs, got, c.lang, c.variant, c.token)
		}
	}
}

// The committed table must be what the generator makes from the vendored TSV.
func TestTableIsGenerated(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool")
	}
	out := filepath.Join(t.TempDir(), "table_gen.go")
	cmd := exec.Command("go", "run", "./gen", filepath.Join("gen", "iso639.tsv"), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, b)
	}
	want, _ := os.ReadFile(out)
	got, _ := os.ReadFile("table_gen.go")
	norm := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
	if norm(got) != norm(want) {
		t.Error("table_gen.go is stale: run go generate ./internal/langs")
	}
}
