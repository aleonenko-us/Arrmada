package audiotag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrUnsupported is returned (wrapped) for files this package won't read or
// write: an unknown container, or a format feature it can't handle safely (e.g.
// ID3v2.2, unsynchronised or compressed ID3 frames, fragmented MP4). The caller
// should leave such a file alone rather than retry.
var ErrUnsupported = errors.New("audiotag: unsupported file")

type format int

const (
	formatUnknown format = iota
	formatFLAC
	formatMP3
	formatMP4
)

// Read returns the house tags of the file at path.
func Read(path string) (Tags, error) {
	f, err := os.Open(path)
	if err != nil {
		return Tags{}, err
	}
	defer f.Close()

	ft, err := sniff(f)
	if err != nil {
		return Tags{}, fmt.Errorf("audiotag: read %s: %w", path, err)
	}
	var m fieldMap
	switch ft {
	case formatFLAC:
		m, err = readFLAC(f)
	case formatMP3:
		m, err = readMP3(f)
	case formatMP4:
		m, err = readMP4(f)
	}
	if err != nil {
		return Tags{}, fmt.Errorf("audiotag: read %s: %w", path, err)
	}
	return tagsFromFields(m), nil
}

// Write replaces the house tags of the file at path with t (see the package doc
// for exactly what is replaced and preserved). It writes a new file next to the
// original and renames it into place, so it never leaves a half-written file and
// it breaks hardlinks on purpose.
func Write(path string, t Tags) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	ft, err := sniff(src)
	if err != nil {
		return fmt.Errorf("audiotag: write %s: %w", path, err)
	}
	var rewrite func(src *os.File, dst io.Writer, m fieldMap) error
	switch ft {
	case formatFLAC:
		rewrite = writeFLAC
	case formatMP3:
		rewrite = writeMP3
	case formatMP4:
		rewrite = writeMP4
	}
	m := t.fields()
	err = replaceFile(path, func(dst *os.File) error {
		if err := rewrite(src, dst, m); err != nil {
			return err
		}
		// Done with the original; close it before the rename (Windows refuses
		// to replace a file that is still open).
		return src.Close()
	})
	if err != nil {
		return fmt.Errorf("audiotag: write %s: %w", path, err)
	}
	return nil
}

// sniff identifies the container from the first bytes rather than the file
// extension, so a mislabelled file is never parsed as the wrong format.
func sniff(f *os.File) (format, error) {
	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return formatUnknown, err
	}
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return formatUnknown, err
	}
	switch {
	case bytes.HasPrefix(head, []byte("fLaC")):
		return formatFLAC, nil
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return formatMP4, nil
	case bytes.HasPrefix(head, []byte("ID3")):
		// ID3v2 is also (wrongly) prepended to some FLAC files; those fail later
		// in the MP3 path's frame check rather than being misparsed.
		return formatMP3, nil
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// Bare MPEG audio frame sync with no ID3 tag yet.
		return formatMP3, nil
	}
	return formatUnknown, fmt.Errorf("%w: unrecognised container", ErrUnsupported)
}

// replaceFile writes a new version of path through fill and atomically renames
// it over path. The temporary file lives in the same directory so the rename
// never crosses filesystems. On any error the temporary file is removed and the
// original is untouched.
//
// The rename gives path a new inode: other hardlinks to the old file (a seeding
// torrent's copy) keep the old contents. That is intended.
func replaceFile(path string, fill func(dst *os.File) error) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".audiotag-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if err = fill(tmp); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; keep the original's permission bits so the
	// media server can still read it. (Ownership follows the writing process.)
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	// Best effort: persist the rename itself. Not every platform lets a
	// directory be synced, and the data is already safely on disk.
	if d, derr := os.Open(filepath.Dir(path)); derr == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
