package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Book naming: how an ebook or audiobook edition's folder and files are named, from a
// template the user can set (Settings → Naming & metadata) the way movies and TV have
// one. The defaults are exactly what Arrmada always did — "<Author>/<Title>/<Title>.ext",
// a multi-file audiobook keeping its files' own names — so an install that never touches
// the setting names nothing differently.
//
// A template is text with {Tokens} in it. "/" starts a folder. Two ways to keep an empty
// value from leaving debris behind:
//
//   - Implicitly: a folder that comes out empty disappears, and a separator left dangling
//     by an empty token goes too — "{Author}/{Series}/{Series Position} - {Title}" for a
//     book with no series is "Author/Title", with no stray " - ".
//   - Explicitly, with <optional parts>: "<…>" is only written when every token inside it
//     has a value, and "<A|B>" writes A when it can and B otherwise —
//     "<{Series}|Standalone>/" puts a book with no series in a "Standalone" folder.
//
// < > and | can't appear in a file name anyway, so they're free to mean this. Every value
// is cleaned the way movie names are (library.clean: no characters a filesystem refuses,
// no "/" smuggled in by a title), so a template can only ever make folders inside the
// library folder.

// The default book naming, which is what Arrmada always did.
const (
	DefaultAudiobookFolder = "{Author}/{Title}"
	DefaultAudiobookFile   = "{Title}"
	DefaultAudiobookTrack  = "{Original}"
	DefaultEbookFolder     = "{Author}/{Title}"
	DefaultEbookFile       = "{Title}"
)

// BookNaming is one kind of book's naming: the folder (with "/" between levels), the file
// of a single-file edition, and each file of a multi-file audiobook (Track). Ebooks are
// always one file, so their Track is unused.
type BookNaming struct {
	Folder string `json:"folder"`
	File   string `json:"file"`
	Track  string `json:"track,omitempty"`
}

// DefaultAudiobookNaming / DefaultEbookNaming are the built-in schemes.
var (
	DefaultAudiobookNaming = BookNaming{Folder: DefaultAudiobookFolder, File: DefaultAudiobookFile, Track: DefaultAudiobookTrack}
	DefaultEbookNaming     = BookNaming{Folder: DefaultEbookFolder, File: DefaultEbookFile}
)

// BookNamingPreset is a ready-made scheme the settings page offers.
type BookNamingPreset struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Audiobook   BookNaming `json:"audiobook"`
	Ebook       BookNaming `json:"ebook"`
}

// BookNamingPresets are the schemes offered as one click. The first is the default.
var BookNamingPresets = []BookNamingPreset{
	{
		ID: "author-title", Label: "Author/Title (default)",
		Description: "One folder per author, one per book inside it. What Arrmada has always done.",
		Audiobook:   DefaultAudiobookNaming, Ebook: DefaultEbookNaming,
	},
	{
		ID: "audiobookshelf", Label: "Audiobookshelf (Author/Series/Title)",
		Description: "The layout Audiobookshelf reads best: a series folder under the author, the book's place in the series before its title. A book in no series sits straight under its author.",
		Audiobook:   BookNaming{Folder: "{Author}/{Series}/{Series Position} - {Title}", File: "{Title}", Track: DefaultAudiobookTrack},
		Ebook:       BookNaming{Folder: "{Author}/{Series}/{Series Position} - {Title}", File: "{Title}"},
	},
}

// BookNamingProvider supplies the book naming in effect, read fresh on every import so a
// settings change applies to the next one.
type BookNamingProvider interface {
	AudiobookNaming() BookNaming
	EbookNaming() BookNaming
}

// BookName is everything a book edition's folder and file names can be made of.
type BookName struct {
	Author         string
	Title          string
	Series         string
	SeriesPosition float64 // 0 = not known
	Year           int
	Narrator       string
	// Edition is an audiobook version's label ("Full cast"); "" for the standard edition.
	// A version always gets its own folder: a scheme that doesn't name {Edition} has
	// " (<label>)" added to its last folder and its file, which is the name versions have
	// always had.
	Edition string
	// VersionID is the audiobook version's id, used only to name a version whose label
	// has nothing a file name can hold.
	VersionID int64
}

