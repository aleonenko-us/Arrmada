// Package extract unpacks archives in a completed download (RAR, including
// multi-part sets, and ZIP) so the importer can find the media inside. This is
// the Unpackerr functionality, folded into Arrmada's pipeline.
package extract

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nwaples/rardecode/v2"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

var reRarPart = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)

// Limits on what one archive may unpack to. A download is untrusted input: a small
// archive that inflates to terabytes, or holds millions of entries, must not fill the
// disk or tie the importer up. Real releases are nowhere near them: scene RARs are
// stored (unpacked ≈ packed) and a zip of video barely compresses.
//
// Vars so tests can shrink them.
var (
	// maxUnpackedBytes caps the total bytes a single extraction call tree (ExtractAll /
	// ExtractTree / ExtractArchive) may unpack, every archive in it together.
	maxUnpackedBytes int64 = 200 << 30 // 200 GiB

	// An archive may unpack to at most its own size on disk (every volume of a RAR set)
	// times ratioNum/ratioDen, plus a slack for small, compressible extras (subtitles,
	// NFOs). An archive found inside another gets far less slack: a bomb is archives in
	// archives, each level inflating the last.
	ratioNum, ratioDen int64 = 3, 2 // 1.5×
	topSlack           int64 = 1 << 30
	nestedSlack        int64 = 16 << 20

	// maxEntries is how many entries (files and folders) one archive may hold.
	maxEntries = 5000

	// maxNestDepth is how deep archives inside archives are followed: the archive itself
	// is depth 0, one it contains depth 1, and so on. Deeper ones are left packed.
	maxNestDepth = 2

	// freeBytes is the space free where an archive is unpacked; ok is false when it
	// can't be told (then nothing is refused for it).
	freeBytes = func(dir string) (uint64, bool) {
		u, ok := diskspace.Of(dir)
		return u.FreeBytes, ok
	}
)

// ErrUnpackLimit is returned when an extraction would exceed an unpack cap: the call's
// total, or an archive's own (its size × 1.5, plus slack).
var ErrUnpackLimit = errors.New("archive unpack limit exceeded")

// ErrTooManyEntries is returned for an archive with more entries than maxEntries.
var ErrTooManyEntries = errors.New("archive has too many entries")

// ErrNoSpace is returned when what an archive says it holds won't fit on the disk.
var ErrNoSpace = errors.New("not enough free space to unpack the archive")

// budget tracks how many bytes an extraction call tree may still write.
type budget struct{ remaining int64 }

// IsArchive reports whether path looks like an archive the extractor can open.
func IsArchive(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".rar")
}

// ExtractArchive extracts a single archive file into destDir (flattened, like the
// directory extractors). A non-first RAR volume is a no-op — rardecode follows the
// chain from the first volume.
func ExtractArchive(archivePath, destDir string) error {
	b := &budget{remaining: maxUnpackedBytes}
	if !IsArchive(archivePath) || !isFirstRarVolume(filepath.Base(archivePath)) {
		return nil
	}
	nested := loadNested(destDir)
	defer nested.save()
	return extractNested(archivePath, destDir, b, 0, nested)
}

// ExtractAll finds archives directly inside dir and extracts them into dir. For
// multi-part RAR sets only the first volume is opened (rardecode follows the
// chain). Returns how many archives were extracted.
func ExtractAll(dir string) (int, error) {
	return extractAll(dir, &budget{remaining: maxUnpackedBytes})
}

