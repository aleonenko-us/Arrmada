package audiotag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupported is returned (wrapped) for files a backend won't read or write:
// an unknown container, a format the backend doesn't cover (Ogg in the
// permissive backend), or a feature it can't handle safely (e.g. ID3v2.2,
// unsynchronised ID3 frames, fragmented MP4). Leave such a file untagged.
var ErrUnsupported = errors.New("audiotag: unsupported file")

// Backend selects the tag library behind a Tagger.
type Backend int

const (
	// Permissive is pure Go with MIT/Apache-2.0 libraries: go-flac + flacvorbis
	// (FLAC), bogem/id3v2 (MP3) and abema/go-mp4 (MP4/M4A). No Ogg.
	Permissive Backend = iota
	// TagLib is TagLib 2 compiled to WebAssembly and run by wazero
	// (go.senan.xyz/taglib, LGPL-2.1). Also covers Ogg Vorbis and Opus.
	TagLib
	// Auto is Permissive for FLAC, MP3 and MP4, and TagLib only for Ogg Vorbis and
	// Opus, which no permissive library covers.
	Auto
)

// DefaultBackend is what the package-level functions use: the owner's decision
// (2026-10-19) is the permissive libraries everywhere they reach, and TagLib only
// for Ogg. Nothing outside this package imports either library, and nothing
// imports this package yet, so the shipped binary holds no LGPL code until the
// import path starts tagging (see THIRD_PARTY_NOTICES.md).
const DefaultBackend = Auto

func (b Backend) String() string {
	switch b {
	case TagLib:
		return "taglib"
	case Auto:
		return "auto"
	}
	return "permissive"
}

// backendFor is the library that handles a container under this Tagger.
func (tg *Tagger) backendFor(f format) Backend {
	if tg.backend == Auto {
		if f == formatOgg {
			return TagLib
		}
		return Permissive
	}
	return tg.backend
}

// WriteOptions tunes how tags are rendered.
type WriteOptions struct {
	// MP4GenreSeparator, when set, writes MP4 genres as one joined value
	// (e.g. "; "), for players that show only the first of several. Empty
	// writes one value per genre, as Picard does. Other formats ignore it.
	MP4GenreSeparator string
}

// Tagger reads and writes tags through one backend. The zero value uses
// Permissive. It is safe for concurrent use on different files.
type Tagger struct{ backend Backend }

// New returns a Tagger using b.
func New(b Backend) *Tagger { return &Tagger{backend: b} }

// Read returns the house tags of the file at path, using DefaultBackend.
func Read(path string) (Tags, error) { return New(DefaultBackend).Read(path) }

// Write replaces the house tags of the file at path, using DefaultBackend.
func Write(path string, t Tags, opts WriteOptions) error {
	return New(DefaultBackend).Write(path, t, opts)
}

// Properties returns the audio properties of the file at path, using
// DefaultBackend.
func Properties(path string) (AudioProperties, error) { return New(DefaultBackend).Properties(path) }

type format int

const (
	formatUnknown format = iota
	formatFLAC
	formatMP3
	formatMP4
	formatOgg
)

func (tg *Tagger) supports(f format) bool {
	return f != formatUnknown && (tg.backendFor(f) == TagLib || f != formatOgg)
}

// open opens path and identifies its container.
func (tg *Tagger) open(path string) (*os.File, format, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	ft, err := sniff(f)
	if err == nil && !tg.supports(ft) {
		err = fmt.Errorf("%w: %s backend has no Ogg support", ErrUnsupported, tg.backend)
	}
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, ft, nil
}

