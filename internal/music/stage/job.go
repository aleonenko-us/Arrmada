package stage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Tagger reads tags, and writes them — but only from inside this package: its write
// method is unexported, so nothing else can write tags through a Tagger it is handed
// (and an archtest keeps audiotag.Write and audiotag.New in here too).
type Tagger interface {
	Read(path string) (audiotag.Tags, error)
	ReadFrontCover(path string) ([]byte, error)
	write(path string, t audiotag.Tags, opts audiotag.WriteOptions) error
}

// funcTagger is a Tagger made of functions.
type funcTagger struct {
	read  func(string) (audiotag.Tags, error)
	cover func(string) ([]byte, error)
	w     func(string, audiotag.Tags, audiotag.WriteOptions) error
}

func (f funcTagger) Read(p string) (audiotag.Tags, error)    { return f.read(p) }
func (f funcTagger) ReadFrontCover(p string) ([]byte, error) { return f.cover(p) }
func (f funcTagger) write(p string, t audiotag.Tags, o audiotag.WriteOptions) error {
	return f.w(p, t, o)
}

// NewTagger builds a Tagger from its three functions (tests give fakes).
func NewTagger(read func(string) (audiotag.Tags, error), cover func(string) ([]byte, error),
	write func(string, audiotag.Tags, audiotag.WriteOptions) error) Tagger {
	return funcTagger{read: read, cover: cover, w: write}
}

// DefaultTagger is audiotag with the owner's backend choice: permissive libraries for
// FLAC, MP3 and MP4, TagLib only for Ogg.
func DefaultTagger() Tagger {
	t := audiotag.New(audiotag.DefaultBackend)
	return NewTagger(t.Read, t.ReadFrontCover, t.Write)
}

// Deps is what a job works with.
type Deps struct {
	Tagger Tagger
	// AudioHash returns a fingerprint of a file's decoded audio (ffmpeg's md5 muxer), to
	// check a tag write left the sound alone in formats whose audio bytes a tag write
	// rewrites (MP4, Ogg). nil, or ErrNoDecoder from it, skips that check. FLAC and MP3
	// are checked byte for byte instead (audiotag.AudioRange).
	AudioHash func(ctx context.Context, path string) (string, error)
	// Bin takes replaced library files. A bin that is switched off refuses a replacement:
	// the music import never deletes a file outright.
	Bin library.Bin
	// FreeBytes measures free space on the drive holding path; nil = diskspace.Of.
	FreeBytes func(path string) (uint64, bool)
	Log       *slog.Logger
}

// ErrNoDecoder is what an AudioHash returns when it can't decode here (no ffmpeg): the
// audio comparison is skipped rather than failing every import.
var ErrNoDecoder = errors.New("stage: no audio decoder available")

// Errors a file can fail with. Each leaves the source as it was.
var (
	// ErrVerify: the tagged copy didn't read back as written, or its audio changed. The
	// copy is deleted; the album goes to Reviews.
	ErrVerify = errors.New("the tagged copy didn't check out")
	// ErrTransient: the check couldn't run just now (a decode that timed out, a shutdown).
	// The copy is deleted and the import is tried again later, not sent to Reviews.
	ErrTransient = errors.New("the tagged copy couldn't be checked just now")
	// ErrReplaceRefused: the recycle bin can't take the file a new one would replace.
	ErrReplaceRefused = errors.New("the recycle bin couldn't take the file it would replace")
	// ErrTargetTaken: a different file is at the final path and the job wasn't told to
	// replace it.
	ErrTargetTaken = errors.New("a different file is already where it would go")
	// ErrSameFile: the target is the source itself reached another way (a symlinked or
	// bind-mounted folder, another spelling the filesystem treats as the same name). Moving
	// would mean removing the one name the file has, so it's refused.
	ErrSameFile = errors.New("the new name is this same file reached another way")
	// ErrNoSpace: not enough free space for the copies.
	ErrNoSpace = errors.New("not enough free space for the staging copies")
)

