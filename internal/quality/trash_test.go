package quality

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

func importFixture(t *testing.T, file, media string) TrashImport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "trash", file))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ImportTrashCF(data, media)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return got
}

// matchesAll checks an imported format against names it should and shouldn't match.
func checkMatches(t *testing.T, f CustomFormat, yes, no []string) {
	t.Helper()
	e := NewEngine([]CustomFormat{f})
	nf := e.formats[f.Name]
	for _, n := range yes {
		if !e.match(nf, n, parser.Parse(n)) {
			t.Errorf("%s should match %s", f.Name, n)
		}
	}
	for _, n := range no {
		if e.match(nf, n, parser.Parse(n)) {
			t.Errorf("%s shouldn't match %s", f.Name, n)
		}
	}
}

func onlyFormat(t *testing.T, got TrashImport) TrashFormat {
	t.Helper()
	if len(got.Formats) != 1 {
		t.Fatalf("formats = %+v, warnings = %v", got.Formats, got.Warnings)
	}
	return got.Formats[0]
}

func TestImportTrashX265HD(t *testing.T) {
	got := importFixture(t, "x265-hd.json", MediaMovie)
	tf := onlyFormat(t, got)
	want := CustomFormat{Name: "x265 (HD)", Conditions: []Condition{
		{Type: CondReleaseTitle, Value: `[xh][ ._-]?265|\bHEVC(\b|\d)`},
		{Type: CondResolution, Value: "2160p", Negate: true},
	}}
	if !reflect.DeepEqual(tf.Format, want) {
		t.Errorf("format = %+v", tf.Format)
	}
	if tf.SuggestedScore == nil || *tf.SuggestedScore != -10000 || tf.TrashID != "fixture-x265-hd" {
		t.Errorf("score/id = %v %q", tf.SuggestedScore, tf.TrashID)
	}
	if len(tf.Warnings) != 0 || len(got.Warnings) != 0 {
		t.Errorf("warnings = %v / %v", tf.Warnings, got.Warnings)
	}
	checkMatches(t, tf.Format,
		[]string{"Movie.2020.1080p.BluRay.x265-GRP", "Movie.2020.720p.WEB-DL.HEVC-GRP", "Show.S01E01.1080p.WEB.h265-GRP"},
		[]string{"Movie.2020.2160p.WEB-DL.x265-GRP", "Movie.2020.1080p.BluRay.x264-GRP"})
}

// A required pattern that needs lookarounds can't be carried over, and without it the
// format would be "not a WEB release" — so it isn't imported, and the warning says why.
func TestImportTrashBRDiskIsRefusedWithAReason(t *testing.T) {
	got := importFixture(t, "br-disk.json", MediaMovie)
	if len(got.Formats) != 0 {
		t.Fatalf("formats = %+v", got.Formats)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], `"BR-DISK"`) || !strings.Contains(got.Warnings[0], "lookaround") {
		t.Errorf("warnings = %v", got.Warnings)
	}
}

func TestImportTrashBlurayTier(t *testing.T) {
	tf := onlyFormat(t, importFixture(t, "hd-bluray-tier-01.json", MediaMovie))
	want := CustomFormat{Name: "HD Bluray Tier 01", Conditions: []Condition{
		{Type: CondReleaseGroup, Value: "BBQ|D-Z0N3|DON|EbP"},
		{Type: CondSource, Value: "BluRay|Remux"},
		{Type: CondSource, Value: "Remux", Negate: true},
		{Type: CondResolution, Value: "2160p", Negate: true},
	}}
	if !reflect.DeepEqual(tf.Format, want) {
		t.Errorf("format = %+v", tf.Format)
	}
	if len(tf.Warnings) != 0 {
		t.Errorf("warnings = %v", tf.Warnings)
	}
	checkMatches(t, tf.Format,
		[]string{"Movie.2020.1080p.BluRay.DTS.x264-DON", "Movie.2020.720p.BluRay.x264-EbP"},
		[]string{"Movie.2020.1080p.BluRay.REMUX.AVC.DTS-HD.MA-DON", "Movie.2020.1080p.WEB-DL.x264-DON",
			"Movie.2020.1080p.BluRay.x264-SPARKS", "Movie.2020.2160p.BluRay.x265-DON"})
}

// Two kinds of alternatives (groups, and WEB or WEBRip) each fold into one condition, so
// "one of these groups AND a web source" survives exactly. Sonarr numbers sources its own
// way: 3 is Web there (a telecine in Radarr).
func TestImportTrashWebTierFoldsAlternatives(t *testing.T) {
	tf := onlyFormat(t, importFixture(t, "web-tier-01.json", MediaSeries))
	want := CustomFormat{Name: "WEB Tier 01", Conditions: []Condition{
		{Type: CondReleaseGroup, Value: "FLUX|NTb|NOSiViD|playWEB"},
		{Type: CondSource, Value: "WEB-DL|WEBRip"},
	}}
	if !reflect.DeepEqual(tf.Format, want) {
		t.Errorf("format = %+v", tf.Format)
	}
	checkMatches(t, tf.Format,
		[]string{"Show.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb", "Show.S01E01.1080p.WEBRip.x264-playWEB"},
		[]string{"Show.S01E01.1080p.BluRay.x264-NTb", "Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-EVO"})
}

