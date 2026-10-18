package kepub

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeConv copies the input to the output with a marker in front, counting its runs. fail
// makes it fail; block holds every run until closed.
type fakeConv struct {
	runs    atomic.Int32
	fail    error
	block   chan struct{}
	noAvail bool
	mu      sync.Mutex
	srcs    []string
	active  atomic.Int32
	maxSeen atomic.Int32
}

func (f *fakeConv) Available() bool { return !f.noAvail }

func (f *fakeConv) Convert(ctx context.Context, src, dst string) error {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.runs.Add(1)
	f.mu.Lock()
	f.srcs = append(f.srcs, src)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.fail != nil {
		return f.fail
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, append([]byte("PK\x03\x04kepub:"), b...), 0o600)
}

// writeEPUB writes a small real EPUB (a zip with the mimetype first) at path.
func writeEPUB(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	files := []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>T</dc:title><dc:identifier id="id">x</dc:identifier><dc:language>en</dc:language></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`},
		{"OEBPS/c.xhtml", `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>T</title></head><body><p>` + text + `</p></body></html>`},
	}
	for i, fl := range files {
		h := &zip.FileHeader{Name: fl.name, Method: zip.Deflate}
		if i == 0 {
			h.Method = zip.Store
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, fl.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestOpenConvertsOnceThenServesTheCache(t *testing.T) {
	lib := t.TempDir()
	src := filepath.Join(lib, "Dune - Frank Herbert.epub")
	writeEPUB(t, src, "Hello")
	before, _ := os.ReadFile(src)
	conv := &fakeConv{}
	dir := filepath.Join(t.TempDir(), "kepub")
	s := New(Options{Dir: dir, Converter: conv})
	ctx := context.Background()

	f, err := s.Open(ctx, 7, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, f); !strings.HasPrefix(got, "PK\x03\x04kepub:") {
		t.Fatalf("served %q, want the converted file", got[:min(20, len(got))])
	}
	f, err = s.Open(ctx, 7, src)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if n := conv.runs.Load(); n != 1 {
		t.Errorf("converter ran %d times, want once (second open is a cache hit)", n)
	}
	// The converter only ever sees a temporary copy with a fixed name, never the library
	// file (whose name is the book's).
	if len(conv.srcs) != 1 || filepath.Base(conv.srcs[0]) != "in.epub" || strings.HasPrefix(conv.srcs[0], lib) {
		t.Errorf("converter read %v, want a temporary in.epub outside the library", conv.srcs)
	}
	after, _ := os.ReadFile(src)
	if string(before) != string(after) {
		t.Error("the library EPUB changed")
	}
	if libFiles := cacheFiles(t, lib); len(libFiles) != 1 {
		t.Errorf("library folder holds %v, want only the EPUB", libFiles)
	}
	files := cacheFiles(t, dir)
	if len(files) != 1 || !strings.HasPrefix(files[0], "book-7-") || !strings.HasSuffix(files[0], Ext) {
		t.Errorf("cache = %v, want one book-7-….kepub.epub", files)
	}
	// Per book, not per person, and never the title.
	if strings.Contains(files[0], "Dune") {
		t.Errorf("cache file %q names the book", files[0])
	}
	// No work folders left behind.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("cache dir holds %d entries, want 1", len(entries))
	}
}

func TestOpenReconvertsWhenTheEPUBChanges(t *testing.T) {
	src := filepath.Join(t.TempDir(), "b.epub")
	writeEPUB(t, src, "first")
	conv := &fakeConv{}
	dir := t.TempDir()
	s := New(Options{Dir: dir, Converter: conv})
	ctx := context.Background()
	f, err := s.Open(ctx, 1, src)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Same size, new mtime: still a new version.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	f, err = s.Open(ctx, 1, src)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// New content, new size.
	writeEPUB(t, src, "second, and longer")
	f, err = s.Open(ctx, 1, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, f); !strings.Contains(got, "PK") {
		t.Fatal("not converted")
	}
	if n := conv.runs.Load(); n != 3 {
		t.Errorf("converter ran %d times, want 3 (each change converts again)", n)
	}
	if files := cacheFiles(t, dir); len(files) != 1 {
		t.Errorf("cache = %v, want only the newest KEPUB of the book", files)
	}
}

func TestOpenFailures(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "b.epub")
	writeEPUB(t, src, "x")

	t.Run("converter fails", func(t *testing.T) {
		dir := t.TempDir()
		s := New(Options{Dir: dir, Converter: &fakeConv{fail: errors.New("zip: not a valid zip file")}})
		if _, err := s.Open(ctx, 1, src); err == nil {
			t.Fatal("want an error")
		}
		if files := cacheFiles(t, dir); len(files) != 0 {
			t.Errorf("cache = %v after a failure, want empty", files)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("a failed conversion left %d entries", len(entries))
		}
	})
	t.Run("output isn't a zip", func(t *testing.T) {
		s := New(Options{Dir: t.TempDir(), Converter: badOutput{}})
		if _, err := s.Open(ctx, 1, src); err == nil {
			t.Fatal("want an error for a non-zip result")
		}
	})
	t.Run("setting off", func(t *testing.T) {
		conv := &fakeConv{}
		s := New(Options{Dir: t.TempDir(), Converter: conv, Enabled: func(context.Context) bool { return false }})
		if s.Ready(ctx) {
			t.Error("Ready with the setting off")
		}
		if _, err := s.Open(ctx, 1, src); !errors.Is(err, ErrOff) {
			t.Errorf("err = %v, want ErrOff", err)
		}
		if conv.runs.Load() != 0 {
			t.Error("converted with the setting off")
		}
	})
	t.Run("no converter binary", func(t *testing.T) {
		s := New(Options{Dir: t.TempDir(), Converter: &fakeConv{noAvail: true}})
		if _, err := s.Open(ctx, 1, src); !errors.Is(err, ErrOff) {
			t.Errorf("err = %v, want ErrOff", err)
		}
	})
	t.Run("nil service", func(t *testing.T) {
		var s *Service
		if s.Ready(ctx) {
			t.Error("a nil service is ready")
		}
		if _, err := s.Open(ctx, 1, src); !errors.Is(err, ErrOff) {
			t.Errorf("err = %v, want ErrOff", err)
		}
	})
	t.Run("not an EPUB", func(t *testing.T) {
		pdf := filepath.Join(t.TempDir(), "b.pdf")
		_ = os.WriteFile(pdf, []byte("%PDF"), 0o644)
		kp := filepath.Join(t.TempDir(), "b.kepub.epub")
		writeEPUB(t, kp, "x")
		s := New(Options{Dir: t.TempDir(), Converter: &fakeConv{}})
		for _, p := range []string{pdf, kp} {
			if _, err := s.Open(ctx, 1, p); !errors.Is(err, ErrNotEPUB) {
				t.Errorf("%s: err = %v, want ErrNotEPUB", filepath.Base(p), err)
			}
		}
	})
	t.Run("too large", func(t *testing.T) {
		conv := &fakeConv{}
		s := New(Options{Dir: t.TempDir(), Converter: conv, MaxSource: 10})
		if _, err := s.Open(ctx, 1, src); !errors.Is(err, ErrTooLarge) {
			t.Errorf("err = %v, want ErrTooLarge", err)
		}
		if conv.runs.Load() != 0 {
			t.Error("converted an EPUB over the limit")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		s := New(Options{Dir: t.TempDir(), Converter: &fakeConv{block: make(chan struct{})}, Timeout: 50 * time.Millisecond})
		_, err := s.Open(ctx, 1, src)
		if err == nil || !strings.Contains(err.Error(), "longer than") {
			t.Errorf("err = %v, want a timeout", err)
		}
	})
}

type badOutput struct{}

func (badOutput) Available() bool { return true }
func (badOutput) Convert(_ context.Context, _, dst string) error {
	return os.WriteFile(dst, []byte("not a zip"), 0o600)
}

// Conversions run one at a time; a request waiting its turn gives up when its own context
// ends, and the book someone else just converted is served from the cache.
func TestOneConversionAtATime(t *testing.T) {
	lib := t.TempDir()
	conv := &fakeConv{block: make(chan struct{})}
	s := New(Options{Dir: t.TempDir(), Converter: conv})
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := range 3 {
		src := filepath.Join(lib, "b"+string(rune('a'+i))+".epub")
		writeEPUB(t, src, "x")
		for range 2 { // the same book twice
			wg.Add(1)
			go func() {
				defer wg.Done()
				f, err := s.Open(ctx, int64(i+1), src)
				if err == nil {
					f.Close()
				}
				errs <- err
			}()
		}
	}
	// Let them queue, then release.
	deadline := time.Now().Add(2 * time.Second)
	for conv.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(conv.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if m := conv.maxSeen.Load(); m != 1 {
		t.Errorf("%d conversions ran at once, want 1", m)
	}
	if n := conv.runs.Load(); n != 3 {
		t.Errorf("converter ran %d times for 3 books, want 3", n)
	}

	// A waiter whose request ends stops waiting.
	conv2 := &fakeConv{block: make(chan struct{})}
	s2 := New(Options{Dir: t.TempDir(), Converter: conv2})
	a := filepath.Join(lib, "ba.epub")
	done := make(chan struct{})
	defer func() { <-done }() // finished before the temp folders go
	go func() {
		defer close(done)
		f, err := s2.Open(ctx, 1, a)
		if err == nil {
			f.Close()
		}
	}()
	for conv2.runs.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := s2.Open(wctx, 2, filepath.Join(lib, "bb.epub")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiting open = %v, want its own deadline", err)
	}
	close(conv2.block)
}

// The cache stays under its size bound, dropping the least recently used KEPUB first.
func TestCacheIsBounded(t *testing.T) {
	lib := t.TempDir()
	dir := t.TempDir()
	conv := &fakeConv{}
	ctx := context.Background()
	var size int64
	srcs := make([]string, 4)
	for i := range srcs {
		srcs[i] = filepath.Join(lib, "b"+string(rune('a'+i))+".epub")
		writeEPUB(t, srcs[i], strings.Repeat("x", 100))
	}
	{
		probe := New(Options{Dir: t.TempDir(), Converter: conv})
		f, err := probe.Open(ctx, 99, srcs[0])
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := f.Stat()
		size = fi.Size()
		f.Close()
	}
	// Room for two.
	s := New(Options{Dir: dir, Converter: conv, MaxBytes: 2*size + size/2})
	open := func(i int) {
		f, err := s.Open(ctx, int64(i+1), srcs[i])
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	open(0)
	time.Sleep(20 * time.Millisecond)
	open(1)
	time.Sleep(20 * time.Millisecond)
	open(0) // book 1 used again: book 2 is now the oldest
	time.Sleep(20 * time.Millisecond)
	open(2)
	files := strings.Join(cacheFiles(t, dir), " ")
	if !strings.Contains(files, "book-1-") || !strings.Contains(files, "book-3-") || strings.Contains(files, "book-2-") {
		t.Errorf("cache = %s, want books 1 and 3 kept and 2 (least recently used) dropped", files)
	}
}

// One real kepubify run, where the binary is installed (the Docker image; CI's Go image
// doesn't have it, and skips).
func TestRealKepubify(t *testing.T) {
	k := Kepubify{}
	if !k.Available() {
		t.Skip("kepubify isn't installed here")
	}
	src := filepath.Join(t.TempDir(), "b.epub")
	writeEPUB(t, src, "Hello there. Second sentence.")
	s := New(Options{Dir: t.TempDir()})
	f, err := s.Open(context.Background(), 1, src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, zf := range zr.File {
		if zf.Name != "OEBPS/c.xhtml" {
			continue
		}
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		found = strings.Contains(string(b), `class="koboSpan"`)
	}
	if !found {
		t.Error("the KEPUB has no Kobo spans")
	}
	// A broken EPUB fails rather than leaving a file.
	bad := filepath.Join(t.TempDir(), "bad.epub")
	_ = os.WriteFile(bad, []byte("not a zip"), 0o644)
	if _, err := s.Open(context.Background(), 2, bad); err == nil {
		t.Error("a broken EPUB converted")
	}
}