// Where a template is used. Each place allows different tokens.
type bookTemplatePlace int

const (
	placeFolder bookTemplatePlace = iota
	placeFile
	placeTrack
)

// bookToken is one token's canonical name.
const (
	tokAuthor     = "Author"
	tokAuthorSort = "Author Sort"
	tokTitle      = "Title"
	tokSeries     = "Series"
	tokPosition   = "Series Position"
	tokYear       = "Year"
	tokNarrator   = "Narrator"
	tokEdition    = "Edition"
	tokFormat     = "Format"
	tokPart       = "Part"
	tokPartCount  = "Part Count"
	tokOriginal   = "Original"
)

// BookTokens lists the tokens in the order the settings page shows them.
var BookTokens = []string{tokAuthor, tokAuthorSort, tokTitle, tokSeries, tokPosition, tokYear, tokNarrator, tokEdition, tokFormat, tokPart, tokPartCount, tokOriginal}

// bookTokenSpellings maps a token's name, folded (lower case, no spaces, dashes or
// underscores), to its canonical name. A few other tools' spellings are accepted too.
var bookTokenSpellings = map[string]string{
	"author": tokAuthor, "authorsort": tokAuthorSort, "title": tokTitle, "series": tokSeries,
	"seriesposition": tokPosition, "seriesindex": tokPosition, "seriesnumber": tokPosition,
	"year": tokYear, "narrator": tokNarrator, "edition": tokEdition, "format": tokFormat,
	"part": tokPart, "track": tokPart, "partcount": tokPartCount,
	"original": tokOriginal, "originalfilename": tokOriginal,
}

// bookVocab is the book tokens for the naming engine (naming_engine.go).
var bookVocab = tokenVocab{
	spellings: bookTokenSpellings,
	tokens:    BookTokens,
	paddable:  map[string]bool{tokPosition: true, tokPart: true, tokPartCount: true},
	padHelp:   "{Series Position}, {Part} and {Part Count}",
}

// parseBookTemplate parses a book template, or says what's wrong with it in words the
// settings page can show.
func parseBookTemplate(s string) ([]bnNode, error) { return parseTemplate(s, bookVocab) }

// renderBookTemplate renders nodes into path segments (one for a file template).
func renderBookTemplate(nodes []bnNode, r bookRender) []string { return renderTemplate(nodes, r) }

// ValidateBookFolder / ValidateBookFile / ValidateBookTrack check a template for its place
// before it's saved, and say what's wrong in a sentence.
func ValidateBookFolder(s string) error { return validateBookTemplate(s, placeFolder) }
func ValidateBookFile(s string) error   { return validateBookTemplate(s, placeFile) }
func ValidateBookTrack(s string) error  { return validateBookTemplate(s, placeTrack) }

func validateBookTemplate(s string, place bookTemplatePlace) error {
	nodes, err := parseBookTemplate(s)
	if err != nil {
		return err
	}
	var bad string
	walkTokens(nodes, true, func(n bnNode, _ bool) {
		if bad != "" {
			return
		}
		switch {
		case place == placeFolder && (n.tok == tokPart || n.tok == tokPartCount || n.tok == tokOriginal):
			bad = "{" + n.tok + "} names a file, so it can't be in a folder name"
		case place == placeFile && (n.tok == tokPart || n.tok == tokPartCount):
			bad = "{" + n.tok + "} only means something for an audiobook in several files — use it in the track file name"
		}
	})
	if bad != "" {
		return errors.New(bad)
	}
	switch place {
	case placeFolder:
		// Every book needs a folder of its own, named after it: a book whose files sat in
		// a folder shared with other books would have them listed, played and deleted as
		// its own. So the last folder has to carry the title, outside any optional part,
		// and nothing after it may start another folder.
		if !titleEndsFolder(nodes) {
			return errors.New("the last folder must have {Title} in it (outside any < >), so every book gets a folder of its own")
		}
	case placeFile, placeTrack:
		if hasSlash(nodes) {
			return errors.New("a file name can't have / in it — folders go in the folder template")
		}
		if place == placeTrack {
			top := false
			walkTokens(nodes, true, func(n bnNode, isTop bool) {
				if isTop && (n.tok == tokPart || n.tok == tokOriginal) {
					top = true
				}
			})
			if !top {
				return errors.New("a track file name needs {Part} or {Original} (outside any < >), or every file of a book would get the same name")
			}
		}
	}
	return nil
}

