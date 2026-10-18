// Package kepub turns a library EPUB into a KEPUB for Kobo's own reader, on demand.
//
// A KEPUB is an EPUB with Kobo's spans added round every sentence. Kobo's stock reader
// only shows real page numbers, reading stats and its better typography for a KEPUB; it
// reads a plain EPUB with an older engine. kepubify (github.com/pgaskin/kepubify, MIT,
// bundled in the image) does the conversion.
//
// Conversion never touches the library file. kepubify reads a temporary copy, and the
// result lands in a cache under the data folder, one file per book named by the book id
// and the source file's size and modified time — so a replaced or edited EPUB is converted
// again, and nothing in the cache says who downloaded it. The cache is bounded in size
// (least recently used goes first), conversions run one at a time with a timeout, and an
// EPUB over MaxSource bytes isn't converted at all.
//
//	k := kepub.New(kepub.Options{Dir: filepath.Join(dataDir, "cache", "kepub"), Enabled: on})
//	f, err := k.Open(ctx, book.ID, "/books/Dune/Dune.epub")
//	if err != nil { /* send the EPUB instead */ }
//	defer f.Close()
//
// Callers send the EPUB whenever Open fails. Nothing here logs a book; the caller's log
// line must not name one next to a person (the audiobook privacy rule covers reading).
package kepub

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// KeyEnabled is the setting "Convert EPUB to KEPUB for Kobo" (Settings → Book server),
// on unless it was switched off.
const KeyEnabled = "books_kepub"

// ContentType is the media type OPDS readers know a KEPUB by (Calibre-Web and COPS use
// the same one).
const ContentType = "application/kepub+zip"

// Ext is the extension Kobo recognises a KEPUB by. A plain ".epub" name would make the
// Kobo open it with its EPUB engine, losing everything the conversion was for.
const Ext = ".kepub.epub"

const (
	// DefaultMaxBytes bounds the cache: a few hundred novels' worth.
	DefaultMaxBytes int64 = 512 << 20
	// DefaultMaxSource is the largest EPUB converted. kepubify holds a book's text in
	// memory; a 100 MB EPUB is an illustrated one, which gains least from the spans.
	DefaultMaxSource int64 = 100 << 20
	// DefaultTimeout bounds one conversion; a typical novel takes well under a second.
	DefaultTimeout = 2 * time.Minute
	// DefaultMaxUncompressed bounds what an EPUB may unpack to. kepubify reads every
	// entry, so a small zip that inflates to gigabytes (a zip bomb) is refused unopened.
	DefaultMaxUncompressed int64 = 1 << 30
	// maxRatio is the most an entry of any size above ratioFloor may inflate by. Book text
	// and images compress a few times over; a hundredfold is a bomb, not a book.
	maxRatio   = 100
	ratioFloor = 1 << 20
	// DefaultFailTTL is how long a failed conversion is remembered: the same broken EPUB
	// isn't converted again on every download, holding the one conversion slot.
	DefaultFailTTL = time.Hour
)

var (
	// ErrOff: the setting is off, or there's no converter.
	ErrOff = errors.New("kepub conversion is off")
	// ErrNotEPUB: only an EPUB can become a KEPUB.
	ErrNotEPUB = errors.New("not an EPUB")
	// ErrTooLarge: the EPUB is over MaxSource, unpacks to too much, or converts to a
	// KEPUB bigger than the whole cache.
	ErrTooLarge = errors.New("the EPUB is too large to convert")
	// ErrRecentlyFailed: this version of the EPUB failed to convert within FailTTL.
	ErrRecentlyFailed = errors.New("this EPUB failed to convert a short while ago")
)

// Failed reports whether an Open error is a real failure worth a log line, rather than
// one of the expected reasons to send the EPUB (off, not an EPUB, too large, or a failure
// already logged a short while ago).
func Failed(err error) bool {
	return err != nil && !errors.Is(err, ErrOff) && !errors.Is(err, ErrNotEPUB) &&
		!errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrRecentlyFailed)
}

// Converter turns the EPUB at src into a KEPUB at dst (a path that doesn't exist yet).
// The real one is Kepubify; tests pass a fake.
type Converter interface {
	Convert(ctx context.Context, src, dst string) error
	// Available reports whether the converter can run here at all.
	Available() bool
}

// Kepubify runs the kepubify binary: `kepubify -o <dst> <src>`, no shell, both paths
// chosen by this package.
type Kepubify struct {
	// Bin is the binary's name or path ("kepubify" on PATH by default).
	Bin string
}

func (k Kepubify) bin() string {
	if k.Bin == "" {
		return "kepubify"
	}
	return k.Bin
}

// Available reports whether the binary is on PATH (dev machines usually lack it).
func (k Kepubify) Available() bool {
	_, err := exec.LookPath(k.bin())
	return err == nil
}