// Job is one staging job. Its methods are safe to call from one goroutine at a time.
type Job struct {
	dir string
	m   Manifest
	d   Deps
	mu  sync.Mutex
}

// Root is the staging folder for a library root.
func Root(libraryRoot string) string { return filepath.Join(libraryRoot, DirName) }

// newID names a job: when it started, and some randomness so two at once never clash.
func newID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Start creates a job under libraryRoot and writes its manifest before anything else
// happens. Items get their staged paths here (imports and retags only).
func Start(libraryRoot string, m Manifest, d Deps) (*Job, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Tagger == nil {
		d.Tagger = DefaultTagger()
	}
	if err := library.PrepareHiddenDir(Root(libraryRoot)); err != nil {
		return nil, fmt.Errorf("stage: preparing %s: %w", Root(libraryRoot), err)
	}
	m.Version = 1
	m.ID = newID(time.Now())
	m.Created = time.Now().UTC()
	dir := filepath.Join(Root(libraryRoot), m.ID)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}
	if m.Kind != KindRename {
		if err := os.Mkdir(filepath.Join(dir, filesDir), 0o755); err != nil {
			_ = os.Remove(dir)
			return nil, fmt.Errorf("stage: %w", err)
		}
	}
	for i := range m.Items {
		it := &m.Items[i]
		it.State = Planned
		if m.Kind != KindRename {
			// A flat, numbered name: what the file is called in the download doesn't
			// matter here, and can't collide or carry odd characters.
			ext := strings.ToLower(filepath.Ext(it.Final))
			if m.Kind == KindSplit {
				ext = ".flac" // a split's pieces go nowhere themselves: they're imported
			}
			it.Staged = filepath.Join(dir, filesDir, fmt.Sprintf("%04d%s", i, ext))
		}
	}
	j := &Job{dir: dir, m: m, d: d}
	if err := writeManifest(dir, &j.m); err != nil {
		_ = os.RemoveAll(dir) // nothing in it yet but what we just made
		return nil, err
	}
	return j, nil
}

// Dir is the job's folder.
func (j *Job) Dir() string { return j.dir }

// Items is a copy of the job's files with their states.
func (j *Job) Items() []Item {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Item(nil), j.m.Items...)
}

// set records item i's new state (and anything else changed) in the manifest.
func (j *Job) set(i int, f func(it *Item)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(&j.m.Items[i])
	return writeManifest(j.dir, &j.m)
}

// CheckSpace refuses the job when the staging drive can't hold every copy plus a margin
// (64 MiB and 2%): a copy that runs the library drive out of space would stop far more
// than this import. An unmeasurable drive passes, as the disk guard does elsewhere.
//
// It measures the filesystem of the job's own folder (statfs on .arrmada-staging/<job>),
// which is where the copies land. On a pooled filesystem (mergerfs, an Unraid user share)
// that is the pool's free space as the pool reports it; which disk a copy actually lands
// on is the pool's choice and can't be seen from here.
//
// A split piece (Item.Scratch) is already on this drive and is deleted as soon as its copy
// is in the library, so only the biggest of them counts: one copy of one piece at a time.
func (j *Job) CheckSpace() error {
	var need, scratch uint64
	for _, it := range j.Items() {
		if it.State != Planned || it.Staged == "" {
			continue
		}
		fi, err := os.Stat(it.Source)
		if err != nil {
			continue
		}
		switch n := uint64(fi.Size()); {
		case !it.Scratch || !j.isScratch(it.Source):
			need += n
		case n > scratch:
			scratch = n
		}
	}
	return j.checkFree(need + scratch)
}

