package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/kepub"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
)

// prefixConv "converts" by prefixing the bytes, or fails when fail is set.
type prefixConv struct {
	fail atomic.Bool
	runs atomic.Int32
}

func (*prefixConv) Available() bool { return true }
func (c *prefixConv) Convert(_ context.Context, src, dst string) error {
	c.runs.Add(1)
	if c.fail.Load() {
		return errors.New("kepubify: exit status 1: zip: not a valid zip file")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, append([]byte("PK\x03\x04kepub:"), b...), 0o600)
}

// zipOf is a small, always identical zip holding text: the KEPUB cache opens an EPUB as a
// zip before converting it.
func zipOf(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "text.xhtml", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, text)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type kepubFixture struct {
	s             *routeServer
	logs          *lockedBuf
	conv          *prefixConv
	cacheDir      string
	cookie, admin *http.Cookie
	manager       *http.Cookie
	epub, azw3    books.Book
	libDir        string
}

func newKepubFixture(t *testing.T) *kepubFixture {
	t.Helper()
	root := t.TempDir()
	f := &kepubFixture{conv: &prefixConv{}, cacheDir: filepath.Join(t.TempDir(), "kepub"), libDir: root}
	s, logs := newMailServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Downloads = download.NewService(db, d.Log)
		d.Quality = quality.NewService(db)
		mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
		sr := series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, mv, sr, d.Books, nil, d.Quality, nil, "", d.Log)
		set := d.Settings
		d.Kepub = kepub.New(kepub.Options{Dir: f.cacheDir, Converter: f.conv,
			Enabled: func(ctx context.Context) bool { return set.GetBool(ctx, kepub.KeyEnabled, true) }})
	})
	f.s, f.logs = s, logs
	_, f.cookie = s.user(t, "reader", auth.RoleRequester)
	_, f.admin = s.user(t, "admin-one", auth.RoleAdmin)
	_, f.manager = s.user(t, "manager-one", auth.RoleManager)
	ctx := context.Background()
	repo := books.NewRepo(s.st.DB())
	mk := func(key, title, file string) books.Book {
		b, err := repo.Create(ctx, books.Book{OLKey: key, Title: title, Author: "Frank Herbert"})
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, file)
		if err := os.WriteFile(p, zipOf(t, "PK fake ebook bytes of "+title), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetEdition(ctx, b.ID, books.KindEbook, p, strings.TrimPrefix(filepath.Ext(file), "."), 1, 1); err != nil {
			t.Fatal(err)
		}
		return b
	}
	f.epub = mk("OL1W", "Dune", "dune.epub")
	f.azw3 = mk("OL2W", "Dune Messiah", "messiah.azw3")
	return f
}

func (f *kepubFixture) download(t *testing.T, id int64, query string) (int, string, http.Header) {
	t.Helper()
	rec := f.s.do("GET", fmt.Sprintf("/api/v1/books/%d/ebook%s", id, query), f.cookie)
	return rec.Code, rec.Body.String(), rec.Header()
}

