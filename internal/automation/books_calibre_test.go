package automation

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/store"
)

// calibreCatalogue is a book catalogue with ISBN lookups that records what it was asked.
// Anything else it's asked panics (nil embed).
type calibreCatalogue struct {
	metadata.BookProvider
	mu      sync.Mutex
	byISBN  map[string]metadata.BookResult
	search  map[string][]metadata.BookResult // by query
	details map[string]metadata.BookDetails  // by key
	isbns   []string
	queries []string
}

func (*calibreCatalogue) Available() bool { return true }

func (c *calibreCatalogue) SearchBooks(_ context.Context, q string) ([]metadata.BookResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	return c.search[q], nil
}

func (c *calibreCatalogue) GetBook(_ context.Context, key string) (*metadata.BookDetails, error) {
	d, ok := c.details[key]
	if !ok {
		return &metadata.BookDetails{BookResult: metadata.BookResult{Key: key}}, nil
	}
	return &d, nil
}

func (c *calibreCatalogue) BookByISBN(_ context.Context, isbn string) (*metadata.BookResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isbns = append(c.isbns, isbn)
	if r, ok := c.byISBN[isbn]; ok {
		return &r, nil
	}
	return nil, nil
}

func (*calibreCatalogue) SeriesBooks(context.Context, string) (*metadata.SeriesInfo, error) {
	return nil, metadata.ErrNotSupported
}
func (*calibreCatalogue) AuthorDetail(context.Context, string) (*metadata.AuthorResult, error) {
	return nil, metadata.ErrNotSupported
}
func (*calibreCatalogue) SimilarBooks(context.Context, string) ([]metadata.BookResult, error) {
	return nil, metadata.ErrNotSupported
}

const goodOmensOPF = `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
    <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
        <dc:identifier opf:scheme="calibre" id="calibre_id">12</dc:identifier>
        <dc:identifier opf:scheme="uuid" id="uuid_id">3a1c3e1b-7d0e-4b8e-9d55-5b8f0e0f2c11</dc:identifier>
        <dc:title>Good Omens</dc:title>
        <dc:creator opf:file-as="Pratchett, Terry &amp; Gaiman, Neil" opf:role="aut">Terry Pratchett</dc:creator>
        <dc:creator opf:file-as="Pratchett, Terry &amp; Gaiman, Neil" opf:role="aut">Neil Gaiman</dc:creator>
        <dc:date>1990-05-01T00:00:00+00:00</dc:date>
        <dc:identifier opf:scheme="ISBN">9780060853983</dc:identifier>
        <dc:language>eng</dc:language>
        <meta name="calibre:series" content="Omens"/>
        <meta name="calibre:series_index" content="1"/>
    </metadata>
</package>
`

// The OPF has no ISBN: the title and authors from it are the lookup, not the folder name.
const goldenSonOPF = `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
    <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
        <dc:identifier opf:scheme="calibre">42</dc:identifier>
        <dc:title>Golden Son</dc:title>
        <dc:creator opf:file-as="Brown, Pierce" opf:role="aut">Pierce Brown</dc:creator>
        <meta name="calibre:series" content="Red Rising Saga"/>
        <meta name="calibre:series_index" content="2"/>
    </metadata>
</package>
`

// calibreJPEG is the start of a JPEG; enough for the cover check.
var calibreJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F', 0}

// calibreSnapshot is every file under root with its size, mode and modified time: what a
// read-only scan must leave exactly as it was.
func calibreSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := d.Info()
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel+"|"+fi.Mode().String()+"|"+fi.ModTime().UTC().Format(time.RFC3339Nano)+"|"+strings.Repeat("#", int(min(fi.Size(), 64))))
		return nil
	})
	sort.Strings(out)
	return out
}

func writeCalibreFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A library scan over a Calibre library: the OPF's ISBN wins (even over a misleading
// folder name), then the OPF's title and author; Calibre's " (123)" never reaches a
// lookup; the OPF's series fills in what the catalogue lacks; cover.jpg is used only for
// a book the catalogue has no cover for; and nothing in the Calibre folder changes.
func TestBookScanReadsCalibreLibrary(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	// Good Omens: a folder name that would look up something else, an OPF with the ISBN,
	// and a cover.jpg — but the catalogue has a cover, so cover.jpg isn't used.
	goodOmens := filepath.Join(root, "Terry Pratchett", "Misfiled Name (12)")
	writeCalibreFile(t, filepath.Join(goodOmens, "Misfiled Name - Terry Pratchett.epub"), []byte("PK epub"))
	writeCalibreFile(t, filepath.Join(goodOmens, "metadata.opf"), []byte(goodOmensOPF))
	writeCalibreFile(t, filepath.Join(goodOmens, "cover.jpg"), calibreJPEG)
	// Golden Son: OPF without an ISBN; the catalogue has no cover, so cover.jpg is.
	golden := filepath.Join(root, "Pierce Brown", "Golden Son (42)")
	writeCalibreFile(t, filepath.Join(golden, "Golden Son - Pierce Brown.epub"), []byte("PK epub"))
	writeCalibreFile(t, filepath.Join(golden, "metadata.opf"), []byte(goldenSonOPF))
	writeCalibreFile(t, filepath.Join(golden, "cover.jpg"), calibreJPEG)
	// Morning Star: a Calibre folder whose OPF was deleted — the " (7)" is still stripped
	// (the file is named the Calibre way).
	morning := filepath.Join(root, "Pierce Brown", "Morning Star (7)")
	writeCalibreFile(t, filepath.Join(morning, "Morning Star - Pierce Brown.epub"), []byte("PK epub"))
	// Saga (2): someone's own folder, not Calibre's — its number stays.
	saga := filepath.Join(root, "Brian K Vaughan", "Saga (2)")
	writeCalibreFile(t, filepath.Join(saga, "saga-volume-two.epub"), []byte("PK epub"))
	before := calibreSnapshot(t, root)

	cat := &calibreCatalogue{
		byISBN: map[string]metadata.BookResult{
			"9780060853983": {Key: "hc:good-omens", Title: "Good Omens", Author: "Terry Pratchett", CoverURL: "https://covers.example/go.jpg"},
		},
		search: map[string][]metadata.BookResult{
			"Pierce Brown Golden Son":   {{Key: "hc:golden-son", Title: "Golden Son", Author: "Pierce Brown"}},
			"Pierce Brown Morning Star": {{Key: "hc:morning-star", Title: "Morning Star", Author: "Pierce Brown", CoverURL: "https://covers.example/ms.jpg"}},
		},
		details: map[string]metadata.BookDetails{
			"hc:good-omens": {BookResult: metadata.BookResult{Key: "hc:good-omens", Title: "Good Omens", Author: "Terry Pratchett",
				CoverURL: "https://covers.example/go.jpg"}},
			"hc:golden-son":   {BookResult: metadata.BookResult{Key: "hc:golden-son", Title: "Golden Son", Author: "Pierce Brown"}},
			"hc:morning-star": {BookResult: metadata.BookResult{Key: "hc:morning-star", Title: "Morning Star", Author: "Pierce Brown", CoverURL: "https://covers.example/ms.jpg"}},
		},
	}
	svc := books.NewService(st.DB(), cat, slog.Default())
	covers := filepath.Join(t.TempDir(), "covers")
	c := &Coordinator{books: svc, db: st.DB(), log: slog.Default(), quality: quality.NewService(st.DB()),
		imp: library.NewImporter(t.TempDir(), slog.Default()), bookCovers: covers}

	res, err := c.ScanBookLibrary(context.Background(), root, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 {
		t.Errorf("imported %d, want 3 (Good Omens, Golden Son, Morning Star): %+v", res.Imported, res)
	}
	// ISBN first: the OPF's ISBN was asked, and Good Omens never needed a title search.
	if len(cat.isbns) == 0 || cat.isbns[0] != "9780060853983" {
		t.Errorf("ISBN lookups = %v, want the OPF's ISBN", cat.isbns)
	}
	for _, q := range cat.queries {
		if strings.Contains(q, "Misfiled") || strings.Contains(q, "Good Omens") {
			t.Errorf("searched %q for a book its OPF's ISBN found", q)
		}
		if strings.Contains(q, "(42)") || strings.Contains(q, "(7)") || strings.Contains(q, "(12)") {
			t.Errorf("searched %q with Calibre's id still on", q)
		}
	}
	if !slices.Contains(cat.queries, "Brian K Vaughan Saga (2)") {
		t.Errorf("queries = %v, want someone's own \"Saga (2)\" kept as it is", cat.queries)
	}

	list, _ := svc.List(context.Background())
	by := map[string]books.Book{}
	for _, b := range list {
		by[b.Title] = b
	}
	if len(by) != 3 {
		t.Fatalf("library = %v", by)
	}
	go1 := by["Good Omens"]
	if go1.Ebook == nil || !strings.HasSuffix(go1.Ebook.Path, "Misfiled Name - Terry Pratchett.epub") {
		t.Errorf("Good Omens ebook = %+v, want the Calibre file", go1.Ebook)
	}
	if go1.CoverURL != "https://covers.example/go.jpg" {
		t.Errorf("Good Omens cover = %q, want the catalogue's (cover.jpg only stands in for none)", go1.CoverURL)
	}
	if go1.SeriesName != "Omens" || go1.SeriesPosition != 1 {
		t.Errorf("Good Omens series = %q #%v, want the OPF's", go1.SeriesName, go1.SeriesPosition)
	}
	if go1.Year != 1990 || go1.ReleaseDate != "1990-05-01" {
		t.Errorf("Good Omens date = %d %q, want the OPF's (the catalogue had none)", go1.Year, go1.ReleaseDate)
	}
	gs := by["Golden Son"]
	if !strings.HasPrefix(gs.CoverURL, "/api/v1/books/") || !strings.Contains(gs.CoverURL, "/cover-image") {
		t.Errorf("Golden Son cover = %q, want the folder's cover.jpg served as an uploaded cover", gs.CoverURL)
	}
	if b, err := os.ReadFile(filepath.Join(covers, "book-"+strconv.FormatInt(gs.ID, 10)+".jpg")); err != nil || string(b) != string(calibreJPEG) {
		t.Errorf("copied cover = %q, %v", b, err)
	}
	if gs.SeriesName != "Red Rising Saga" || gs.SeriesPosition != 2 {
		t.Errorf("Golden Son series = %q #%v", gs.SeriesName, gs.SeriesPosition)
	}
	if ms := by["Morning Star"]; ms.Ebook == nil {
		t.Errorf("Morning Star (no OPF) not matched: %+v", ms)
	}
	if entries, _ := os.ReadDir(covers); len(entries) != 1 {
		t.Errorf("covers folder holds %d files, want only Golden Son's", len(entries))
	}

	// Read-only: every file in the Calibre library is exactly as it was.
	if after := calibreSnapshot(t, root); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("the scan changed the Calibre folder:\nbefore %v\nafter  %v", before, after)
	}

	// A second scan finds everything already filed and adds nothing.
	res, err = c.ScanBookLibrary(context.Background(), root, root, nil)
	if err != nil || res.Imported != 0 {
		t.Errorf("second scan = %+v, %v; want nothing new", res, err)
	}
}

