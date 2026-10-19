package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixedBookNaming struct{ audio, ebook BookNaming }

func (f fixedBookNaming) AudiobookNaming() BookNaming { return f.audio }
func (f fixedBookNaming) EbookNaming() BookNaming     { return f.ebook }

// writeNamed writes a file whose content is its own name, so a test can tell where it went.
func writeNamed(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The pre-template placement, kept here to prove the default renders exactly the same.
func oldBookDir(root, author, title string) string {
	a := clean(author)
	if a == "" {
		a = "Unknown Author"
	}
	t := clean(title)
	if t == "" {
		t = "Unknown"
	}
	return filepath.Join(root, a, t)
}

// An install that never sets the book naming names every book exactly as before: the
// folder, a single file, a multi-file audiobook's files, and an audiobook version's
// "<Title> (<label>)" folder and file.
func TestBookNamingDefaultUnchanged(t *testing.T) {
	cases := []struct{ author, title, label string }{
		{"Brandon Sanderson", "The Way of Kings", ""},
		{"", "Golden Son", ""},
		{"J.R.R. Tolkien", "", ""},
		{"Pierce Brown", "Golden Son: Book 2 of the Red Rising Saga", ""},
		{"AC/DC & Friends", "What? A <Book>: \"Quoted\" | Piped * Starred", ""},
		{"  Spaced   Author  ", "  Spaced   Title  ", ""},
		{"Mr. Jr.", "Ends with a dot.", ""},
		{"Jay Kristoff", "Empire of the Vampire", "Full cast"},
		{"Jay Kristoff", "Empire of the Vampire", "GraphicAudio: Dramatized"},
		{"Ünïcødé Àuthor", "日本語のタイトル", ""},
		{"- Leading Dash", "[Bracketed] (Paren) - - Dashes -", ""},
		// Long names that fit the 255-byte limit render byte for byte as before: 125
		// Cyrillic letters are 250 bytes, 251 with ".m4b" four more.
		{strings.Repeat("Ж", 120), strings.Repeat("Ж", 125), ""},
		{"Автор", strings.Repeat("Ж", 122), ""},
	}
	for _, audio := range []bool{true, false} {
		for _, c := range cases {
			n := BookName{Author: c.author, Title: c.title, Series: "Some Series", SeriesPosition: 3, Year: 2001, Narrator: "N", Edition: c.label}
			oldTitle := c.title
			if c.label != "" {
				oldTitle = fmt.Sprintf("%s (%s)", c.title, c.label)
			}
			if c.title == "" && c.label != "" {
				continue // "Unknown (label)" vs "(label)": no title and a version can't happen
			}
			nm := DefaultEbookNaming
			if audio {
				nm = DefaultAudiobookNaming
			}
			got := BookFolderPath("/lib", nm, audio, n, "M4B")
			if want := oldBookDir("/lib", c.author, oldTitle); got != want {
				t.Errorf("folder for %+v:\n got  %q\n want %q", c, got, want)
			}
			file := BookFileName(nm, audio, n, "/dl/whatever.release.m4b")
			wantFile := clean(oldTitle)
			if wantFile == "" {
				wantFile = "Unknown"
			}
			if file != wantFile+".m4b" {
				t.Errorf("file for %+v: got %q, want %q", c, file, wantFile+".m4b")
			}
		}
	}
	rels := []string{"01 - Chapter One.mp3", "Chapter  10 :Extra.mp3", "chapter 2.MP3"}
	got := BookTrackNames(DefaultAudiobookNaming, BookName{Title: "x"}, rels)
	for i, r := range rels {
		ext := filepath.Ext(r)
		if want := clean(strings.TrimSuffix(r, ext)) + ext; got[i] != want {
			t.Errorf("track %q: got %q, want %q (multi-file books keep their names)", r, got[i], want)
		}
	}
	im := NewImporter("/lib", quiet())
	if im.BookNamingFor(true) != DefaultAudiobookNaming || im.BookNamingFor(false) != DefaultEbookNaming {
		t.Errorf("an importer with no book naming must use the defaults, got %+v / %+v", im.BookNamingFor(true), im.BookNamingFor(false))
	}
	if BookNamingPresets[0].Audiobook != DefaultAudiobookNaming || BookNamingPresets[0].Ebook != DefaultEbookNaming {
		t.Error("the first preset is the default scheme")
	}
}

func render(t *testing.T, folder, file string, n BookName) string {
	t.Helper()
	nm := BookNaming{Folder: folder, File: file, Track: DefaultAudiobookTrack}
	if err := ValidateBookFolder(folder); err != nil {
		t.Fatalf("folder %q: %v", folder, err)
	}
	if err := ValidateBookFile(file); err != nil {
		t.Fatalf("file %q: %v", file, err)
	}
	p := filepath.Join(BookFolderPath("", nm, true, n, "M4B"), BookFileName(nm, true, n, "src.m4b"))
	return filepath.ToSlash(p)
}

// Optional parts collapse cleanly: no empty folder, no stray " - " or "()".
func TestBookTemplateCollapse(t *testing.T) {
	series := BookName{Author: "Pierce Brown", Title: "Golden Son", Series: "Red Rising Saga", SeriesPosition: 2, Year: 2015, Narrator: "Tim Gerard Reynolds"}
	alone := BookName{Author: "Andy Weir", Title: "Project Hail Mary"}
	noPos := BookName{Author: "A", Title: "T", Series: "S"}
	cases := []struct {
		folder, file string
		n            BookName
		want         string
	}{
		// The issue's example, implicitly: the series folder and the position prefix go.
		{"{Author}/{Series}/{Series Position} - {Title}", "{Title}", series, "Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son.m4b"},
		{"{Author}/{Series}/{Series Position} - {Title}", "{Title}", alone, "Andy Weir/Project Hail Mary/Project Hail Mary.m4b"},
		{"{Author}/{Series}/{Series Position} - {Title}", "{Title}", noPos, "A/S/T/T.m4b"},
		{"{Author}/{Title} ({Year})", "{Title} - ({Year}) [{Narrator}]", alone, "Andy Weir/Project Hail Mary/Project Hail Mary.m4b"},
		{"{Author}/{Title} ({Year})", "{Title} - ({Year}) [{Narrator}]", series, "Pierce Brown/Golden Son (2015)/Golden Son - (2015) [Tim Gerard Reynolds].m4b"},
		// Zero padding.
		{"{Author}/{Series}/{Series Position:00} - {Title}", "{Title}", series, "Pierce Brown/Red Rising Saga/02 - Golden Son/Golden Son.m4b"},
		// The issue reporter's layout, with explicit optional parts and a fallback.
		{"{Author}/<{Series}|Standalone>/<{Series Position} >{Title}", "<{Series Position} - >{Title}< - ({Year})>", series,
			"Pierce Brown/Red Rising Saga/2 Golden Son/2 - Golden Son - (2015).m4b"},
		{"{Author}/<{Series}|Standalone>/<{Series Position} >{Title}", "<{Series Position} - >{Title}< - ({Year})>", alone,
			"Andy Weir/Standalone/Project Hail Mary/Project Hail Mary.m4b"},
		// An optional part with a token inside stays out when the token is empty, even
		// with text around it that implicit tidying wouldn't catch.
		{"{Author}/<{Series} #{Series Position} - >{Title}", "{Title}", noPos, "A/T/T.m4b"},
		{"{Author}/<Book {Series Position} of {Series} - >{Title}", "{Title}", series, "Pierce Brown/Book 2 of Red Rising Saga - Golden Son/Golden Son.m4b"},
		// Author Sort, and the author's fallback.
		{"{Author Sort}/{Title}", "{Title}", series, "Brown, Pierce/Golden Son/Golden Son.m4b"},
		{"{Author}/{Title}", "{Title}", BookName{Title: "X"}, "Unknown Author/X/X.m4b"},
		{"<{Author}|Anonymous>/{Title}", "{Title}", BookName{Title: "X"}, "Anonymous/X/X.m4b"},
		// Token names are forgiving about case and spaces.
		{"{author}/{seriesposition} - {TITLE}", "{title}", series, "Pierce Brown/2 - Golden Son/Golden Son.m4b"},
	}
	for _, c := range cases {
		if got := render(t, c.folder, c.file, c.n); got != c.want {
			t.Errorf("%q + %q for %q:\n got  %q\n want %q", c.folder, c.file, c.n.Title, got, c.want)
		}
	}
}

// Values are sanitised like movie names, and no value can climb out of the library or
// make a hidden or empty folder.
func TestBookTemplateSanitises(t *testing.T) {
	n := BookName{Author: "../../etc", Title: "Who/What: a \"Book\"?", Series: "..", SeriesPosition: 1}
	got := render(t, "{Author}/{Series}/{Title}", "{Title}", n)
	// "../../etc" loses its slashes, and then its leading dots: a name starting with a dot
	// would be a hidden folder (MUS-34 review, 2026-10-20).
	if want := "etc/WhoWhat a Book/WhoWhat a Book.m4b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	dots := render(t, "{Author}/{Title}", "{Title}", BookName{Author: "..", Title: "."})
	if dots != "Unknown Author/Unknown/Unknown.m4b" {
		t.Errorf("dot-only names must fall back, got %q", dots)
	}
	for _, seg := range strings.Split(got, "/") {
		if seg == ".." || seg == "." || seg == "" {
			t.Errorf("unsafe segment %q in %q", seg, got)
		}
	}
	long := strings.Repeat("Very Long Title ", 40)
	name := BookFileName(DefaultAudiobookNaming, true, BookName{Title: long}, "a.m4b")
	if len(name) > 255 || !strings.HasSuffix(name, ".m4b") {
		t.Errorf("a long title must still fit a file name: %d bytes", len(name))
	}
	if dir := BookFolderPath("/lib", DefaultAudiobookNaming, true, BookName{Author: "A", Title: long}, ""); len(filepath.Base(dir)) > 255 {
		t.Errorf("a long title must still fit a folder name: %d bytes", len(filepath.Base(dir)))
	}
}

func TestBookTemplateValidation(t *testing.T) {
	bad := []struct {
		place bookTemplatePlace
		tpl   string
	}{
		{placeFolder, ""},
		{placeFolder, "{Author}/{Nope}/{Title}"},
		{placeFolder, "{Author/{Title}"},
		{placeFolder, "{Author}}/{Title}"},
		{placeFolder, "{Author}/<{Series}/{Title}"},
		{placeFolder, "{Author}/{Series}>/{Title}"},
		{placeFolder, "{Author}|{Title}"},
		{placeFolder, "{Author}/{Title}/{Series}"}, // the last folder must be the book's
		{placeFolder, "{Author}/<{Title}>"},        // and its title can't be optional
		{placeFolder, "{Title}</{Series}>"},
		{placeFolder, "{Author}/{Title} {Part}"},
		{placeFolder, "{Author}/{Title} {Year:00}"},
		{placeFolder, "{Author}/{Title} {Series Position:0a}"},
		{placeFile, "{Author}/{Title}"},
		{placeFile, "{Title} {Part}"},
		{placeTrack, "{Title}"},
		{placeTrack, "{Title} <{Part}>"},
		{placeTrack, "CD/{Part}"},
	}
	for _, c := range bad {
		if err := validateBookTemplate(c.tpl, c.place); err == nil {
			t.Errorf("place %d: %q should be refused", c.place, c.tpl)
		}
	}
	good := []struct {
		place bookTemplatePlace
		tpl   string
	}{
		{placeFolder, DefaultAudiobookFolder},
		{placeFolder, "{Title}"},
		{placeFolder, "{Author}/<{Series}/>{Series Position:000} - {Title}"},
		{placeFolder, "{Author}/{Format}/{Title} [{Edition}]"},
		{placeFile, "{Author} - {Title} ({Original})"},
		{placeTrack, "{Title} - Part {Part:000} of {Part Count}"},
		{placeTrack, "{Original}"},
	}
	for _, c := range good {
		if err := validateBookTemplate(c.tpl, c.place); err != nil {
			t.Errorf("place %d: %q should be fine: %v", c.place, c.tpl, err)
		}
	}
}

func TestBookPresetsValid(t *testing.T) {
	for _, p := range BookNamingPresets {
		for _, nm := range []BookNaming{p.Audiobook, p.Ebook} {
			if err := ValidateBookFolder(nm.Folder); err != nil {
				t.Errorf("%s folder: %v", p.ID, err)
			}
			if err := ValidateBookFile(nm.File); err != nil {
				t.Errorf("%s file: %v", p.ID, err)
			}
		}
		if err := ValidateBookTrack(p.Audiobook.Track); err != nil {
			t.Errorf("%s track: %v", p.ID, err)
		}
		if _, perr := PreviewBookNaming(p.Audiobook, true); perr != nil {
			t.Errorf("%s preview: %v", p.ID, perr)
		}
	}
	abs := BookNamingPresets[1]
	if abs.ID != "audiobookshelf" {
		t.Fatalf("second preset = %q", abs.ID)
	}
	samples, _ := PreviewBookNaming(abs.Audiobook, true)
	if got := samples[0].Paths[0]; got != "Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son.m4b" {
		t.Errorf("Audiobookshelf preset sample = %q", got)
	}
	if got := samples[1].Paths[0]; got != "Andy Weir/Project Hail Mary/Project Hail Mary.m4b" {
		t.Errorf("Audiobookshelf preset, no series = %q", got)
	}
	if got := samples[3].Paths[0]; got != "Brandon Sanderson/The Stormlight Archive/1 - The Way of Kings (GraphicAudio)/The Way of Kings (GraphicAudio).m4b" {
		t.Errorf("a version gets its own folder = %q", got)
	}
	if _, perr := PreviewBookNaming(BookNaming{Folder: "{Author}", File: "{Title}", Track: "{Part}"}, true); perr == nil || perr.Field != "folder" {
		t.Errorf("a folder with no title must be refused naming the field, got %+v", perr)
	}
}

// A multi-file audiobook's tracks are numbered in play order (natural order), padded to
// the count, whatever order they arrive in.
func TestBookTrackNamesPlayOrder(t *testing.T) {
	nm := BookNaming{Folder: DefaultAudiobookFolder, File: DefaultAudiobookFile, Track: "{Title} - Part {Part}"}
	rels := []string{"Dune 10.mp3", "Dune 2.mp3", "dune 1.mp3", "CD2/Dune 1.mp3"}
	got := BookTrackNames(nm, BookName{Title: "Dune"}, rels)
	// Natural order, as the audiobook server plays them: "CD2/…" sorts before "dune …".
	want := []string{"Dune - Part 04.mp3", "Dune - Part 03.mp3", "Dune - Part 02.mp3", "Dune - Part 01.mp3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%q → %q, want %q", rels[i], got[i], want[i])
		}
	}
	many := make([]string, 120)
	for i := range many {
		many[i] = fmt.Sprintf("t%d.mp3", i+1)
	}
	if got := BookTrackNames(nm, BookName{Title: "X"}, many); got[0] != "X - Part 001.mp3" || got[119] != "X - Part 120.mp3" {
		t.Errorf("120 tracks pad to three digits: %q … %q", got[0], got[119])
	}
	if got := BookTrackNames(BookNaming{Track: "{Part:0} of {Part Count}"}, BookName{Title: "X"}, []string{"a.mp3", "b.mp3"}); got[1] != "2 of 2.mp3" {
		t.Errorf("explicit padding wins: %q", got[1])
	}
}