// titleEndsFolder reports whether a top-level {Title} comes after the last "/".
func titleEndsFolder(nodes []bnNode) bool {
	ok := false
	for _, n := range nodes {
		switch {
		case n.group:
			for _, alt := range n.alts {
				if hasSlash(alt) {
					ok = false
				}
			}
		case n.tok == tokTitle:
			ok = true
		case n.tok == "" && strings.ContainsAny(n.lit, `/\`):
			ok = false
		}
	}
	return ok
}

// bookRender is what one render fills tokens from.
type bookRender struct {
	n        BookName
	format   string // the edition's (or the file's) format, "M4B"
	part     int    // 1-based, for a track; 0 otherwise
	parts    int
	original string // the file's own name without its extension
}

// value is a token's value, cleaned for a file name. top says the token is outside every
// optional part: there {Author} and {Title} never come out empty, as they never have.
func (r bookRender) value(n bnNode, top bool) string {
	var v string
	switch n.tok {
	case tokAuthor:
		v = cleanValue(r.n.Author)
		if v == "" && top {
			v = "Unknown Author"
		}
	case tokAuthorSort:
		v = cleanValue(AuthorSort(r.n.Author))
	case tokTitle:
		v = cleanValue(r.n.Title)
		if v == "" && top {
			v = "Unknown"
		}
	case tokSeries:
		v = cleanValue(r.n.Series)
	case tokPosition:
		if cleanValue(r.n.Series) != "" {
			v = seriesPosition(r.n.SeriesPosition, n.pad)
		}
	case tokYear:
		if r.n.Year > 0 {
			v = strconv.Itoa(r.n.Year)
		}
	case tokNarrator:
		v = cleanValue(r.n.Narrator)
	case tokEdition:
		v = cleanValue(r.n.Edition)
		if v == "" && strings.TrimSpace(r.n.Edition) != "" {
			// A label made only of characters a file name can't hold ("???") would leave
			// the version in the standard edition's folder: name it by its id instead.
			v = "Version"
			if r.n.VersionID > 0 {
				v += " " + strconv.FormatInt(r.n.VersionID, 10)
			}
		}
	case tokFormat:
		v = cleanValue(r.format)
	case tokPart:
		if r.part > 0 {
			pad := n.pad
			if pad == 0 {
				pad = max(2, len(strconv.Itoa(r.parts)))
			}
			v = fmt.Sprintf("%0*d", pad, r.part)
		}
	case tokPartCount:
		if r.parts > 0 {
			v = fmt.Sprintf("%0*d", max(n.pad, 1), r.parts)
		}
	case tokOriginal:
		v = clean(r.original)
	}
	return v
}

// seriesPosition writes a place in a series: "2", "2.5", or padded "02", "02.5".
func seriesPosition(p float64, pad int) string {
	if p <= 0 {
		return ""
	}
	s := strconv.FormatFloat(p, 'f', -1, 64)
	whole, frac, hasFrac := strings.Cut(s, ".")
	if len(whole) < pad {
		whole = strings.Repeat("0", pad-len(whole)) + whole
	}
	if hasFrac {
		return whole + "." + frac
	}
	return whole
}

// AuthorSort turns the first author's name around for sorting: "Brandon Sanderson" →
// "Sanderson, Brandon", "Martin Luther King Jr." → "King, Martin Luther Jr.". A name that
// already has a comma is taken as sorted.
func AuthorSort(author string) string {
	first := strings.TrimSpace(author)
	for _, sep := range []string{" & ", "; ", " and ", " with "} {
		if i := strings.Index(first, sep); i > 0 {
			first = strings.TrimSpace(first[:i])
		}
	}
	if strings.Contains(first, ",") {
		return first
	}
	words := strings.Fields(first)
	if len(words) < 2 {
		return first
	}
	last := len(words) - 1
	suffix := ""
	if len(words) > 2 {
		switch strings.ToLower(strings.Trim(words[last], ".,")) {
		case "jr", "sr", "ii", "iii", "iv", "phd", "md":
			suffix = " " + words[last]
			last--
		}
	}
	return words[last] + ", " + strings.Join(words[:last], " ") + suffix
}

// bookNamingOrDefault fills a blank or broken template with the default for its place, so
// a scheme saved some other way can never stop an import.
func bookNamingOrDefault(n BookNaming, audio bool) BookNaming {
	def := DefaultEbookNaming
	if audio {
		def = DefaultAudiobookNaming
	}
	if ValidateBookFolder(n.Folder) != nil {
		n.Folder = def.Folder
	}
	if ValidateBookFile(n.File) != nil {
		n.File = def.File
	}
	if !audio {
		n.Track = ""
	} else if ValidateBookTrack(n.Track) != nil {
		n.Track = def.Track
	}
	return n
}

// withEdition makes sure a version's folder and file say which version they are: a
// template that doesn't name {Edition} gets " ({Edition})" after it. Versions have always
// lived in "<Title> (<label>)", and two versions sharing a folder would be read as one.
func withEdition(tpl string, n BookName) string {
	if strings.TrimSpace(n.Edition) == "" {
		return tpl
	}
	// Only an {Edition} outside every optional part counts: one inside a part that drops
	// out (with an empty narrator, say) would leave the version in the standard folder.
	top := false
	if nodes, err := parseBookTemplate(tpl); err == nil {
		walkTokens(nodes, true, func(n bnNode, isTop bool) {
			if isTop && n.tok == tokEdition {
				top = true
			}
		})
	}
	if top {
		return tpl
	}
	return tpl + " ({Edition})"
}

// BookFolderPath renders a book's folder under root with the naming nm.
func BookFolderPath(root string, nm BookNaming, audio bool, n BookName, format string) string {
	nm = bookNamingOrDefault(nm, audio)
	nodes, err := parseBookTemplate(withEdition(nm.Folder, n))
	if err != nil {
		nodes, _ = parseBookTemplate(DefaultEbookFolder)
	}
	segs := renderBookTemplate(nodes, bookRender{n: n, format: format})
	for i := range segs {
		segs[i] = truncateName(segs[i], maxNameBytes)
	}
	if len(segs) == 0 {
		segs = []string{"Unknown"}
	}
	return filepath.Join(append([]string{root}, segs...)...)
}

// BookFileName renders a single-file edition's file name (with ext).
func BookFileName(nm BookNaming, audio bool, n BookName, srcPath string) string {
	nm = bookNamingOrDefault(nm, audio)
	ext := filepath.Ext(srcPath)
	original := strings.TrimSuffix(filepath.Base(srcPath), ext)
	name := renderOneName(withEdition(nm.File, n), bookRender{n: n, format: BookFileFormat(srcPath), original: original})
	if name == "" {
		name = cleanValue(n.Title)
		if name == "" {
			name = "Unknown"
		}
	}
	return truncateName(name, maxNameBytes-len(ext)) + ext
}

func renderOneName(tpl string, r bookRender) string {
	nodes, err := parseBookTemplate(tpl)
	if err != nil {
		return ""
	}
	return strings.Join(renderBookTemplate(nodes, r), " ")
}

// BookTrackNames names a multi-file audiobook's files. rels are the files' paths relative
// to the book (or just their names); they're numbered in play order — the natural order
// of rels, the order the audiobook server plays them in — and the result is aligned with
// rels: names[i] is rels[i]'s new file name, extension kept.
func BookTrackNames(nm BookNaming, n BookName, rels []string) []string {
	nm = bookNamingOrDefault(nm, true)
	order := make([]int, len(rels))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return NaturalLess(rels[order[a]], rels[order[b]]) })
	nodes, err := parseBookTemplate(nm.Track)
	if err != nil {
		nodes, _ = parseBookTemplate(DefaultAudiobookTrack)
	}
	out := make([]string, len(rels))
	for place, i := range order {
		ext := filepath.Ext(rels[i])
		original := strings.TrimSuffix(filepath.Base(rels[i]), ext)
		r := bookRender{n: n, format: BookFileFormat(rels[i]), part: place + 1, parts: len(rels), original: original}
		name := strings.Join(renderBookTemplate(nodes, r), " ")
		if name == "" {
			name = clean(original)
		}
		out[i] = truncateName(name, maxNameBytes-len(ext)) + ext
	}
	return out
}

// NaturalLess orders "Chapter 2" before "Chapter 10", ignoring case. It is THE play order
// of a multi-file audiobook: the audiobook server sorts an item's files with it, and a
// rename numbers tracks with it, so renaming never changes what plays when — a listening
// place is a time into the whole book, and reordered files would move it.
func NaturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingNumber(a)
			nb, restB := leadingNumber(b)
			if na != nb {
				return na < nb
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingNumber(s string) (int64, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.ParseInt(s[:i], 10, 64)
	return n, s[i:]
}

// BookNamingSample is one rendered example for the settings preview.
type BookNamingSample struct {
	Label string   `json:"label"`
	Paths []string `json:"paths"` // relative to the library folder, "/" between levels
}

// sampleBook is a made-up book the preview renders.
type sampleBook struct {
	label string
	name  BookName
	files []string
}

var audiobookSamples = []sampleBook{
	{"A book in a series", BookName{Author: "Pierce Brown", Title: "Golden Son", Series: "Red Rising Saga", SeriesPosition: 2, Year: 2015, Narrator: "Tim Gerard Reynolds"}, []string{"Golden.Son.Unabridged.m4b"}},
	{"A book in no series", BookName{Author: "Andy Weir", Title: "Project Hail Mary", Year: 2021, Narrator: "Ray Porter"}, []string{"project_hail_mary.m4b"}},
	{"An audiobook in several files", BookName{Author: "Frank Herbert", Title: "Dune", Series: "Dune", SeriesPosition: 1, Year: 1965, Narrator: "Scott Brick"}, []string{"Dune 01.mp3", "Dune 02.mp3", "Dune 10.mp3"}},
	{"A second version of a book", BookName{Author: "Brandon Sanderson", Title: "The Way of Kings", Series: "The Stormlight Archive", SeriesPosition: 1, Year: 2010, Edition: "GraphicAudio"}, []string{"The Way of Kings.m4b"}},
}

var ebookSamples = []sampleBook{
	{"A book in a series", BookName{Author: "Pierce Brown", Title: "Golden Son", Series: "Red Rising Saga", SeriesPosition: 2, Year: 2015}, []string{"golden-son.epub"}},
	{"A book in no series", BookName{Author: "Andy Weir", Title: "Project Hail Mary", Year: 2021}, []string{"Project Hail Mary.epub"}},
}

// PreviewBookNaming renders the sample books with nm, for the settings page. Templates
// are checked first; the first problem comes back as an error naming the field.
func PreviewBookNaming(nm BookNaming, audio bool) ([]BookNamingSample, *BookNamingError) {
	if err := ValidateBookFolder(nm.Folder); err != nil {
		return nil, &BookNamingError{Field: "folder", Message: err.Error()}
	}
	if err := ValidateBookFile(nm.File); err != nil {
		return nil, &BookNamingError{Field: "file", Message: err.Error()}
	}
	samples := ebookSamples
	if audio {
		if err := ValidateBookTrack(nm.Track); err != nil {
			return nil, &BookNamingError{Field: "track", Message: err.Error()}
		}
		samples = audiobookSamples
	}
	out := make([]BookNamingSample, 0, len(samples))
	for _, s := range samples {
		format := BookFileFormat(s.files[0])
		dir := BookFolderPath("", nm, audio, s.name, format)
		var paths []string
		if len(s.files) == 1 {
			paths = []string{filepath.ToSlash(filepath.Join(dir, BookFileName(nm, audio, s.name, s.files[0])))}
		} else {
			for _, name := range BookTrackNames(nm, s.name, s.files) {
				paths = append(paths, filepath.ToSlash(filepath.Join(dir, name)))
			}
		}
		out = append(out, BookNamingSample{Label: s.label, Paths: paths})
	}
	return out, nil
}

// BookNamingError is a template the settings page should mark: which field, and why.
type BookNamingError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *BookNamingError) Error() string { return e.Message }

// bookScanHint is what a library scan learns from a book folder scheme other than the
// default, to read an author and a title back out of the folders it made: the first
// folder is the author when the scheme starts with {Author} ("Author/Series/Title" would
// otherwise read the series as the author), and a "2 - " before the title is the book's
// place in its series, not part of its name. The default scheme is read exactly as it
// always was.
type bookScanHint struct {
	authorFirst    bool
	positionPrefix bool
}

func (im *Importer) bookScanHint(root string) bookScanHint {
	var tpl string
	switch root {
	case im.audiobookDir():
		tpl = im.BookNamingFor(true).Folder
	case im.ebookDir():
		tpl = im.BookNamingFor(false).Folder
	default:
		return bookScanHint{}
	}
	if tpl == DefaultAudiobookFolder || tpl == DefaultEbookFolder {
		return bookScanHint{}
	}
	nodes, err := parseBookTemplate(tpl)
	if err != nil {
		return bookScanHint{}
	}
	var h bookScanHint
	for _, n := range nodes {
		if n.group || (n.tok == "" && strings.ContainsAny(n.lit, `/\`)) {
			break // the first folder ended (or is optional, so not reliably the author)
		}
		if n.tok == tokAuthor {
			h.authorFirst = true
		}
	}
	sawPosition := false
	for _, n := range nodes {
		switch {
		case n.tok == "" && !n.group && strings.ContainsAny(n.lit, `/\`):
			sawPosition = false // a new folder starts
		case n.group:
			for _, alt := range n.alts {
				if templateHasToken(alt, tokPosition) {
					sawPosition = true
				}
			}
		case n.tok == tokPosition:
			sawPosition = true
		case n.tok == tokTitle:
			h.positionPrefix = sawPosition
		}
	}
	return h
}

// rePositionPrefix is a series place written before a title: "2 - ", "02. ", "Book 3 - ".
var rePositionPrefix = regexp.MustCompile(`^(?i:(?:book|vol(?:ume)?|#)\.?\s*)?\d+(?:\.\d+)?\s*[-–.:]\s+`)

// read applies the hint to one folder the scan found.
func (h bookScanHint) read(root, dir, author, title string) (string, string) {
	if h.authorFirst {
		if rel, err := filepath.Rel(root, dir); err == nil {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) >= 2 && parts[0] != "" && parts[0] != ".." {
				author = parts[0]
			}
		}
	}
	if h.positionPrefix {
		if t := rePositionPrefix.ReplaceAllString(title, ""); strings.TrimSpace(t) != "" {
			title = t
		}
	}
	return author, title
}