// Read returns the house tags of the file at path. Values come back
// normalised (see Tags) whichever backend and format produced them.
func (tg *Tagger) Read(path string) (Tags, error) {
	f, ft, err := tg.open(path)
	if err != nil {
		return nil, fmt.Errorf("audiotag: read %s: %w", path, err)
	}
	defer f.Close()

	var raw map[string][]string
	switch {
	case tg.backendFor(ft) == TagLib:
		raw, err = readTagLib(path)
	case ft == formatFLAC:
		raw, err = readFLAC(f)
	case ft == formatMP3:
		raw, err = readMP3(f)
	case ft == formatMP4:
		raw, err = readMP4(f)
	}
	if err != nil {
		return nil, fmt.Errorf("audiotag: read %s: %w", path, err)
	}
	return normalizeRead(raw), nil
}

// Write makes the file's house tags exactly t: house keys missing from t are
// removed, every other tag in the file is kept, and the audio is not touched.
// The new file is built next to the original and renamed over it (see the
// package doc), so a failed write leaves the original as it was.
func (tg *Tagger) Write(path string, t Tags, opts WriteOptions) error {
	clean, err := prepareWrite(t)
	if err != nil {
		return err
	}
	src, ft, err := tg.open(path)
	if err != nil {
		return fmt.Errorf("audiotag: write %s: %w", path, err)
	}
	defer src.Close()

	err = replaceFile(path, func(dst *os.File) error {
		if tg.backendFor(ft) == TagLib {
			return writeTagLib(src, dst, ft, clean, opts)
		}
		var rewrite func(*os.File, io.Writer, Tags, WriteOptions) error
		switch ft {
		case formatFLAC:
			rewrite = writeFLAC
		case formatMP3:
			rewrite = writeMP3
		case formatMP4:
			rewrite = writeMP4
		}
		if err := rewrite(src, dst, clean, opts); err != nil {
			return err
		}
		// Close before the rename (Windows refuses to replace an open file).
		return src.Close()
	})
	if err != nil {
		return fmt.Errorf("audiotag: write %s: %w", path, err)
	}
	return nil
}

// Properties returns the audio properties of the file at path.
func (tg *Tagger) Properties(path string) (AudioProperties, error) {
	f, ft, err := tg.open(path)
	if err != nil {
		return AudioProperties{}, fmt.Errorf("audiotag: properties %s: %w", path, err)
	}
	defer f.Close()

	var p AudioProperties
	switch {
	case tg.backendFor(ft) == TagLib:
		p, err = propertiesTagLib(path)
	case ft == formatFLAC:
		p, err = flacProperties(f)
	case ft == formatMP3:
		p, err = mp3Properties(f)
	case ft == formatMP4:
		p, err = mp4Properties(f)
	}
	if err != nil {
		return AudioProperties{}, fmt.Errorf("audiotag: properties %s: %w", path, err)
	}
	return p, nil
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
	case bytes.HasPrefix(head, []byte("OggS")):
		return formatOgg, nil
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return formatMP4, nil
	case bytes.HasPrefix(head, []byte("ID3")):
		// ID3v2 is also (wrongly) prepended to some FLAC files; those fail later
		// in the MP3 path rather than being misparsed.
		return formatMP3, nil
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// Bare MPEG audio frame sync with no ID3 tag yet.
		return formatMP3, nil
	}
	return formatUnknown, fmt.Errorf("%w: unrecognised container", ErrUnsupported)
}

// replaceFile writes a new version of path through fill and atomically renames
// it over path. The temporary file lives in the same directory so the rename
// never crosses filesystems, and keeps the extension so libraries that pick a
// format by name still see the right one. On any error the temporary file is
// removed and the original is untouched.
//
// The rename gives path a new inode: other hardlinks to the old file (a seeding
// torrent's copy) keep the old contents. That is intended.
func replaceFile(path string, fill func(dst *os.File) error) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+strings.TrimSuffix(base, ext)+".audiotag-*"+ext)
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

func copyRange(dst io.Writer, src io.ReadSeeker, off, n int64) error {
	if _, err := src.Seek(off, io.SeekStart); err != nil {
		return err
	}
	_, err := io.CopyN(dst, src, n)
	return err
}
