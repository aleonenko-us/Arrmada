package automation

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/tristenlammi/arrmada/internal/library"
)

// A review import (Import anyway, Choose different…, Map files…) can copy tens of
// gigabytes when the downloads and the library sit on different drives, so it runs as a
// job: the API checks what it can refuse up front (CheckReviewImport), then the job does
// the import and reports how far the copy has got.

// ImportProgress hears how far a review import's copying has got: bytes copied so far of
// the download's size. A hardlinked import is instant and reports nothing.
type ImportProgress func(copied, total int64)

type importProgressKey struct{}

// WithImportProgress makes a review import run under ctx report its copying to fn.
func WithImportProgress(ctx context.Context, fn ImportProgress) context.Context {
	return context.WithValue(ctx, importProgressKey{}, fn)
}

func importProgressFrom(ctx context.Context) ImportProgress {
	fn, _ := ctx.Value(importProgressKey{}).(ImportProgress)
	return fn
}

// watchReviewCopies reports the copies out of r's download to ctx's ImportProgress, if
// it has one, until stop is called. The total is the download's size: what's in it that
// doesn't import (a sample, an .nfo) never gets copied, so the bar can stop short of full.
func watchReviewCopies(ctx context.Context, r Review) (stop func()) {
	fn := importProgressFrom(ctx)
	if fn == nil || r.ContentPath == "" {
		return func() {}
	}
	total := r.SizeBytes
	if total <= 0 {
		total = treeSize(r.ContentPath)
	}
	var mu sync.Mutex
	files := map[string]int64{}
	report := func(src string, done, _ int64) {
		mu.Lock()
		files[src] = done
		var copied int64
		for _, n := range files {
			copied += n
		}
		mu.Unlock()
		fn(copied, max(total, copied))
	}
	// A lone archive is unpacked next to itself (library.prepareContent), and its
	// contents are copied from there.
	stopDir := library.WatchCopies(r.ContentPath, report)
	stopScratch := library.WatchCopies(r.ContentPath+".extracted", report)
	return func() { stopDir(); stopScratch() }
}

// treeSize is the size of every regular file at or under path.
func treeSize(path string) int64 {
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ReviewImportTarget is a review import that passed its checks: the review, the library
// item it goes into, and that item's title (for "Imported into …"; empty if unreadable).
type ReviewImportTarget struct {
	Review Review
	Dest   int64
	Title  string
}

// CheckReviewImport answers, before anything is touched, whether review id can be
// imported into targetID (0 = the item it was grabbed for) of kind targetKind: the review
// must still be pending, the target of the review's own kind, there must be a target at
// all, the download still on disk and its module on. Each refusal is the same error
// ImportReview gives, so the API answers it with the same status.
func (c *Coordinator) CheckReviewImport(ctx context.Context, id, targetID int64, targetKind string) (ReviewImportTarget, error) {
	r, err := c.pendingReview(ctx, id)
	if err != nil {
		return ReviewImportTarget{}, err
	}
	if targetKind != "" && targetKind != r.MediaType {
		return ReviewImportTarget{}, fmt.Errorf("%w: this download is a %s, not a %s", ErrWrongTargetKind,
			kindLabel(r.MediaType), kindLabel(targetKind))
	}
	dest := r.ExpectedID
	if targetID > 0 {
		dest = targetID
	}
	if dest <= 0 {
		return ReviewImportTarget{}, needsTargetError{kindLabel(r.MediaType)}
	}
	if err := checkDownloadThere(r); err != nil {
		return ReviewImportTarget{}, err
	}
	if !c.importerWired(r.MediaType) {
		return ReviewImportTarget{}, moduleOffError{reviewModuleLabel[r.MediaType]}
	}
	return ReviewImportTarget{Review: r, Dest: dest, Title: c.itemTitle(ctx, r.MediaType, dest)}, nil
}

// checkDownloadThere is ErrDownloadGone for a review whose files are no longer on disk.
func checkDownloadThere(r Review) error {
	if r.ContentPath == "" {
		return ErrDownloadGone
	}
	if _, err := os.Stat(r.ContentPath); err != nil {
		return fmt.Errorf("%w: %s", ErrDownloadGone, r.ContentPath)
	}
	return nil
}

// importerWired reports whether the module that imports kind is running. An unknown kind is
// left to the import, which names it.
func (c *Coordinator) importerWired(kind string) bool {
	switch kind {
	case "series":
		return c.series != nil
	case "movie":
		return c.movies != nil
	case "music":
		return c.music != nil
	case "book":
		return c.books != nil
	}
	return true
}

// itemTitle is a library item's title, or "" when it can't be read.
func (c *Coordinator) itemTitle(ctx context.Context, kind string, id int64) string {
	switch kind {
	case "series":
		if c.series != nil {
			if s, err := c.series.Get(ctx, id); err == nil {
				return s.Title
			}
		}
	case "movie":
		if c.movies != nil {
			if m, err := c.movies.Get(ctx, id); err == nil {
				return m.Title
			}
		}
	case "music":
		if c.music != nil {
			if al, err := c.music.GetAlbum(ctx, id); err == nil {
				return al.Title
			}
		}
	case "book":
		if c.books != nil {
			if b, err := c.books.Get(ctx, id); err == nil {
				return b.Title
			}
		}
	}
	return ""
}

// reviewImportFailed writes a failed review import into the history of the item it was
// going into, so Activity → History shows it with its reason. The review stays open. how,
// when set, says how it was imported ("mapped by hand"), as the success line does.
func (c *Coordinator) reviewImportFailed(ctx context.Context, r Review, dest int64, how string, err error) {
	// The job may have been stopped; the line is still worth writing.
	ctx = context.WithoutCancel(ctx)
	from := "review"
	if how != "" {
		from += " (" + how + ")"
	}
	c.reviewEvent(ctx, r.MediaType, dest, "failed", fmt.Sprintf("Couldn't import from %s: %v — %s", from, err, r.Name))
}