// "Download for Kobo (KEPUB)" sends the converted EPUB, named .kepub.epub; the plain
// download is untouched; a second download comes from the cache.
func TestKepubDownload(t *testing.T) {
	f := newKepubFixture(t)
	code, body, h := f.download(t, f.epub.ID, "?format=kepub")
	if code != 200 || !strings.HasPrefix(body, "PK\x03\x04kepub:") {
		t.Fatalf("kepub download: HTTP %d %q", code, body)
	}
	if ct := h.Get("Content-Type"); ct != "application/kepub+zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := h.Get("Content-Disposition"); cd != `attachment; filename="Dune - Frank Herbert.kepub.epub"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	f.download(t, f.epub.ID, "?format=kepub")
	if f.conv.runs.Load() != 1 {
		t.Errorf("converted %d times, want once (then the cache)", f.conv.runs.Load())
	}
	code, body, h = f.download(t, f.epub.ID, "")
	if code != 200 || body != string(zipOf(t, "PK fake ebook bytes of Dune")) || h.Get("Content-Type") != "application/epub+zip" {
		t.Errorf("plain download: HTTP %d %q %q", code, body, h.Get("Content-Type"))
	}
	// The library file is as it was, and nothing was written beside it.
	if b, _ := os.ReadFile(filepath.Join(f.libDir, "dune.epub")); string(b) != string(zipOf(t, "PK fake ebook bytes of Dune")) {
		t.Errorf("the library EPUB changed: %q", b)
	}
	if entries, _ := os.ReadDir(f.libDir); len(entries) != 2 {
		t.Errorf("library folder holds %d files, want 2", len(entries))
	}
	// Not an EPUB: the file as it is.
	code, body, h = f.download(t, f.azw3.ID, "?format=kepub")
	if code != 200 || body != string(zipOf(t, "PK fake ebook bytes of Dune Messiah")) || !strings.HasSuffix(h.Get("Content-Disposition"), `.azw3"`) {
		t.Errorf("azw3 with format=kepub: HTTP %d %q %q", code, body, h.Get("Content-Disposition"))
	}
	// Read-only accounts can't download either way.
	s := f.s
	_, ro := s.user(t, "watcher", auth.RoleReadonly)
	if rec := s.do("GET", fmt.Sprintf("/api/v1/books/%d/ebook?format=kepub", f.epub.ID), ro); rec.Code != http.StatusForbidden {
		t.Errorf("read-only: HTTP %d, want 403", rec.Code)
	}
}

// A conversion that fails sends the EPUB and logs the failure without the book or person.
func TestKepubDownloadFallsBack(t *testing.T) {
	f := newKepubFixture(t)
	f.conv.fail.Store(true)
	code, body, h := f.download(t, f.epub.ID, "?format=kepub")
	if code != 200 || body != string(zipOf(t, "PK fake ebook bytes of Dune")) || h.Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("HTTP %d %q %q, want the EPUB", code, body, h.Get("Content-Type"))
	}
	logs := f.logs.String()
	if !strings.Contains(logs, "couldn't make a KEPUB") {
		t.Errorf("failure not logged: %s", logs)
	}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "KEPUB") && (strings.Contains(line, "Dune") || strings.Contains(line, "reader")) {
			t.Errorf("log line names the book or the person: %s", line)
		}
	}
}