// An alternative that can't run is skipped and said; the rest still match.
func TestImportTrashSkipsAnAlternativeWithAWarning(t *testing.T) {
	tf := onlyFormat(t, importFixture(t, "remaster.json", MediaMovie))
	want := CustomFormat{Name: "Remaster", Conditions: []Condition{
		{Type: CondReleaseTitle, Value: `\bRemaster(ed)?\b`},
		{Type: CondReleaseTitle, Value: `\b4K[ ._-]Remaster(ed)?\b`, Negate: true},
	}}
	if !reflect.DeepEqual(tf.Format, want) {
		t.Errorf("format = %+v", tf.Format)
	}
	joined := strings.Join(tf.Warnings, " | ")
	if !strings.Contains(joined, "Matches less") || !strings.Contains(joined, `"Restored after 4K"`) || !strings.Contains(joined, "edition pattern") {
		t.Errorf("warnings = %v", tf.Warnings)
	}
	checkMatches(t, tf.Format,
		[]string{"Movie.1990.Remastered.1080p.BluRay.x264-GRP"},
		[]string{"Movie.1990.4K.Remastered.2160p.BluRay.x265-GRP", "Movie.1990.1080p.BluRay.x264-GRP"})
}

// A Radarr/Sonarr export (fields as a list) with several formats: alternatives fold, an
// unsupported required spec drops its format, a built-in's name is renamed, and an
// exclusion that can't be checked is left out with a loud warning.
func TestImportTrashExportShape(t *testing.T) {
	got := importFixture(t, "radarr-export.json", MediaMovie)
	if len(got.Formats) != 4 || len(got.Warnings) != 0 {
		t.Fatalf("formats = %+v, warnings = %v", got.Formats, got.Warnings)
	}
	repack := got.Formats[0]
	if want := []Condition{{Type: CondReleaseTitle, Value: `(?:\bRepack\b)|(?:\bProper\b)`}}; !reflect.DeepEqual(repack.Format.Conditions, want) {
		t.Errorf("repack = %+v", repack.Format)
	}
	checkMatches(t, repack.Format, []string{"Movie.2020.PROPER.1080p.WEB-DL-GRP", "Movie.2020.REPACK.1080p.WEB-DL-GRP"}, []string{"Movie.2020.1080p.WEB-DL-GRP"})

	french := got.Formats[1]
	if !reflect.DeepEqual(french.Format.Conditions, []Condition{{Type: CondLanguage, Value: "fr"}}) {
		t.Errorf("french = %+v", french.Format)
	}
	checkMatches(t, french.Format, []string{"Film.2019.FRENCH.1080p.WEB.H264-GRP"}, []string{"Film.2019.1080p.WEB.H264-GRP"})

	av1 := got.Formats[2]
	if av1.Format.Name != "AV1 (TRaSH)" || len(av1.Warnings) != 1 || !strings.Contains(av1.Warnings[0], "built-in") {
		t.Errorf("av1 = %+v", av1)
	}

	scene := got.Formats[3]
	if scene.Format.Match != MatchAny || !reflect.DeepEqual(scene.Format.Conditions, []Condition{
		{Type: CondSource, Value: "WEB-DL"}, {Type: CondSource, Value: "CAM", Negate: true},
	}) {
		t.Errorf("scene = %+v", scene.Format)
	}
	if len(scene.Warnings) != 1 || !strings.Contains(scene.Warnings[0], "Matches MORE") {
		t.Errorf("scene warnings = %v", scene.Warnings)
	}
}

// The parser's traits: a BR-DISK modifier becomes a full-disc condition, a stated language a
// language condition, and "not English" is refused — names rarely state English, so it
// would hit nearly every release.
func TestImportTrashTraits(t *testing.T) {
	got := importFixture(t, "traits.json", MediaMovie)
	if len(got.Formats) != 2 {
		t.Fatalf("formats = %+v, warnings = %v", got.Formats, got.Warnings)
	}
	disc := got.Formats[0]
	if !reflect.DeepEqual(disc.Format.Conditions, []Condition{{Type: CondFullDisc}}) {
		t.Errorf("disc = %+v", disc.Format)
	}
	checkMatches(t, disc.Format, []string{"Movie.2020.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GRP"}, []string{"Movie.2020.1080p.BluRay.x264-GRP"})
	german := got.Formats[1]
	if !reflect.DeepEqual(german.Format.Conditions, []Condition{{Type: CondLanguage, Value: "de"}, {Type: CondFullDisc, Negate: true}}) {
		t.Errorf("german = %+v", german.Format)
	}
	checkMatches(t, german.Format, []string{"Movie.2020.GERMAN.DL.1080p.BluRay.x264-GRP"}, []string{"Movie.2020.1080p.BluRay.x264-GRP"})
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], `"Language: Not English"`) || !strings.Contains(got.Warnings[0], "English") {
		t.Errorf("warnings = %v", got.Warnings)
	}
}

func TestImportTrashRejectsWhatIsntJSON(t *testing.T) {
	for _, in := range []string{"", "not json", `{"name":"x"}`, `[{"name":"x","specifications":[]}`} {
		if _, err := ImportTrashCF([]byte(in), MediaMovie); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
	got, err := ImportTrashCF([]byte(`{"name":"Empty","specifications":[]}`), MediaMovie)
	if err != nil || len(got.Formats) != 0 || len(got.Warnings) != 1 {
		t.Errorf("empty format: %+v, %v", got, err)
	}
}

func TestLiteralNames(t *testing.T) {
	cases := map[string][]string{
		"^(FLUX)$":             {"FLUX"},
		"^(Tigole|QxR)$":       {"Tigole", "QxR"},
		`\b(D-Z0N3)\b`:         {"D-Z0N3"},
		`^(W4NK3R|HQ\.Mixed)$`: {"W4NK3R", "HQ.Mixed"},
	}
	for in, want := range cases {
		if got, ok := literalNames(in); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{`^(FLUX)[-_.]?$`, `^(.*)$`, `(a)|(b)`} {
		if _, ok := literalNames(in); ok {
			t.Errorf("%s read as names", in)
		}
	}
}
