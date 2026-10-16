package library

import (
	"context"
	"io"
	"os"
)

// Copy progress for imports that run as jobs. A person's import from Needs you can copy
// tens of gigabytes onto another drive; the job that runs it puts a CopyReport in its
// context (WithCopyReport), and the import takes an importer that carries it
// (Importer.Using). Only that import's own copies report: nothing is matched by folder, so
// an unrelated copy out of the same downloads folder never shows up in its progress.
// A hardlink copies nothing and reports nothing.

// CopyReport is told how far one file's copy has got: its source path, and the bytes
// written so far of its size.
type CopyReport func(src string, done, size int64)

type copyReportKey struct{}

// WithCopyReport makes the imports run under ctx (through Importer.Using) report their
// copies to fn. An inner call replaces an outer one.
func WithCopyReport(ctx context.Context, fn CopyReport) context.Context {
	return context.WithValue(ctx, copyReportKey{}, fn)
}

// Using is the importer for an import run under ctx: itself, or — when ctx carries a
// CopyReport — a copy that reports its copies there. Cheap: the importer is a few fields.
func (im *Importer) Using(ctx context.Context) *Importer {
	fn, _ := ctx.Value(copyReportKey{}).(CopyReport)
	if fn == nil || im == nil {
		return im
	}
	cp := *im
	cp.copyReport = fn
	return &cp
}

// copyReportEvery is how many bytes a reported copy moves between reports.
const copyReportEvery = 64 << 20

// copyTo is io.Copy, swappable so a test can see what the copy is handed: the kernel's
// fast paths (copy_file_range, which reflinks on btrfs/XFS and copies server-side on
// some network shares) only apply when the writer is an *os.File and the reader is an
// *os.File, or an *io.LimitedReader over one.
var copyTo = io.Copy

// copyContents copies in to out. Without a report it is one io.Copy. With one it copies
// in chunks — each still a LimitedReader over the file, so the fast paths stay — and
// reports after every chunk.
func copyContents(out, in *os.File, src string, size int64, report CopyReport) error {
	if report == nil {
		_, err := copyTo(out, in)
		return err
	}
	report(src, 0, size)
	var done int64
	for {
		n, err := copyTo(out, &io.LimitedReader{R: in, N: copyReportEvery})
		done += n
		if err != nil {
			return err
		}
		if n > 0 {
			report(src, done, size)
		}
		if n < copyReportEvery { // the end of the file
			return nil
		}
	}
}
