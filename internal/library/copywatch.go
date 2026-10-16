package library

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
)

// Copy progress for imports that run as jobs. The importer's copy has no context to carry
// a reporter (it sits under every kind's import), so whoever wants to hear about it watches
// a source folder instead: every import copy out of that folder reports its bytes. A
// download being imported by hand is never imported by the sweep at the same time (a held
// download is skipped by it), so what a watch hears is that import's own copying.
// A hardlink copies nothing and reports nothing.

// CopyReport is told how far one file's copy has got: its source path, and the bytes
// written so far of its size.
type CopyReport func(src string, done, size int64)

type copyWatch struct {
	dir string
	fn  CopyReport
}

var (
	copyWatchMu sync.Mutex
	copyWatches = map[int]copyWatch{}
	copyWatchID int
)

// copyReportEvery is how many bytes pass between reports of one file's copy.
const copyReportEvery = 16 << 20

// WatchCopies reports every import copy out of dir (dir itself, or anything under it)
// to fn until stop is called. fn runs on the copying goroutine and must be quick.
func WatchCopies(dir string, fn CopyReport) (stop func()) {
	if dir == "" || fn == nil {
		return func() {}
	}
	copyWatchMu.Lock()
	copyWatchID++
	id := copyWatchID
	copyWatches[id] = copyWatch{dir: filepath.Clean(dir), fn: fn}
	copyWatchMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			copyWatchMu.Lock()
			delete(copyWatches, id)
			copyWatchMu.Unlock()
		})
	}
}

// copyReporter is the report for a copy of src, or nil when nobody is watching.
func copyReporter(src string) CopyReport {
	copyWatchMu.Lock()
	defer copyWatchMu.Unlock()
	if len(copyWatches) == 0 {
		return nil
	}
	src = filepath.Clean(src)
	for _, w := range copyWatches {
		if src == w.dir || strings.HasPrefix(src, w.dir+string(filepath.Separator)) {
			return w.fn
		}
	}
	return nil
}

// countingReader reports what passes through it to a CopyReport, at most every
// copyReportEvery bytes, and once more at the end of the file.
type countingReader struct {
	r          io.Reader
	src        string
	size, done int64
	last       int64
	report     CopyReport
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.done-c.last >= copyReportEvery || (err == io.EOF && c.done != c.last) {
		c.last = c.done
		c.report(c.src, c.done, c.size)
	}
	return n, err
}

// watchedReader is r, reporting to whoever watches src's folder.
func watchedReader(r io.Reader, src string, size int64) io.Reader {
	rep := copyReporter(src)
	if rep == nil {
		return r
	}
	rep(src, 0, size)
	return &countingReader{r: r, src: src, size: size, report: rep}
}