func TestSeriesPositionAndAuthorSort(t *testing.T) {
	for _, c := range []struct {
		p    float64
		pad  int
		want string
	}{{2, 0, "2"}, {2, 2, "02"}, {2.5, 2, "02.5"}, {12, 1, "12"}, {0, 2, ""}, {101, 2, "101"}} {
		if got := seriesPosition(c.p, c.pad); got != c.want {
			t.Errorf("seriesPosition(%v, %d) = %q, want %q", c.p, c.pad, got, c.want)
		}
	}
	for in, want := range map[string]string{
		"Brandon Sanderson":             "Sanderson, Brandon",
		"J.R.R. Tolkien":                "Tolkien, J.R.R.",
		"Martin Luther King Jr.":        "King, Martin Luther Jr.",
		"Sanderson, Brandon":            "Sanderson, Brandon",
		"Plato":                         "Plato",
		"Neil Gaiman & Terry Pratchett": "Gaiman, Neil",
		"":                              "",
	} {
		if got := AuthorSort(in); got != want {
			t.Errorf("AuthorSort(%q) = %q, want %q", in, got, want)
		}
	}
}

// New imports follow the scheme: a single file, a multi-file audiobook numbered in play
// order, and a version in a folder of its own.
func TestImportBookFollowsScheme(t *testing.T) {
	base := t.TempDir()
	lib, src := filepath.Join(base, "lib"), filepath.Join(base, "dl")
	for _, d := range []string{lib, src} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	im := NewImporter(lib, quiet())
	abs := BookNamingPresets[1]
	abs.Audiobook.Track = "{Title} - {Part}"
	im.SetBookNaming(fixedBookNaming{audio: abs.Audiobook, ebook: abs.Ebook})
	n := BookName{Author: "Pierce Brown", Title: "Golden Son", Series: "Red Rising Saga", SeriesPosition: 2}

	single, err := im.ImportBook(n, []FoundFile{{Path: mustWrite(t, src, "golden.son.m4b")}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(lib, "Pierce Brown", "Red Rising Saga", "2 - Golden Son", "Golden Son.m4b"); single.TargetPath != want {
		t.Errorf("single file at %q, want %q", single.TargetPath, want)
	}

	multiSrc := filepath.Join(src, "multi")
	var files []FoundFile
	for _, name := range []string{"10.mp3", "2.mp3", "1.mp3"} {
		files = append(files, FoundFile{Path: writeNamed(t, multiSrc, name)})
	}
	dune := BookName{Author: "Frank Herbert", Title: "Dune"}
	multi, err := im.ImportBook(dune, files)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(lib, "Frank Herbert", "Dune"); multi.TargetPath != want || multi.FileCount != 3 {
		t.Fatalf("multi-file at %q (%d files), want %q", multi.TargetPath, multi.FileCount, want)
	}
	for src, want := range map[string]string{"1.mp3": "Dune - 01.mp3", "2.mp3": "Dune - 02.mp3", "10.mp3": "Dune - 03.mp3"} {
		b, err := os.ReadFile(filepath.Join(multi.TargetPath, want))
		if err != nil || string(b) != src {
			t.Errorf("%s should be %s: %q %v", src, want, b, err)
		}
	}

	v := n
	v.Edition = "Full cast"
	ver, err := im.ImportBook(v, []FoundFile{{Path: mustWrite(t, src, "fullcast.m4b")}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(lib, "Pierce Brown", "Red Rising Saga", "2 - Golden Son (Full cast)", "Golden Son (Full cast).m4b"); ver.TargetPath != want {
		t.Errorf("version at %q, want %q", ver.TargetPath, want)
	}
	if got := im.BookLibraryFiles(n, "", "M4B"); len(got) != 1 || got[0].Path != single.TargetPath {
		t.Errorf("rescan should find the book in its scheme folder: %+v", got)
	}
}

// A scan of a library laid out by a scheme reads the author from the top folder and drops
// the series place from the title; the default layout is read as it always was.
func TestFindBookFoldersReadsTheScheme(t *testing.T) {
	lib := t.TempDir()
	mustWrite(t, filepath.Join(lib, "Pierce Brown", "Red Rising Saga", "2 - Golden Son"), "Golden Son.m4b")
	mustWrite(t, filepath.Join(lib, "Andy Weir", "Project Hail Mary"), "Project Hail Mary.m4b")
	read := func(im *Importer) map[string]string {
		out := map[string]string{}
		for _, f := range im.FindBookFoldersIn(lib) {
			out[f.Title] = f.Author
		}
		return out
	}
	im := NewImporter(lib, quiet())
	if got := read(im); got["2 - Golden Son"] != "Red Rising Saga" || got["Project Hail Mary"] != "Andy Weir" {
		t.Errorf("default scheme reads folders as before: %v", got)
	}
	abs := BookNamingPresets[1]
	im.SetBookNaming(fixedBookNaming{audio: abs.Audiobook, ebook: abs.Ebook})
	if got := read(im); got["Golden Son"] != "Pierce Brown" || got["Project Hail Mary"] != "Andy Weir" {
		t.Errorf("Audiobookshelf scheme: %v", got)
	}
}

// A name over the 255-byte limit (which could never have been created) is shortened to fit,
// on a character boundary, keeping its extension.
func TestBookNamesOverTheLimitAreShortened(t *testing.T) {
	long := strings.Repeat("Ж", 200) // 400 bytes
	dir := BookFolderPath("/r", DefaultAudiobookNaming, true, BookName{Author: "A", Title: long}, "M4B")
	if b := filepath.Base(dir); len(b) > 255 || !strings.HasPrefix(long, b) {
		t.Errorf("folder %d bytes", len(b))
	}
	f := BookFileName(DefaultAudiobookNaming, true, BookName{Title: long}, "x.m4b")
	if len(f) > 255 || !strings.HasSuffix(f, ".m4b") {
		t.Errorf("file %d bytes", len(f))
	}
}

// A version always gets a folder apart from the standard edition's: an {Edition} inside an
// optional part that drops out doesn't count, and a label with nothing printable is named
// by the version's id.
func TestVersionFolderNeverSharesTheStandardOne(t *testing.T) {
	nm := BookNaming{Folder: "{Author}/{Title}< [{Narrator} {Edition}]>", File: "{Title}", Track: "{Original}"}
	std := BookFolderPath("/r", nm, true, BookName{Author: "A", Title: "T"}, "")
	ver := BookFolderPath("/r", nm, true, BookName{Author: "A", Title: "T", Edition: "Full cast", VersionID: 3}, "")
	if std == ver || filepath.Base(ver) != "T (Full cast)" {
		t.Errorf("standard %q, version %q", std, ver)
	}
	odd := BookFolderPath("/r", DefaultAudiobookNaming, true, BookName{Author: "A", Title: "T", Edition: "???", VersionID: 9}, "")
	if filepath.Base(odd) != "T (Version 9)" {
		t.Errorf("unprintable label: %q", odd)
	}
}