func extractAll(dir string, b *budget) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	nested := loadNested(dir)
	defer nested.save()

	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !IsArchive(name) || !isFirstRarVolume(name) {
			continue
		}
		full := filepath.Join(dir, name)
		// One that came out of another archive here is unpacked with that one, under its
		// limits and depth, never again on its own: a later pass would otherwise peel a
		// bomb one more level each time with a fresh allowance.
		if nested.has(full) {
			continue
		}
		if err := extractNested(full, dir, b, 0, nested); err != nil {
			return count, fmt.Errorf("extract %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// ExtractTree extracts archives found anywhere under dir (recursively), so a season
// pack whose episodes each live in their own subfolder of RARs is fully unpacked.
// Best-effort: a failure in one folder doesn't stop the rest — except blowing an
// unpack cap or running out of space, which aborts the walk. Returns the total count.
func ExtractTree(dir string) (int, error) {
	total := 0
	var firstErr error
	b := &budget{remaining: maxUnpackedBytes} // one cap for the whole tree
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		n, e := extractAll(p, b)
		total += n
		if e != nil && firstErr == nil {
			firstErr = e
		}
		if errors.Is(e, ErrUnpackLimit) || errors.Is(e, ErrTooManyEntries) || errors.Is(e, ErrNoSpace) {
			return filepath.SkipAll // a hostile or oversized download: stop unpacking it
		}
		return nil
	})
	return total, firstErr
}

// extractNested unpacks one archive and then, up to maxNestDepth, the archives it held,
// all against the same call budget. Every archive that came out of it is recorded in
// nested, so a later pass leaves it to this one.
func extractNested(archivePath, destDir string, b *budget, depth int, nested *nestedSet) error {
	outs, err := extractOne(archivePath, destDir, b, depth)
	if err != nil {
		return err
	}
	for _, out := range outs {
		if !IsArchive(out) {
			continue
		}
		nested.add(out)
		if depth+1 > maxNestDepth || !isFirstRarVolume(filepath.Base(out)) {
			continue
		}
		if err := extractNested(out, destDir, b, depth+1, nested); err != nil {
			return fmt.Errorf("inside %s: %w", filepath.Base(archivePath), err)
		}
	}
	return nil
}

// isFirstRarVolume reports whether name is the first volume of a RAR set. Plain
// ".rar" (old-style .rar/.r00/.r01…) is first; ".partNN.rar" only when NN == 1.
func isFirstRarVolume(name string) bool {
	if m := reRarPart.FindStringSubmatch(name); m != nil {
		return strings.TrimLeft(m[1], "0") == "1"
	}
	return true
}

// archiveSize is the size on disk of the archive at path: for a RAR set, every volume
// of it beside the first (name.partNN.rar, or the old name.rNN / name.sNN).
func archiveSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	size := fi.Size()
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	if !strings.HasSuffix(lower, ".rar") {
		return size
	}
	var same func(string) bool
	if m := reRarPart.FindStringIndex(base); m != nil {
		stem := strings.ToLower(base[:m[0]])
		same = func(n string) bool {
			l := strings.ToLower(n)
			return l != lower && strings.HasPrefix(l, stem) && reRarPart.MatchString(n) && reRarPart.FindStringIndex(n)[0] == len(stem)
		}
	} else {
		stem := lower[:len(lower)-len(".rar")]
		same = func(n string) bool {
			l := strings.ToLower(n)
			if len(l) != len(stem)+4 || !strings.HasPrefix(l, stem+".") {
				return false
			}
			ext := l[len(stem)+1:]
			_, err := strconv.Atoi(ext[1:])
			return (ext[0] == 'r' || ext[0] == 's') && err == nil
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return size
	}
	for _, e := range entries {
		if e.IsDir() || !same(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			size += info.Size()
		}
	}
	return size
}

// archiveLimit is how many bytes the archive at path may unpack to (see ratioNum).
func archiveLimit(path string, depth int) int64 {
	slack := topSlack
	if depth > 0 {
		slack = nestedSlack
	}
	return archiveSize(path)/ratioDen*ratioNum + slack
}

// entry is one entry an archive says it holds.
type entry struct {
	dir  bool
	name string
	size int64 // what the archive's headers declare for a file; -1 when unknown
}