// Convert runs kepubify. Its output names only the temporary file names, never a book.
func (k Kepubify) Convert(ctx context.Context, src, dst string) error {
	out, err := toolrun.Command(ctx, toolrun.Normal, k.bin(), "-o", dst, src).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kepubify: %w: %s", err, lastLines(out, 3))
	}
	return nil
}

// lastLines keeps the end of a tool's output (where kepubify says what went wrong), short.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	s := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Options configures the cache.
type Options struct {
	// Dir is the cache folder (created when first needed), under the data folder.
	Dir string
	// MaxBytes bounds the cache (0 = DefaultMaxBytes).
	MaxBytes int64
	// MaxSource is the largest EPUB converted (0 = DefaultMaxSource).
	MaxSource int64
	// Timeout bounds one conversion (0 = DefaultTimeout).
	Timeout time.Duration
	// MaxUncompressed bounds what an EPUB may unpack to (0 = DefaultMaxUncompressed).
	MaxUncompressed int64
	// FailTTL is how long a failed conversion is remembered (0 = DefaultFailTTL).
	FailTTL time.Duration
	// Converter does the work (nil = kepubify on PATH).
	Converter Converter
	// Enabled reads the setting on every use (nil = always on).
	Enabled func(ctx context.Context) bool
	// Now is the clock (nil = time.Now); tests move it.
	Now func() time.Time
}

// Service is the KEPUB cache. Safe for concurrent use; nil-safe (a nil Service is off).
type Service struct {
	o   Options
	sem chan struct{} // one conversion at a time; also guards pruning
	// available is the converter's answer, asked once: a binary doesn't appear on PATH
	// while the app runs.
	availOnce sync.Once
	avail     bool
	// failed is when each cache name last failed to convert, in memory (a restart tries
	// again).
	failMu sync.Mutex
	failed map[string]time.Time
}

// New builds the cache.
func New(o Options) *Service {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxSource <= 0 {
		o.MaxSource = DefaultMaxSource
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxUncompressed <= 0 {
		o.MaxUncompressed = DefaultMaxUncompressed
	}
	if o.FailTTL <= 0 {
		o.FailTTL = DefaultFailTTL
	}
	if o.Converter == nil {
		o.Converter = Kepubify{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, sem: make(chan struct{}, 1), failed: map[string]time.Time{}}
}

// failedRecently reports whether name failed to convert within FailTTL.
func (s *Service) failedRecently(name string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	at, ok := s.failed[name]
	return ok && s.o.Now().Sub(at) < s.o.FailTTL
}

// noteFailure remembers that name failed, dropping entries that have run out so the map
// stays as small as the number of broken EPUBs asked for within the hour.
func (s *Service) noteFailure(name string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	now := s.o.Now()
	for k, at := range s.failed {
		if now.Sub(at) >= s.o.FailTTL {
			delete(s.failed, k)
		}
	}
	s.failed[name] = now
}

// Ready reports whether a KEPUB can be made: the setting is on and the converter exists.
// OPDS offers the KEPUB link, and the shelf the Kobo download, only while it is.
func (s *Service) Ready(ctx context.Context) bool {
	if s == nil || s.o.Dir == "" {
		return false
	}
	if s.o.Enabled != nil && !s.o.Enabled(ctx) {
		return false
	}
	s.availOnce.Do(func() { s.avail = s.o.Converter.Available() })
	return s.avail
}

// IsEPUB reports whether a file can be converted, by its name: a plain EPUB, not a KEPUB
// already.
func IsEPUB(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".epub") && !strings.HasSuffix(p, Ext)
}

// cacheName is the cached KEPUB for one version of one book's EPUB. The size and modified
// time stand for the content: a replaced or re-saved file gets a new name.
func cacheName(bookID int64, fi os.FileInfo) string {
	return fmt.Sprintf("book-%d-%d-%d%s", bookID, fi.Size(), fi.ModTime().UnixNano(), Ext)
}

// bookPrefix starts every cached name of one book.
func bookPrefix(bookID int64) string { return "book-" + strconv.FormatInt(bookID, 10) + "-" }

// Open returns the KEPUB of a book's EPUB at src, opened for reading: from the cache, or
// converted now (waiting for any conversion already running). Any error means "send the
// EPUB": ErrOff, ErrNotEPUB and ErrTooLarge are expected; anything else is a failure worth
// logging.
func (s *Service) Open(ctx context.Context, bookID int64, src string) (*os.File, error) {
	if !s.Ready(ctx) {
		return nil, ErrOff
	}
	if !IsEPUB(src) {
		return nil, ErrNotEPUB
	}
	fi, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, ErrNotEPUB
	}
	if fi.Size() > s.o.MaxSource {
		return nil, ErrTooLarge
	}
	name := cacheName(bookID, fi)
	dst := filepath.Join(s.o.Dir, name)
	if f, ok := s.hit(dst); ok {
		return f, nil
	}
	if s.failedRecently(name) {
		return nil, ErrRecentlyFailed
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.sem }()
	// Someone else may have made it, or failed to, while this waited.
	if f, ok := s.hit(dst); ok {
		return f, nil
	}
	if s.failedRecently(name) {
		return nil, ErrRecentlyFailed
	}
	if err := s.convert(ctx, bookID, src, dst); err != nil {
		// A request that went away isn't the EPUB's fault; anything else is remembered.
		if ctx.Err() == nil {
			s.noteFailure(name)
		}
		return nil, err
	}
	s.prune(dst)
	f, err := os.Open(dst)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// hit opens a cached KEPUB and marks it used, so pruning keeps it longest.
func (s *Service) hit(dst string) (*os.File, bool) {
	f, err := os.Open(dst)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(dst, now, now)
	return f, true
}

// convert makes dst from src: copy the EPUB into a fresh work folder (kepubify never sees
// the library path, and a book renamed mid-way can't confuse it), convert there under the
// timeout, check the result is a zip, then move it into place and drop the book's older
// KEPUBs.
func (s *Service) convert(ctx context.Context, bookID int64, src, dst string) error {
	if err := os.MkdirAll(s.o.Dir, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(s.o.Dir, ".work-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	in := filepath.Join(work, "in.epub")
	if err := copyLimited(src, in, s.o.MaxSource); err != nil {
		return err
	}
	// The copy is what kepubify reads, so the copy is what's checked.
	if err := checkInflate(in, s.o.MaxUncompressed); err != nil {
		return err
	}
	out := filepath.Join(work, "out"+Ext)
	cctx, cancel := context.WithTimeout(ctx, s.o.Timeout)
	defer cancel()
	if err := s.o.Converter.Convert(cctx, in, out); err != nil {
		if cctx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("kepubify took longer than %s: %w", s.o.Timeout, err)
		}
		return err
	}
	if err := checkZip(out); err != nil {
		return err
	}
	// A KEPUB bigger than the whole cache would only evict everything else.
	if st, err := os.Stat(out); err != nil {
		return err
	} else if st.Size() > s.o.MaxBytes {
		return fmt.Errorf("the KEPUB is %d MB, more than the cache holds: %w", st.Size()>>20, ErrTooLarge)
	}
	s.dropBook(bookID)
	return os.Rename(out, dst)
}

// checkInflate opens an EPUB as a zip and refuses it, as ErrTooLarge, when its entries
// unpack to more than limit in all or one of them inflates by more than maxRatio (a zip
// bomb). Sizes come from the zip's own directory; an entry that lies about its size is
// caught by kepubify's own reads failing, within the timeout.
func checkInflate(path string, limit int64) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("the EPUB isn't a readable zip: %w", err)
	}
	defer zr.Close()
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > uint64(limit) {
			return fmt.Errorf("the EPUB unpacks to more than %d MB: %w", limit>>20, ErrTooLarge)
		}
		if f.UncompressedSize64 > ratioFloor {
			if f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > maxRatio {
				return fmt.Errorf("an entry of the EPUB inflates more than %d-fold: %w", maxRatio, ErrTooLarge)
			}
		}
	}
	return nil
}