// The setting: on by default, shown on the shelf as ebook.kepub, admin-only to change,
// and off means plain EPUBs everywhere.
func TestKepubSetting(t *testing.T) {
	f := newKepubFixture(t)
	s := f.s
	readSetting := func() bool {
		rec := s.do("GET", "/api/v1/settings", f.admin)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		v, ok := out["books_kepub"].(bool)
		if !ok {
			t.Fatalf("settings lack books_kepub: %s", rec.Body)
		}
		return v
	}
	shelf := func() map[string]bool {
		rec := s.do("GET", "/api/v1/me/books", f.cookie)
		if rec.Code != 200 {
			t.Fatalf("shelf: HTTP %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Books []struct {
				Title string `json:"title"`
				Ebook *struct {
					Kepub bool `json:"kepub"`
				} `json:"ebook"`
			} `json:"books"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		m := map[string]bool{}
		for _, b := range out.Books {
			m[b.Title] = b.Ebook != nil && b.Ebook.Kepub
		}
		return m
	}
	if !readSetting() {
		t.Error("books_kepub defaults off, want on")
	}
	if m := shelf(); !m["Dune"] || m["Dune Messiah"] {
		t.Errorf("shelf kepub flags = %v, want Dune (EPUB) only", m)
	}
	if rec := s.doJSON("PUT", "/api/v1/settings", f.manager, `{"books_kepub":false}`); rec.Code != http.StatusForbidden {
		t.Errorf("manager switching it off: HTTP %d, want 403", rec.Code)
	}
	// A manager sending the whole object back unchanged still saves.
	if rec := s.doJSON("PUT", "/api/v1/settings", f.manager, `{"books_kepub":true}`); rec.Code != http.StatusOK {
		t.Errorf("manager sending it unchanged: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doJSON("PUT", "/api/v1/settings", f.admin, `{"books_kepub":false}`); rec.Code != http.StatusOK {
		t.Fatalf("admin switching it off: HTTP %d %s", rec.Code, rec.Body)
	}
	if readSetting() {
		t.Error("books_kepub still on")
	}
	if m := shelf(); m["Dune"] {
		t.Errorf("shelf still offers the Kobo download with it off: %v", m)
	}
	code, body, h := f.download(t, f.epub.ID, "?format=kepub")
	if code != 200 || body != string(zipOf(t, "PK fake ebook bytes of Dune")) || h.Get("Content-Type") != "application/epub+zip" {
		t.Errorf("off: HTTP %d %q, want the EPUB", code, body)
	}
	if f.conv.runs.Load() != 0 {
		t.Error("converted with the setting off")
	}
}

// Picking a cover from the picker removes a stored cover file (an upload, or the cover.jpg
// a Calibre scan copied): that file would otherwise keep winning in listening apps and
// OPDS readers, which serve a stored file before the URL.
func TestPickedCoverReplacesStoredCoverFile(t *testing.T) {
	f := newKepubFixture(t)
	s := f.s
	dir := filepath.Join(s.deps.Config.DataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(dir, fmt.Sprintf("book-%d.jpg", f.epub.ID))
	other := filepath.Join(dir, fmt.Sprintf("book-%d.jpg", f.azw3.ID))
	for _, p := range []string{stored, other} {
		if err := os.WriteFile(p, []byte{0xFF, 0xD8, 0xFF}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := s.doJSON("PUT", fmt.Sprintf("/api/v1/books/%d/cover", f.epub.ID), f.manager, `{"url":"https://covers.example/picked.jpg"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("the stored cover file is still there (err %v); it would keep winning over the picked cover", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another book's cover file went too: %v", err)
	}
	b, _ := s.deps.Books.Get(context.Background(), f.epub.ID)
	if b.CoverURL != "https://covers.example/picked.jpg" {
		t.Errorf("cover_url = %q", b.CoverURL)
	}
	if rec := s.do("GET", fmt.Sprintf("/api/v1/books/%d/cover-image", f.epub.ID), f.cookie); rec.Code != http.StatusNotFound {
		t.Errorf("cover-image: HTTP %d, want 404 now the file is gone", rec.Code)
	}
}

func TestSetEbookKepub(t *testing.T) {
	for _, c := range []struct {
		format string
		ready  bool
		want   bool
	}{{"EPUB", true, true}, {"epub", true, true}, {"EPUB", false, false}, {"AZW3", true, false}, {"PDF", true, false}} {
		b := books.Book{Ebook: &books.BookFile{Path: "/books/b." + strings.ToLower(c.format), Format: c.format, FileCount: 1}}
		setEbookKepub(&b, c.ready)
		if b.Ebook.Kepub != c.want {
			t.Errorf("format %s ready %v: kepub = %v, want %v", c.format, c.ready, b.Ebook.Kepub, c.want)
		}
	}
	setEbookKepub(&books.Book{}, true) // no ebook: no panic

	// A Calibre folder with the AZW3 and the EPUB is recorded as AZW3 (its first file),
	// but a download sends the EPUB, so the Kobo download is offered; a folder with no
	// EPUB isn't.
	both := t.TempDir()
	for _, n := range []string{"Dune - Frank Herbert.azw3", "Dune - Frank Herbert.epub"} {
		_ = os.WriteFile(filepath.Join(both, n), []byte("x"), 0o644)
	}
	noEPUB := t.TempDir()
	for _, n := range []string{"Dune.azw3", "Dune.pdf"} {
		_ = os.WriteFile(filepath.Join(noEPUB, n), []byte("x"), 0o644)
	}
	for dir, want := range map[string]bool{both: true, noEPUB: false} {
		b := books.Book{Ebook: &books.BookFile{Path: dir, Format: "AZW3", FileCount: 2}}
		setEbookKepub(&b, true)
		if b.Ebook.Kepub != want {
			t.Errorf("folder %s: kepub = %v, want %v", dir, b.Ebook.Kepub, want)
		}
	}
}