// An unreadable or hostile OPF doesn't stop the scan: the folder is matched by its name.
func TestBookScanIgnoresBrokenOPF(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	dir := filepath.Join(root, "Pierce Brown", "Golden Son (42)")
	writeCalibreFile(t, filepath.Join(dir, "Golden Son - Pierce Brown.epub"), []byte("PK epub"))
	writeCalibreFile(t, filepath.Join(dir, "metadata.opf"), []byte(`<?xml version="1.0"?><!DOCTYPE package [<!ENTITY x SYSTEM "file:///etc/passwd">]><package><metadata><dc:title>&x;</dc:title></metadata></package>`))
	cat := &calibreCatalogue{search: map[string][]metadata.BookResult{
		"Pierce Brown Golden Son": {{Key: "hc:golden-son", Title: "Golden Son", Author: "Pierce Brown"}},
	}}
	svc := books.NewService(st.DB(), cat, slog.Default())
	c := &Coordinator{books: svc, db: st.DB(), log: slog.Default(), quality: quality.NewService(st.DB()),
		imp: library.NewImporter(t.TempDir(), slog.Default())}
	res, err := c.ScanBookLibrary(context.Background(), root, root, nil)
	if err != nil || res.Imported != 1 {
		t.Fatalf("scan = %+v, %v; want Golden Son matched by its folder name", res, err)
	}
	if strings.Join(cat.queries, "|") != "Pierce Brown Golden Son" {
		t.Errorf("queries = %v", cat.queries)
	}
}