// Stage copies item i's source into the job. Always a copy, never a link: the source may
// be a file a torrent is still seeding (or a library file hardlinked to one), and the copy
// is what gets written. The source is only read.
func (j *Job) Stage(i int) error {
	it := j.Items()[i]
	if it.State != Planned {
		return nil
	}
	if err := copyNew(it.Source, it.Staged); err != nil {
		_ = removeOwn(j.dir, it.Staged)
		return fmt.Errorf("stage: copying %s: %w", filepath.Base(it.Source), err)
	}
	// Belt and braces: the copy must be a file of its own before anything writes to it.
	si, serr := os.Stat(it.Source)
	di, derr := os.Stat(it.Staged)
	if serr != nil || derr != nil || os.SameFile(si, di) || linkCount(di) > 1 {
		_ = removeOwn(j.dir, it.Staged)
		return fmt.Errorf("stage: the staged copy of %s isn't a separate file", filepath.Base(it.Source))
	}
	return j.set(i, func(it *Item) { it.State = Staged })
}

// Tag writes the house tags (and the front cover, when given) into item i's staged copy
// and checks the result: the tags read back as written, the cover reads back, and the
// audio is what the source's was. A copy that fails is deleted; the source is untouched.
// audiotag.ErrUnsupported comes back as is, for the caller to place the file untagged.
func (j *Job) Tag(ctx context.Context, i int, tags audiotag.Tags, cover *audiotag.Picture) error {
	it := j.Items()[i]
	if it.State != Staged {
		return fmt.Errorf("stage: tagging a file in state %s", it.State)
	}
	opts := audiotag.WriteOptions{FrontCover: cover}
	if err := j.d.Tagger.write(it.Staged, tags, opts); err != nil {
		j.discard(i, "tag write failed: "+err.Error())
		return err
	}
	if err := j.verify(ctx, it, tags, cover); err != nil {
		j.discard(i, err.Error())
		return err
	}
	return j.set(i, func(it *Item) { it.State = Verified })
}

// verify checks a tagged copy against what was written and against its source.
func (j *Job) verify(ctx context.Context, it Item, tags audiotag.Tags, cover *audiotag.Picture) error {
	got, err := j.d.Tagger.Read(it.Staged)
	if err != nil {
		return fmt.Errorf("%w: reading the tags back: %v", ErrVerify, err)
	}
	if want := canonical(tags); !reflect.DeepEqual(canonical(got), want) {
		return fmt.Errorf("%w: the tags read back differently (%s)", ErrVerify, firstDiff(canonical(got), want))
	}
	if cover != nil {
		img, err := j.d.Tagger.ReadFrontCover(it.Staged)
		if err != nil || !bytes.Equal(img, cover.Data) {
			return fmt.Errorf("%w: the embedded cover didn't read back (%v)", ErrVerify, err)
		}
	}
	// FLAC and MPEG frames are copied byte for byte by the tag writer: compare those
	// bytes. A decoder isn't a fair judge there — an MP3 decoder can read a big APE tag
	// at the end as a few frames of noise, so the source decodes "differently" from a
	// copy that no longer has it.
	if same, ok, err := sameAudioBytes(it.Source, it.Staged); ok {
		if err != nil {
			return fmt.Errorf("%w: comparing the audio: %v", ErrTransient, err)
		}
		if !same {
			return fmt.Errorf("%w: the audio data changed", ErrVerify)
		}
		// A FLAC's STREAMINFO (sample count, the decoded MD5) and SEEKTABLE describe its
		// frames and must come through the write unchanged too.
		if a, aerr := audiotag.FLACStreamBlocks(it.Source); aerr == nil {
			b, berr := audiotag.FLACStreamBlocks(it.Staged)
			if berr != nil || !bytes.Equal(a, b) {
				return fmt.Errorf("%w: the FLAC stream information changed", ErrVerify)
			}
		}
		return nil
	}
	if j.d.AudioHash == nil {
		return nil
	}
	want, err := j.d.AudioHash(ctx, it.Source)
	if errors.Is(err, ErrNoDecoder) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: decoding the source: %v", ErrTransient, err)
	}
	have, err := j.d.AudioHash(ctx, it.Staged)
	if err != nil {
		return fmt.Errorf("%w: decoding the tagged copy: %v", ErrTransient, err)
	}
	if have != want {
		return fmt.Errorf("%w: the decoded audio changed", ErrVerify)
	}
	return nil
}

