package automation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// A library scan pointed at a Calibre library ("Author/Title (123)/Title - Author.epub",
// with metadata.opf and cover.jpg beside each book) reads the OPF to know which book a
// folder holds: its ISBN first, then its title and authors, instead of the folder name.
// The OPF's series fills in a series the catalogue didn't give, and cover.jpg stands in
// for a cover the catalogue doesn't have. Everything here only reads the Calibre folder;
// the cover is copied into Arrmada's own covers folder.

// maxFolderCover bounds the cover.jpg copied, as an upload is bounded.
const maxFolderCover = 16 << 20

// folderOPF reads a scanned folder's metadata.opf. ok is false when there is none; one
// that can't be read is logged and ignored, so the folder is matched by its name as before.
func (c *Coordinator) folderOPF(bf library.BookFolder) (books.OPF, bool) {
	if bf.Dir == "" {
		return books.OPF{}, false
	}
	o, err := books.ReadOPF(bf.Dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.log.Info("book scan: couldn't read the folder's metadata.opf — matching by its name", "folder_title", bf.Title, "err", err)
		}
		return books.OPF{}, false
	}
	if o.Title == "" && o.ISBN == "" {
		return books.OPF{}, false
	}
	return o, true
}

// scanIdentity is what a folder is looked up by: the OPF's ISBN, title and authors when
// it has them, else the ISBN in a name and the folder's own title and author.
func scanIdentity(bf library.BookFolder, opf books.OPF, hasOPF bool) (isbn, title, author string) {
	title, author = bf.Title, bf.Author
	if hasOPF {
		isbn = opf.ISBN
		if opf.Title != "" {
			title = opf.Title
			if a := opf.Author(); a != "" {
				author = a
			}
		}
	}
	if isbn == "" {
		isbn = scanISBN(bf)
	}
	return isbn, title, author
}

// withOPFDates fills a lookup result's publication date and year from the OPF where the
// catalogue gave none: books.Add keeps a result's own values first.
func withOPFDates(r metadata.BookResult, opf books.OPF) metadata.BookResult {
	if r.ReleaseDate == "" {
		r.ReleaseDate = opf.Published
	}
	if r.Year == 0 {
		r.Year = opf.Year
	}
	return r
}

// applyFolderMeta fills in, after a scan has filed a folder under book b, what only the
// Calibre folder knew: the OPF's series when the book has none, and cover.jpg when the
// book has no cover at all. Only ever fills in.
func (c *Coordinator) applyFolderMeta(ctx context.Context, b books.Book, opf *books.OPF, coverDirs []string) {
	if opf != nil && b.SeriesName == "" && opf.Series != "" {
		if err := c.books.SetSeries(ctx, b.ID, opf.Series, opf.SeriesPosition); err != nil {
			c.log.Warn("book scan: couldn't record the series from metadata.opf", "title", b.Title, "err", err)
		} else {
			c.log.Info("book scan: series from metadata.opf", "title", b.Title, "series", opf.Series, "position", opf.SeriesPosition)
		}
	}
	if b.CoverURL != "" || c.bookCovers == "" || len(coverDirs) == 0 {
		return
	}
	if m, _ := filepath.Glob(filepath.Join(c.bookCovers, fmt.Sprintf("book-%d.*", b.ID))); len(m) > 0 {
		return // a cover someone uploaded
	}
	for _, dir := range coverDirs {
		ok, err := copyFolderCover(filepath.Join(dir, books.CalibreCover), filepath.Join(c.bookCovers, fmt.Sprintf("book-%d.jpg", b.ID)))
		if err != nil {
			c.log.Warn("book scan: couldn't copy the folder's cover.jpg", "title", b.Title, "err", err)
			continue
		}
		if !ok {
			continue
		}
		url := fmt.Sprintf("/api/v1/books/%d/cover-image?v=%d", b.ID, time.Now().Unix())
		if err := c.books.SetCover(ctx, b.ID, url); err != nil {
			c.log.Warn("book scan: couldn't use the folder's cover.jpg", "title", b.Title, "err", err)
			return
		}
		_ = c.books.TouchMeta(ctx, b.ID)
		c.log.Info("book scan: cover from the folder's cover.jpg (the catalogue had none)", "title", b.Title)
		return
	}
}

// copyFolderCover copies src (a cover.jpg) to dst, refusing anything that isn't a
// regular file, is empty, too large, or isn't a JPEG. ok is false when there's no cover.
func copyFolderCover(src, dst string) (bool, error) {
	in, err := os.Open(src)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false, nil
	}
	if st.Size() > maxFolderCover {
		return false, fmt.Errorf("cover.jpg is %d MB, more than covers may be", st.Size()>>20)
	}
	head := make([]byte, 3)
	if _, err := io.ReadFull(in, head); err != nil || head[0] != 0xFF || head[1] != 0xD8 || head[2] != 0xFF {
		return false, errors.New("cover.jpg isn't a JPEG")
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cover-*")
	if err != nil {
		return false, err
	}
	_, err = io.Copy(tmp, io.LimitReader(in, maxFolderCover))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return false, err
	}
	return true, nil
}