// extractOne unpacks one archive into destDir and returns every file it holds there
// (written now or by an earlier pass). Before anything is written, what the archive
// declares is checked against its limits and the free space; the actual bytes are capped
// too while writing, since headers can lie.
func extractOne(archivePath, destDir string, b *budget, depth int) ([]string, error) {
	var (
		entries []entry
		err     error
		isZip   = strings.HasSuffix(strings.ToLower(archivePath), ".zip")
	)
	if isZip {
		entries, err = listZip(archivePath)
	} else {
		entries, err = listRar(archivePath)
	}
	if err != nil {
		return nil, err
	}
	limit := archiveLimit(archivePath, depth)
	if err := checkPlan(archivePath, destDir, entries, limit, b); err != nil {
		return nil, err
	}
	w := &writer{destDir: destDir, b: b, limit: limit, used: map[string]int{}}
	if isZip {
		err = w.zip(archivePath)
	} else {
		err = w.rar(archivePath)
	}
	return w.outs, err
}

// checkPlan refuses an archive with too many entries, one that declares more than it may
// unpack to, and one whose new files won't fit in the free space.
func checkPlan(archivePath, destDir string, entries []entry, limit int64, b *budget) error {
	name := filepath.Base(archivePath)
	if len(entries) > maxEntries {
		return fmt.Errorf("%w: %s has %d (at most %d)", ErrTooManyEntries, name, len(entries), maxEntries)
	}
	var declared, need int64
	used := map[string]int{}
	for _, e := range entries {
		if e.dir {
			continue
		}
		dst := filepath.Join(destDir, flatName(used, e.name)) // in the order the writer names them
		if e.size < 0 {
			continue // unknown: the cap while writing still applies
		}
		declared += e.size
		if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
			continue // unpacked by an earlier pass: written again, it would be skipped
		}
		need += e.size
	}
	if declared > limit {
		return fmt.Errorf("%w: %s says it unpacks to %d bytes, more than %d for an archive of its size", ErrUnpackLimit, name, declared, limit)
	}
	if need > b.remaining {
		return fmt.Errorf("%w: %s would take the unpacked total past %d bytes", ErrUnpackLimit, name, maxUnpackedBytes)
	}
	if free, ok := freeBytes(destDir); ok && need > 0 && uint64(need) > free {
		return fmt.Errorf("%w: %s needs %d bytes, %d free", ErrNoSpace, name, need, free)
	}
	return nil
}

func listZip(archivePath string) ([]entry, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if len(r.File) > maxEntries {
		return make([]entry, len(r.File)), nil // checkPlan refuses it on the count alone
	}
	out := make([]entry, 0, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			out = append(out, entry{dir: true})
			continue
		}
		size := maxUnpackedBytes + 1 // more than anything may unpack to
		if f.UncompressedSize64 <= uint64(maxUnpackedBytes) {
			size = int64(f.UncompressedSize64)
		}
		out = append(out, entry{name: f.Name, size: size})
	}
	return out, nil
}

func listRar(archivePath string) ([]entry, error) {
	files, err := rardecode.List(archivePath)
	if err != nil {
		return nil, err
	}
	out := make([]entry, 0, min(len(files), maxEntries+1))
	for _, f := range files {
		switch {
		case f.IsDir:
			out = append(out, entry{dir: true})
		case f.UnKnownSize || f.UnPackedSize < 0:
			out = append(out, entry{name: f.Name, size: -1})
		default:
			out = append(out, entry{name: f.Name, size: f.UnPackedSize})
		}
	}
	return out, nil
}

// writer unpacks one archive's files into destDir under both the call budget and the
// archive's own limit.
type writer struct {
	destDir string
	b       *budget
	limit   int64
	used    map[string]int
	outs    []string
	entries int
}

func (w *writer) next() error {
	w.entries++
	if w.entries > maxEntries {
		return fmt.Errorf("%w (more than %d)", ErrTooManyEntries, maxEntries)
	}
	return nil
}