// sameAudioBytes compares the encoded audio of two files whose format has it at a fixed
// place (audiotag.AudioRange). ok is false when either has none (MP4, Ogg, unknown).
func sameAudioBytes(a, b string) (same, ok bool, err error) {
	ao, an, aerr := audiotag.AudioRange(a)
	bo, bn, berr := audiotag.AudioRange(b)
	if aerr != nil || berr != nil {
		return false, false, nil
	}
	if an != bn {
		return false, true, nil
	}
	ha, err := hashRange(a, ao, an)
	if err != nil {
		return false, true, err
	}
	hb, err := hashRange(b, bo, bn)
	if err != nil {
		return false, true, err
	}
	return ha == hb, true, nil
}

func hashRange(path string, off, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, off, n)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Untagged marks item i's staged copy as ready without writing tags.
func (j *Job) Untagged(i int) error {
	return j.set(i, func(it *Item) {
		if it.State == Staged {
			it.State = Verified
		}
	})
}

// Place moves item i into the library, never landing on a file it wasn't told to replace
// and never leaving the track without a file:
//
//   - nothing at the final path: the staged copy moves in with one no-clobber move;
//   - a file to replace there: the staged copy is first linked into the album folder
//     under a temporary name (Arriving), then the old file goes to the recycle bin
//     (Binning, then Binned), then the temporary name becomes the final one. Each step is
//     in the manifest before it starts, so a crash anywhere is finished or rolled back
//     (Recover), and a failed last step brings the old file back (Settle).
//
// A rename (Kind "rename") just moves the file, with the same no-clobber rules.
func (j *Job) Place(i int) error {
	it := j.Items()[i]
	if j.m.Kind == KindRename {
		if it.State != Planned {
			return fmt.Errorf("stage: renaming a file in state %s", it.State)
		}
		if err := os.MkdirAll(filepath.Dir(it.Final), 0o755); err != nil {
			return err
		}
		if err := moveNoClobber(it.Source, it.Final); err != nil {
			return err
		}
		return j.set(i, func(it *Item) { it.State = Placed })
	}
	if it.State != Verified {
		return fmt.Errorf("stage: placing a file in state %s", it.State)
	}
	if it.Retire != "" {
		if err := j.binReady(it.Retire); err != nil {
			return fmt.Errorf("%w (%v)", ErrReplaceRefused, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(it.Final), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(it.Final); errors.Is(err, fs.ErrNotExist) {
		// Saved first, so a crash after the move (the copy's name gone, the file at its
		// final path) reads as placed, not as a copy never made.
		if err := j.set(i, func(it *Item) { it.State = Placing }); err != nil {
			return err
		}
		if err := hook(); err != nil {
			return err
		}
		if err := moveNoClobber(it.Staged, it.Final); err != nil {
			_ = j.set(i, func(it *Item) { it.State = Verified })
			return err
		}
		return j.placed(i)
	} else if err != nil {
		return err
	}
	if it.Replace != it.Final {
		return fmt.Errorf("%w: %s", ErrTargetTaken, it.Final)
	}
	if err := j.binReady(it.Final); err != nil {
		return fmt.Errorf("%w (%v)", ErrReplaceRefused, err)
	}

	// 1. The new file arrives beside the old one, under a temporary name of ours.
	tmp := tempName(it.Final, "new")
	if err := j.set(i, func(it *Item) { it.State, it.Temp = Arriving, tmp }); err != nil {
		return err
	}
	_ = os.Remove(tmp) // a leftover of ours from an interrupted try
	if err := linkOrCopyNew(it.Staged, tmp); err != nil {
		_ = os.Remove(tmp)
		_ = j.set(i, func(it *Item) { it.State, it.Temp = Verified, "" })
		return err
	}
	// 2. The old file goes to the bin.
	if err := j.set(i, func(it *Item) { it.State = Binning }); err != nil {
		return err
	}
	moved, err := j.binFile(it.Final)
	if err != nil {
		_ = os.Remove(tmp)
		_ = j.set(i, func(it *Item) { it.State, it.Temp = Verified, "" })
		return fmt.Errorf("%w (%v)", ErrReplaceRefused, err)
	}
	if err := j.set(i, func(it *Item) { it.State, it.Binned = Binned, moved }); err != nil {
		return err
	}
	if err := hook(); err != nil {
		return err
	}
	// 3. The temporary name becomes the final one.
	if err := renameInto(tmp, it.Final); err != nil {
		return err
	}
	return j.placed(i)
}

// placed finishes an item whose file is at its final path: our leftovers (the staged
// copy, the temporary name) go, the old name it retires goes to the bin.
func (j *Job) placed(i int) error {
	it := j.Items()[i]
	j.dropTemp(it)
	if err := removeOwn(j.dir, it.Staged); err != nil {
		j.d.Log.Warn("music: couldn't delete a staged copy", "path", it.Staged, "err", err)
	}
	if err := j.set(i, func(it *Item) { it.State = Placed }); err != nil {
		return err
	}
	j.retire(it)
	j.dropScratch(it)
	return nil
}

// dropTemp removes an item's temporary name in the album folder. It is only ever ours:
// a name we made, whose content is also at the staged copy or the final path.
func (j *Job) dropTemp(it Item) {
	if it.Temp == "" || filepath.Dir(it.Temp) != filepath.Dir(it.Final) || !isTempName(filepath.Base(it.Temp)) {
		return
	}
	if err := os.Remove(it.Temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.d.Log.Warn("music: couldn't remove a temporary file", "path", it.Temp, "err", err)
	}
}

// beforeFinalMove is a test seam: an error from it stops Place just before the final
// move, as a crash would.
var beforeFinalMove func() error

func hook() error {
	if beforeFinalMove != nil {
		return beforeFinalMove()
	}
	return nil
}

// retire sends the old file a new one replaced under another name to the bin. If the bin
// is off or fails now, both stay: nothing is lost, and the log says which file is extra.
func (j *Job) retire(it Item) {
	if it.Retire == "" || it.Retire == it.Final {
		return
	}
	if fi, err := os.Lstat(it.Retire); err != nil {
		return
	} else if di, derr := os.Lstat(it.Final); derr == nil && os.SameFile(fi, di) {
		return // another spelling of the file just placed
	}
	if moved, err := j.binFile(it.Retire); err != nil {
		j.d.Log.Warn("music: kept the replaced file — the recycle bin couldn't take it", "path", it.Retire, "err", err)
	} else {
		j.d.Log.Info("recycled replaced file", "target", it.Retire, "recycled_to", moved)
	}
}

// binReady checks that path could go to the recycle bin now. A bin that is switched off
// is a refusal: a replaced music file is only ever moved to the bin.
func (j *Job) binReady(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	dir, err := j.binDir(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func (j *Job) binDir(path string) (string, error) {
	if j.d.Bin == nil {
		return "", library.ErrRecycleDisabled
	}
	return j.d.Bin.For(path)
}

// binFile moves path into the recycle bin and says where it went. Unlike
// library.RemoveToBin, a bin that is switched off is an error, never a delete.
func (j *Job) binFile(path string) (string, error) {
	dir, err := j.binDir(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	moved, err := library.RecycleFile(dir, path)
	if err != nil {
		return "", err
	}
	if moved == "" {
		return "", fmt.Errorf("%s wasn't there to move to the bin", path)
	}
	return moved, nil
}

// SetMeta stores what item i will be recorded with (see Item.Meta).
func (j *Job) SetMeta(i int, meta json.RawMessage) error {
	return j.set(i, func(it *Item) { it.Meta = meta })
}

// Settle tidies item i after a Place that failed. A file whose old version already went
// to the bin gets its old file back (whatever went wrong, the track keeps a file); if the
// new one did land after all, it stays and the item is Placed. Anything earlier is
// discarded. After it the item is Placed (record it), Discarded, or — when the old file
// can't come back either — still Binned, kept for Recover; nothing is deleted then.
func (j *Job) Settle(i int) error {
	it := j.Items()[i]
	switch it.State {
	case Placed, Recorded, Discarded:
		return nil
	case Binned:
		if landed(it) {
			return j.placed(i)
		}
		return j.restore(i)
	case Placing:
		if movedIn(it) {
			return j.placed(i)
		}
	}
	if j.m.Kind != KindRename {
		j.dropTemp(it)
	}
	j.discard(i, "not placed")
	return nil
}

// landed reports whether item it's new file is at its final path: the same file as its
// temporary name or staged copy, or — both of ours gone — whatever is there now, which the
// final move put there (nothing else writes a track's path in between).
//
// "The same file" is the same inode, or — where the staging folder and the album folder
// can't share a hard link (mergerfs, a separately mounted album folder), so the new file
// arrived as a copy — the same bytes.
func landed(it Item) bool {
	if !exists(it.Final) {
		return false
	}
	for _, p := range []string{it.Temp, it.Staged} {
		if p == "" || !exists(p) {
			continue
		}
		return sameFile(it.Final, p) || sameContent(it.Final, p)
	}
	return true
}

// movedIn reports whether a plain move (Placing) happened: the file is at its final path
// and the staged copy is gone, or still there as the same file or the same bytes (a copy
// across drives whose old name wasn't removed yet).
func movedIn(it Item) bool {
	if !exists(it.Final) {
		return false
	}
	if !exists(it.Staged) {
		return true
	}
	return sameFile(it.Final, it.Staged) || sameContent(it.Final, it.Staged)
}

// sameContent reports whether two files hold the same bytes (sizes first, then a hash of
// each). Recovery and Settle only: it reads both files whole.
func sameContent(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil || ai.Size() != bi.Size() {
		return false
	}
	ha, err := hashRange(a, 0, ai.Size())
	if err != nil {
		return false
	}
	hb, err := hashRange(b, 0, bi.Size())
	return err == nil && ha == hb
}

// restore brings a binned file back to where it was, then drops our copies.
func (j *Job) restore(i int) error {
	it := j.Items()[i]
	if it.Binned == "" {
		return fmt.Errorf("stage: %s is in the recycle bin and its place there wasn't recorded", it.Replace)
	}
	if _, err := os.Lstat(it.Binned); err != nil {
		return fmt.Errorf("stage: the replaced file isn't in the recycle bin any more (%s): %w", it.Binned, err)
	}
	if err := moveNoClobber(it.Binned, it.Replace); err != nil {
		return fmt.Errorf("the replaced file is in the recycle bin (%s) and couldn't go back: %w", it.Binned, err)
	}
	_ = os.Remove(it.Binned + library.RecycleMetaExt) // the bin's note about it, now stale
	j.dropTemp(it)
	j.discard(i, "rolled back: the old file is back in place")
	return nil
}

// Recorded marks item i done: the database knows about it.
func (j *Job) Recorded(i int) error {
	return j.set(i, func(it *Item) { it.State = Recorded })
}

// Discard drops item i: its staged copy is deleted (it's ours; the source is untouched).
// A file already in the bin or placed isn't discardable: it's the track's file now.
func (j *Job) Discard(i int, why string) error {
	switch st := j.Items()[i].State; st {
	case Binning, Binned, Placed, Recorded:
		return fmt.Errorf("stage: a %s file can't be discarded", st)
	}
	j.discard(i, why)
	return nil
}

func (j *Job) discard(i int, why string) {
	it := j.Items()[i]
	if err := removeOwn(j.dir, it.Staged); err != nil {
		j.d.Log.Warn("music: couldn't delete a staged copy", "path", it.Staged, "err", err)
	}
	if err := j.set(i, func(it *Item) { it.State, it.Note = Discarded, why }); err != nil {
		j.d.Log.Warn("music: couldn't save the staging manifest", "job", j.m.ID, "err", err)
	}
}

// Close ends the job. Every file must be settled (recorded or discarded); anything else
// keeps the job folder for Recover, and Close says so. A settled job's folder and
// leftover copies are removed.
func (j *Job) Close() error {
	var open []string
	for _, it := range j.Items() {
		if !it.State.terminal() {
			open = append(open, filepath.Base(it.Final)+" ("+string(it.State)+")")
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("stage: job %s kept for recovery: %s", j.m.ID, strings.Join(open, ", "))
	}
	return removeJobDir(j.dir)
}

// Abandon settles a job a caller gave up on part way: copies that never reached the
// library are deleted, and the job closes if nothing else is open.
func (j *Job) Abandon(why string) error {
	for i, it := range j.Items() {
		switch it.State {
		case Planned, Staged, Verified:
			j.discard(i, why)
		case Arriving:
			j.dropTemp(it)
			j.discard(i, why)
		}
	}
	return j.Close()
}

// removeJobDir deletes a settled job's folder: its manifest and whatever copies are left
// in files/. Only a folder named like a job, inside a staging folder, is ever removed.
func removeJobDir(dir string) error {
	if !reJobID.MatchString(filepath.Base(dir)) || filepath.Base(filepath.Dir(dir)) != DirName {
		return ErrNotInJob
	}
	return os.RemoveAll(dir)
}

// copyNew copies src to dst (which must not exist) through a temporary name, synced, and
// then linked into place, so dst is either absent or complete and nothing at dst is
// ever replaced.
func copyNew(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := tempName(dst, "part")
	_ = os.Remove(tmp) // ours, from an interrupted try
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameInto(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// canonical is tags as a reader reports them: trimmed, non-empty values, numbers without
// leading zeros, so what was written and what reads back compare equal.
func canonical(t audiotag.Tags) audiotag.Tags {
	out := audiotag.Tags{}
	for k, vs := range t {
		k = strings.ToUpper(strings.TrimSpace(k))
		for _, v := range vs {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			switch k {
			case audiotag.KeyTrackNumber, audiotag.KeyTrackTotal, audiotag.KeyDiscNumber, audiotag.KeyDiscTotal:
				if n, err := strconv.Atoi(v); err == nil {
					v = strconv.Itoa(n)
				}
			}
			out[k] = append(out[k], v)
		}
	}
	return out
}

// firstDiff names the first key that differs, for the Reviews message.
func firstDiff(got, want audiotag.Tags) string {
	for _, k := range audiotag.HouseKeys() {
		if !reflect.DeepEqual(got[k], want[k]) {
			return fmt.Sprintf("%s: wrote %q, read %q", k, want[k], got[k])
		}
	}
	return "unknown field"
}

// CoverName is the album cover the house style writes beside the tracks (the Cover Art
// Archive's 1200 px front). A cover a download brings is kept as folder.<ext> (MUS-36).
const CoverName = "cover.jpg"

// PlaceCover writes the album's cover.jpg into albumDir through the job: written into
// staging first, then moved in with one no-clobber move. An album folder that already has
// a cover.jpg keeps it.
func (j *Job) PlaceCover(albumDir string, data []byte) (bool, error) {
	if len(data) == 0 {
		return false, nil
	}
	dst := filepath.Join(albumDir, CoverName)
	if _, err := os.Lstat(dst); err == nil {
		return false, nil
	}
	if j.m.Kind == KindRename {
		return false, errors.New("stage: a rename has no files to stage a cover in")
	}
	tmp := filepath.Join(j.dir, filesDir, CoverName)
	if err := os.WriteFile(tmp+".part", data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp+".part", tmp); err != nil {
		_ = os.Remove(tmp + ".part")
		return false, err
	}
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		_ = removeOwn(j.dir, tmp)
		return false, err
	}
	if err := moveNoClobber(tmp, dst); err != nil {
		_ = removeOwn(j.dir, tmp)
		if errors.Is(err, ErrTargetTaken) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
