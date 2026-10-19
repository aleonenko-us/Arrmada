package stage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Tagger writes and reads tags. audiotag's Auto backend in the app (DefaultTagger); tests
// give a fake.
type Tagger interface {
	Read(path string) (audiotag.Tags, error)
	Write(path string, t audiotag.Tags, opts audiotag.WriteOptions) error
	ReadFrontCover(path string) ([]byte, error)
}

// DefaultTagger is audiotag with the owner's backend choice: permissive libraries for
// FLAC, MP3 and MP4, TagLib only for Ogg.
func DefaultTagger() Tagger { return audiotag.New(audiotag.DefaultBackend) }

// Deps is what a job works with.
type Deps struct {
	Tagger Tagger
	// AudioHash returns a fingerprint of a file's decoded audio (ffmpeg's md5 muxer), to
	// check a tag write left the sound alone. nil, or ErrNoDecoder from it, skips that
	// check (the tag read-back still runs).
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
	// ErrReplaceRefused: the recycle bin can't take the file a new one would replace.
	ErrReplaceRefused = errors.New("the recycle bin couldn't take the file it would replace")
	// ErrTargetTaken: a different file is at the final path and the job wasn't told to
	// replace it.
	ErrTargetTaken = errors.New("a different file is already where it would go")
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
			it.Staged = filepath.Join(dir, filesDir, fmt.Sprintf("%04d%s", i, strings.ToLower(filepath.Ext(it.Final))))
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
func (j *Job) CheckSpace() error {
	var need uint64
	for _, it := range j.Items() {
		if it.State != Planned || it.Staged == "" {
			continue
		}
		if fi, err := os.Stat(it.Source); err == nil {
			need += uint64(fi.Size())
		}
	}
	if need == 0 {
		return nil
	}
	free := j.d.FreeBytes
	if free == nil {
		free = func(p string) (uint64, bool) {
			u, ok := diskspace.Of(p)
			return u.FreeBytes, ok
		}
	}
	have, ok := free(j.dir)
	if !ok {
		return nil
	}
	margin := uint64(64<<20) + need/50
	if have < need+margin {
		return fmt.Errorf("%w: %d MB needed, %d MB free", ErrNoSpace, (need+margin)>>20, have>>20)
	}
	return nil
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
// decoded audio is what the source's was. A copy that fails is deleted; the source is
// untouched. audiotag.ErrUnsupported comes back as is, for the caller to place the file
// untagged instead.
func (j *Job) Tag(ctx context.Context, i int, tags audiotag.Tags, cover *audiotag.Picture) error {
	it := j.Items()[i]
	if it.State != Staged {
		return fmt.Errorf("stage: tagging a file in state %s", it.State)
	}
	opts := audiotag.WriteOptions{FrontCover: cover}
	if err := j.d.Tagger.Write(it.Staged, tags, opts); err != nil {
		j.discard(i, "tag write failed: "+err.Error())
		return err
	}
	if err := j.verify(ctx, it, tags, cover); err != nil {
		j.discard(i, err.Error())
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	return j.set(i, func(it *Item) { it.State = Verified })
}

// verify checks a tagged copy against what was written and against its source.
func (j *Job) verify(ctx context.Context, it Item, tags audiotag.Tags, cover *audiotag.Picture) error {
	got, err := j.d.Tagger.Read(it.Staged)
	if err != nil {
		return fmt.Errorf("reading the tags back: %v", err)
	}
	if want := canonical(tags); !reflect.DeepEqual(canonical(got), want) {
		return fmt.Errorf("the tags read back differently (%s)", firstDiff(canonical(got), want))
	}
	if cover != nil {
		img, err := j.d.Tagger.ReadFrontCover(it.Staged)
		if err != nil || !reflectBytes(img, cover.Data) {
			return fmt.Errorf("the embedded cover didn't read back (%v)", err)
		}
	}
	if j.d.AudioHash == nil {
		return nil
	}
	want, err := j.d.AudioHash(ctx, it.Source)
	if errors.Is(err, ErrNoDecoder) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decoding the source: %v", err)
	}
	have, err := j.d.AudioHash(ctx, it.Staged)
	if err != nil {
		return fmt.Errorf("decoding the tagged copy: %v", err)
	}
	if have != want {
		return errors.New("the decoded audio changed")
	}
	return nil
}

// Untagged marks item i's staged copy as ready without writing tags (a file the tag
// library can't handle goes in as it arrived). Mostly the caller hardlinks such a file
// straight in instead; this is for a retag, where the copy is already made.
func (j *Job) Untagged(i int) error {
	return j.set(i, func(it *Item) {
		if it.State == Staged {
			it.State = Verified
		}
	})
}

// Place moves item i into the library: the file it replaces goes to the recycle bin
// first (refusing if the bin can't take it), then the staged copy (or, for a rename, the
// file itself) takes its final name with one no-clobber move, and an old file it retires
// goes to the bin. A staged copy never lands on a file it wasn't told to replace.
func (j *Job) Place(i int) error {
	it := j.Items()[i]
	from := it.Staged
	if j.m.Kind == KindRename {
		from = it.Source
	}
	switch it.State {
	case Verified:
	case Planned:
		if j.m.Kind != KindRename {
			return fmt.Errorf("stage: placing a file in state %s", it.State)
		}
	case Binned:
		return j.finishPlace(i, from)
	default:
		return fmt.Errorf("stage: placing a file in state %s", it.State)
	}
	if it.Replace != "" || it.Retire != "" {
		for _, p := range []string{it.Replace, it.Retire} {
			if p != "" {
				if err := j.binReady(p); err != nil {
					return fmt.Errorf("%w (%v)", ErrReplaceRefused, err)
				}
			}
		}
	}
	if it.Replace != "" && it.Replace != it.Final {
		// Replacing a file under another name is Retire's job; Replace is the file at Final.
		return fmt.Errorf("stage: replace %s isn't the final path", it.Replace)
	}
	if di, err := os.Lstat(it.Final); err == nil {
		fi, ferr := os.Lstat(from)
		switch {
		case ferr == nil && os.SameFile(fi, di):
			// A rename onto another name of the same file (case-only, or a hardlink).
		case it.Replace == it.Final:
			moved, err := library.RemoveToBin(j.d.Bin, it.Final)
			if err != nil {
				return fmt.Errorf("%w (%v)", ErrReplaceRefused, err)
			}
			if err := j.set(i, func(it *Item) { it.State, it.Binned = Binned, moved }); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: %s", ErrTargetTaken, it.Final)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if beforeFinalMove != nil {
		if err := beforeFinalMove(); err != nil {
			return err
		}
	}
	return j.finishPlace(i, from)
}

// beforeFinalMove is a test seam: an error from it stops Place just before the final
// move, as a crash would.
var beforeFinalMove func() error

// finishPlace is Place from the point the target is free.
func (j *Job) finishPlace(i int, from string) error {
	it := j.Items()[i]
	if err := os.MkdirAll(filepath.Dir(it.Final), 0o755); err != nil {
		return err
	}
	if err := moveNoClobber(from, it.Final); err != nil {
		return err
	}
	if err := j.set(i, func(it *Item) { it.State = Placed }); err != nil {
		return err
	}
	j.retire(it)
	return nil
}

// retire sends the old file a new one replaced under another name to the bin. If the bin
// fails now, both stay: nothing is lost, and the log says which file is extra.
func (j *Job) retire(it Item) {
	if it.Retire == "" || it.Retire == it.Final {
		return
	}
	if fi, err := os.Lstat(it.Retire); err != nil {
		return
	} else if di, derr := os.Lstat(it.Final); derr == nil && os.SameFile(fi, di) {
		return // another spelling of the file just placed
	}
	if moved, err := library.RemoveToBin(j.d.Bin, it.Retire); err != nil {
		j.d.Log.Warn("music: kept the replaced file — the recycle bin couldn't take it", "path", it.Retire, "err", err)
	} else {
		j.d.Log.Info("recycled replaced file", "target", it.Retire, "recycled_to", moved)
	}
}

// binReady checks that path could go to the recycle bin now. A bin that is switched off
// is a refusal: a replaced music file is only ever moved to the bin.
func (j *Job) binReady(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if j.d.Bin == nil {
		return library.ErrRecycleDisabled
	}
	dir, err := j.d.Bin.For(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// SetMeta stores what item i will be recorded with (see Item.Meta).
func (j *Job) SetMeta(i int, meta json.RawMessage) error {
	return j.set(i, func(it *Item) { it.Meta = meta })
}

// Settle tidies item i after a Place that failed. A file whose old version already went
// to the bin is put in place after all, or else the old file comes back out of the bin;
// a file that never got that far is discarded. After it, the item is Placed (record it),
// Discarded, or — when neither could be done — still Binned, kept for Recover.
func (j *Job) Settle(i int) error {
	switch j.Items()[i].State {
	case Binned:
		var rep RecoverReport
		return j.recoverBinned(i, &rep)
	case Placed, Recorded, Discarded:
		return nil
	}
	j.discard(i, "not placed")
	return nil
}

// Recorded marks item i done: the database knows about it.
func (j *Job) Recorded(i int) error {
	return j.set(i, func(it *Item) { it.State = Recorded })
}

// Discard drops item i: its staged copy is deleted (it's ours; the source is untouched).
// A file already binned or placed isn't discardable: it's the track's file now.
func (j *Job) Discard(i int, why string) error {
	switch j.Items()[i].State {
	case Binned, Placed, Recorded:
		return fmt.Errorf("stage: a %s file can't be discarded", j.Items()[i].State)
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
// (a file the bin took whose copy couldn't be placed) keeps the job folder for Recover,
// and Close says so. A settled job's folder and leftover copies are removed.
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

// copyNew copies src to dst (which must not exist yet) through a temporary name, synced
// before the rename, so dst is either absent or complete.
func copyNew(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
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
		if _, serr := os.Lstat(dst); serr == nil {
			err = fmt.Errorf("%s already exists", dst)
		} else {
			err = os.Rename(tmp, dst)
		}
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

func reflectBytes(a, b []byte) bool { return string(a) == string(b) }

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