func (w *writer) zip(archivePath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := w.next(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		err = w.write(in, f.Name)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) rar(archivePath string) error {
	rc, err := rardecode.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer rc.Close()
	for {
		hdr, err := rc.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := w.next(); err != nil {
			return err
		}
		if hdr.IsDir {
			continue
		}
		if err := w.write(rc, hdr.Name); err != nil {
			return err
		}
	}
}

func (w *writer) write(in io.Reader, entryName string) error {
	dst := filepath.Join(w.destDir, flatName(w.used, entryName))
	if err := writeFile(in, dst, w.b, &w.limit); err != nil {
		return err
	}
	w.outs = append(w.outs, dst)
	return nil
}

// flatName returns the flattened output name for an archive entry, disambiguating
// deterministically when two entries collide on basename ("a/movie.mkv" and
// "b/movie.mkv" become "movie.mkv" and "movie.2.mkv") instead of silently dropping
// the second. Entry order in an archive is stable, so re-extraction is idempotent.
// filepath.Base keeps this zip-slip safe.
func flatName(used map[string]int, entryName string) string {
	base := filepath.Base(entryName)
	n := used[base]
	used[base] = n + 1
	if n == 0 {
		return base
	}
	ext := filepath.Ext(base)
	return fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), n+1, ext)
}

// writeFile extracts one entry to dst atomically: it writes a temp file in the
// destination directory and renames it into place on success, so a completed dst is
// always a fully-extracted file and a crash never leaves a plausible-looking partial.
// It writes at most what both the call budget b and the archive's own limit allow.
func writeFile(in io.Reader, dst string, b *budget, limit *int64) error {
	// Idempotent: if we've already extracted this file, don't rewrite it. A season pack
	// gets re-scanned while it still has missing episodes, and re-copying multi-GB videos
	// every pass would be pointless churn. Safe: completed files only exist via the
	// rename below, so an existing dst is never a partial.
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return nil
	}
	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.arrmada-tmp")
	if err != nil {
		return err
	}
	tmp := out.Name()
	fail := func(e error) error {
		out.Close()
		_ = os.Remove(tmp)
		return e
	}
	// Copy at most the smaller allowance (+1 so exceeding is detectable): the zip-bomb
	// guard, whatever the headers said.
	allow := min(b.remaining, *limit)
	n, err := io.Copy(out, io.LimitReader(in, allow+1))
	b.remaining -= n
	*limit -= n
	if n > allow {
		return fail(fmt.Errorf("%w extracting %s", ErrUnpackLimit, filepath.Base(dst)))
	}
	if err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil { // CreateTemp makes 0600
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// nestedFile, in a folder archives were unpacked into, lists the archives that came out
// of other archives there: name, size and modification time, so a file that's replaced
// later (a new download under the same name) counts as new.
const nestedFile = ".arrmada-nested-archives"

// nestedSet is one folder's nestedFile, loaded and saved around a pass.
type nestedSet struct {
	path    string
	keys    map[string]bool
	changed bool
}

func nestedKey(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d\t%d\t%s", fi.Size(), fi.ModTime().UnixNano(), filepath.Base(path)), true
}

func loadNested(dir string) *nestedSet {
	s := &nestedSet{path: filepath.Join(dir, nestedFile), keys: map[string]bool{}}
	f, err := os.Open(s.path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			s.keys[line] = true
		}
	}
	return s
}

func (s *nestedSet) has(path string) bool {
	k, ok := nestedKey(path)
	return ok && s.keys[k]
}

func (s *nestedSet) add(path string) {
	if k, ok := nestedKey(path); ok && !s.keys[k] {
		s.keys[k] = true
		s.changed = true
	}
}

// save writes the list back when it grew. Best effort: without it a later pass unpacks
// a nested archive on its own, which its own limits still bound.
func (s *nestedSet) save() {
	if !s.changed {
		return
	}
	var sb strings.Builder
	for k := range s.keys {
		sb.WriteString(k)
		sb.WriteByte('\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
	}
}