// copyLimited copies src to dst, refusing a file that grew past limit since it was checked.
func copyLimited(src, dst string, limit int64) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, io.LimitReader(r, limit+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return ErrTooLarge
	}
	return nil
}

// checkZip makes sure the converter left a non-empty zip, not an empty or half file.
func checkZip(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("kepubify left no output: %w", err)
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, []byte("PK\x03\x04")) {
		return errors.New("kepubify's output isn't an EPUB")
	}
	return nil
}

// dropBook removes a book's cached KEPUBs (made from an older version of its EPUB).
func (s *Service) dropBook(bookID int64) {
	entries, err := os.ReadDir(s.o.Dir)
	if err != nil {
		return
	}
	prefix := bookPrefix(bookID)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), Ext) {
			_ = os.Remove(filepath.Join(s.o.Dir, e.Name()))
		}
	}
}

// prune keeps the cache under MaxBytes, removing the least recently used KEPUBs first
// (never keep, the one just made), and clears work folders a crash left behind. Called
// holding the conversion slot, so two prunes never race.
func (s *Service) prune(keep string) {
	entries, err := os.ReadDir(s.o.Dir)
	if err != nil {
		return
	}
	type cached struct {
		path string
		size int64
		used time.Time
	}
	var list []cached
	var total int64
	for _, e := range entries {
		p := filepath.Join(s.o.Dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.IsDir() {
			if strings.HasPrefix(e.Name(), ".work-") && time.Since(info.ModTime()) > time.Hour {
				_ = os.RemoveAll(p)
			}
			continue
		}
		if !strings.HasPrefix(e.Name(), "book-") || !strings.HasSuffix(e.Name(), Ext) {
			continue
		}
		list = append(list, cached{p, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(list, func(i, j int) bool { return list[i].used.Before(list[j].used) })
	for _, c := range list {
		if total <= s.o.MaxBytes {
			break
		}
		if c.path == keep {
			continue
		}
		if os.Remove(c.path) == nil {
			total -= c.size
		}
	}
}
