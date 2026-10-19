package stage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/library"
)

// fakeTagger stands in for audiotag on byte blobs: a file's "tags" are a JSON header line
// in front of its "audio". Like audiotag it writes a new file and renames it over the old
// one, so a write breaks any hard link.
type fakeTagger struct {
	corrupt bool // write different values than asked (a broken writer)
	fail    error
}

const tagMagic = "FAKETAGS:"

func splitFake(b []byte) (audiotag.Tags, []byte, []byte) {
	if !bytes.HasPrefix(b, []byte(tagMagic)) {
		return audiotag.Tags{}, nil, b
	}
	line, rest, _ := bytes.Cut(b, []byte("\n"))
	var t struct {
		Tags  audiotag.Tags
		Cover []byte
	}
	_ = json.Unmarshal(line[len(tagMagic):], &t)
	return t.Tags, t.Cover, rest
}

func (f *fakeTagger) Read(p string) (audiotag.Tags, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	t, _, _ := splitFake(b)
	return t, nil
}

func (f *fakeTagger) ReadFrontCover(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	_, c, _ := splitFake(b)
	if len(c) == 0 {
		return nil, audiotag.ErrNoCover
	}
	return c, nil
}

func (f *fakeTagger) write(p string, t audiotag.Tags, opts audiotag.WriteOptions) error {
	if f.fail != nil {
		return f.fail
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	_, oldCover, audio := splitFake(b)
	if f.corrupt {
		t = audiotag.Tags{audiotag.KeyTitle: {"something else"}}
	}
	cover := oldCover
	if opts.FrontCover != nil {
		cover = opts.FrontCover.Data
	}
	hdr, _ := json.Marshal(struct {
		Tags  audiotag.Tags
		Cover []byte
	}{t, cover})
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(append([]byte(tagMagic), hdr...), append([]byte("\n"), audio...)...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// fakeHash fingerprints the "audio" part of a blob.
func fakeHash(_ context.Context, p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	_, _, audio := splitFake(b)
	s := sha256.Sum256(audio)
	return hex.EncodeToString(s[:]), nil
}

type world struct {
	t        *testing.T
	root     string // the music library
	dl       string // the download folder
	bin      string
	deps     Deps
	recorded []Item
}

func newWorld(t *testing.T) *world {
	t.Helper()
	base := t.TempDir()
	w := &world{t: t, root: filepath.Join(base, "music"), dl: filepath.Join(base, "downloads"), bin: filepath.Join(base, "bin")}
	for _, d := range []string{w.root, w.dl, w.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w.deps = Deps{Tagger: &fakeTagger{}, AudioHash: fakeHash, Bin: library.SingleBin(w.bin), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return w
}

func (w *world) write(p string, content string) string {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) record(_ context.Context, _ Manifest, it Item) error {
	w.recorded = append(w.recorded, it)
	return nil
}

func (w *world) stagingJobs() []string {
	entries, _ := os.ReadDir(Root(w.root))
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// snapshot is what must not change about a seeding file.
type snapshot struct {
	content []byte
	mtime   time.Time
	links   uint64
}

func snap(t *testing.T, p string) snapshot {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	return snapshot{b, fi.ModTime(), linkCount(fi)}
}

func (s snapshot) same(t *testing.T, p string) {
	t.Helper()
	now := snap(t, p)
	if !bytes.Equal(now.content, s.content) {
		t.Errorf("%s: content changed", p)
	}
	if !now.mtime.Equal(s.mtime) {
		t.Errorf("%s: mtime changed %v -> %v", p, s.mtime, now.mtime)
	}
	if now.links != s.links {
		t.Errorf("%s: link count %d -> %d", p, s.links, now.links)
	}
}

var houseTags = audiotag.Tags{audiotag.KeyTitle: {"One"}, audiotag.KeyTrackNumber: {"1"}, audiotag.KeyArtists: {"A", "B"}}

// The whole import of one file: copy, tag, check, one move; the seeding source (a
// torrent's file, here with a second link as a client's hardlinked copy would have) is
// byte-identical with the same link count and mtime afterwards.
func TestImportStagesCopiesAndPlaces(t *testing.T) {
	w := newWorld(t)
	src := w.write(filepath.Join(w.dl, "Album", "01 - One.flac"), "AUDIO-ONE")
	if err := os.Link(src, filepath.Join(w.dl, "seed-link.flac")); err != nil {
		t.Skip("no hard links here:", err)
	}
	before := snap(t, src)
	final := filepath.Join(w.root, "Artist", "Album (2001)", "01 - One.flac")
	j, err := Start(w.root, Manifest{Kind: KindImport, AlbumID: 7, Items: []Item{{Source: src, Final: final, Ref: 11}}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.CheckSpace(); err != nil {
		t.Fatal(err)
	}
	if err := j.Stage(0); err != nil {
		t.Fatal(err)
	}
	cover := &audiotag.Picture{Data: []byte{0xFF, 0xD8, 0xFF, 1, 2}}
	if err := j.Tag(context.Background(), 0, houseTags, cover); err != nil {
		t.Fatal(err)
	}
	before.same(t, src) // tagging touched only the copy
	if err := j.Place(0); err != nil {
		t.Fatal(err)
	}
	if err := j.Recorded(0); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	before.same(t, src)
	got, err := (&fakeTagger{}).Read(final)
	if err != nil || got.Get(audiotag.KeyTitle) != "One" || len(got[audiotag.KeyArtists]) != 2 {
		t.Errorf("placed file's tags: %v %v", got, err)
	}
	if c, _ := (&fakeTagger{}).ReadFrontCover(final); !bytes.Equal(c, cover.Data) {
		t.Error("placed file has no cover")
	}
	if fi, _ := os.Stat(final); linkCount(fi) != 1 {
		t.Error("the library file shares its bytes with something")
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("staging left %v", jobs)
	}
	if _, err := os.Stat(filepath.Join(Root(w.root), ".plexignore")); err != nil {
		t.Error("the staging folder has no .plexignore")
	}
}

// A copy whose tags don't read back as written is deleted; the source and the library are
// untouched, and the error says why (the album goes to Reviews).
func TestVerifyFailureLeavesEverythingAlone(t *testing.T) {
	for name, deps := range map[string]func(*Deps){
		"tags differ":   func(d *Deps) { d.Tagger = &fakeTagger{corrupt: true} },
		"audio changed": func(d *Deps) { d.AudioHash = func(_ context.Context, p string) (string, error) { return p, nil } },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			deps(&w.deps)
			src := w.write(filepath.Join(w.dl, "a.flac"), "AUDIO")
			before := snap(t, src)
			final := filepath.Join(w.root, "A", "a.flac")
			j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final}}}, w.deps)
			if err := j.Stage(0); err != nil {
				t.Fatal(err)
			}
			err := j.Tag(context.Background(), 0, houseTags, nil)
			if !errors.Is(err, ErrVerify) {
				t.Fatalf("Tag: %v, want ErrVerify", err)
			}
			it := j.Items()[0]
			if it.State != Discarded || it.Note == "" {
				t.Errorf("item %+v", it)
			}
			if _, err := os.Stat(it.Staged); !os.IsNotExist(err) {
				t.Error("the failed copy is still in staging")
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			before.same(t, src)
			if _, err := os.Stat(final); !os.IsNotExist(err) {
				t.Error("something reached the library")
			}
		})
	}
}

func TestUnsupportedFormatComesBack(t *testing.T) {
	w := newWorld(t)
	w.deps.Tagger = &fakeTagger{fail: audiotag.ErrUnsupported}
	src := w.write(filepath.Join(w.dl, "a.wav"), "WAV")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "a.wav")}}}, w.deps)
	_ = j.Stage(0)
	if err := j.Tag(context.Background(), 0, houseTags, nil); !errors.Is(err, audiotag.ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

// A better copy replaces the library file: the old one is in the bin first. With no bin
// to take it, nothing moves.
func TestPlaceReplacesThroughTheBin(t *testing.T) {
	w := newWorld(t)
	final := w.write(filepath.Join(w.root, "A", "01.flac"), "OLD")
	src := w.write(filepath.Join(w.dl, "01.flac"), "NEW")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final, Replace: final}}}, w.deps)
	_ = j.Stage(0)
	if err := j.Tag(context.Background(), 0, houseTags, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Place(0); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(final)
	if !strings.HasSuffix(string(b), "NEW") {
		t.Errorf("final holds %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(w.bin, "A", "01.flac")); err != nil || string(b) != "OLD" {
		t.Errorf("old file in the bin: %q %v", b, err)
	}
}

func TestPlaceRefusesWithoutABin(t *testing.T) {
	w := newWorld(t)
	w.deps.Bin = library.SingleBin("") // switched off
	final := w.write(filepath.Join(w.root, "A", "01.flac"), "OLD")
	src := w.write(filepath.Join(w.dl, "01.flac"), "NEW")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final, Replace: final}}}, w.deps)
	_ = j.Stage(0)
	_ = j.Tag(context.Background(), 0, houseTags, nil)
	if err := j.Place(0); !errors.Is(err, ErrReplaceRefused) {
		t.Fatalf("Place: %v, want ErrReplaceRefused", err)
	}
	if b, _ := os.ReadFile(final); string(b) != "OLD" {
		t.Error("the library file changed")
	}
	if err := j.Abandon("refused"); err != nil {
		t.Fatal(err)
	}
	if len(w.stagingJobs()) != 0 {
		t.Error("the abandoned job is still there")
	}
}

func TestPlaceNeverLandsOnAnUnexpectedFile(t *testing.T) {
	w := newWorld(t)
	final := w.write(filepath.Join(w.root, "A", "01.flac"), "SOMEONE ELSE'S")
	src := w.write(filepath.Join(w.dl, "01.flac"), "NEW")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final}}}, w.deps)
	_ = j.Stage(0)
	_ = j.Tag(context.Background(), 0, houseTags, nil)
	if err := j.Place(0); !errors.Is(err, ErrTargetTaken) {
		t.Fatalf("Place: %v, want ErrTargetTaken", err)
	}
	if b, _ := os.ReadFile(final); string(b) != "SOMEONE ELSE'S" {
		t.Error("the file at the target changed")
	}
}

func TestRetireSendsTheOldNameToTheBin(t *testing.T) {
	w := newWorld(t)
	old := w.write(filepath.Join(w.root, "A", "01 - One.mp3"), "MP3")
	src := w.write(filepath.Join(w.dl, "01.flac"), "FLAC")
	final := filepath.Join(w.root, "A", "01 - One.flac")
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: final, Retire: old}}}, w.deps)
	_ = j.Stage(0)
	_ = j.Tag(context.Background(), 0, houseTags, nil)
	if err := j.Place(0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old .mp3 is still beside the new file")
	}
	if _, err := os.Stat(filepath.Join(w.bin, "A", "01 - One.mp3")); err != nil {
		t.Error("the old .mp3 isn't in the bin")
	}
}

func TestCheckSpace(t *testing.T) {
	w := newWorld(t)
	src := w.write(filepath.Join(w.dl, "a.flac"), strings.Repeat("x", 1000))
	w.deps.FreeBytes = func(string) (uint64, bool) { return 10 << 20, true }
	j, _ := Start(w.root, Manifest{Kind: KindImport, Items: []Item{{Source: src, Final: filepath.Join(w.root, "a.flac")}}}, w.deps)
	if err := j.CheckSpace(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("CheckSpace with 10 MB free: %v", err)
	}
	w.deps.FreeBytes = func(string) (uint64, bool) { return 1 << 30, true }
	j.d = w.deps
	if err := j.CheckSpace(); err != nil {
		t.Fatal(err)
	}
	_ = j.Abandon("test")
}

// crashAfter runs the import steps for one file up to and including step, then walks
// away as a crash would (no Close).
func crashAfter(t *testing.T, w *world, step string, replace bool) (src, final string, j *Job) {
	t.Helper()
	src = w.write(filepath.Join(w.dl, "01.flac"), "NEW")
	final = filepath.Join(w.root, "A", "01.flac")
	it := Item{Source: src, Final: final, Ref: 5, Meta: json.RawMessage(`{"q":"FLAC"}`)}
	if replace {
		w.write(final, "OLD")
		it.Replace = final
	}
	j, err := Start(w.root, Manifest{Kind: KindImport, AlbumID: 3, Items: []Item{it}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if step == "begin" {
		return
	}
	if err := j.Stage(0); err != nil {
		t.Fatal(err)
	}
	if step == "stage" {
		return
	}
	if err := j.Tag(context.Background(), 0, houseTags, nil); err != nil {
		t.Fatal(err)
	}
	if step == "tag" {
		return
	}
	if step == "bin" {
		// Place, crashing between the bin and the final move.
		beforeFinalMove = func() error { return errors.New("crash") }
		defer func() { beforeFinalMove = nil }()
		if err := j.Place(0); err == nil {
			t.Fatal("the injected crash didn't happen")
		}
		return
	}
	// "place": in the library, not yet recorded.
	if err := j.Place(0); err != nil {
		t.Fatal(err)
	}
	return
}

// Recovery settles every manifest state: copies that never reached the library go, a
// file whose old version is in the bin goes into place, a placed file is recorded. No
// file is lost and nothing is left twice in the library.
func TestRecoverEveryState(t *testing.T) {
	for _, tc := range []struct {
		step     string
		replace  bool
		want     string // the final file's audio after recovery ("" = no file)
		recorded bool
	}{
		{"begin", false, "", false},
		{"stage", false, "", false},
		{"tag", false, "", false},
		{"stage", true, "OLD", false},
		{"tag", true, "OLD", false},
		{"bin", true, "NEW", true},
		{"place", true, "NEW", true},
		{"place", false, "NEW", true},
	} {
		t.Run(tc.step+map[bool]string{true: "+replace"}[tc.replace], func(t *testing.T) {
			w := newWorld(t)
			src, final, _ := crashAfter(t, w, tc.step, tc.replace)
			before := snap(t, src)
			rep, err := Recover(context.Background(), w.root, w.deps, w.record)
			if err != nil {
				t.Fatalf("Recover: %v (%+v)", err, rep)
			}
			before.same(t, src)
			b, err := os.ReadFile(final)
			switch {
			case tc.want == "" && err == nil:
				t.Errorf("a file reached the library: %q", b)
			case tc.want != "" && (err != nil || !strings.HasSuffix(string(b), tc.want)):
				t.Errorf("final = %q (%v), want %q", b, err, tc.want)
			}
			if got := len(w.recorded) == 1; got != tc.recorded {
				t.Errorf("recorded = %v, want %v", w.recorded, tc.recorded)
			}
			if tc.recorded {
				var meta bytes.Buffer
				_ = json.Compact(&meta, w.recorded[0].Meta)
				if w.recorded[0].Ref != 5 || meta.String() != `{"q":"FLAC"}` {
					t.Errorf("recorded item %+v", w.recorded[0])
				}
			}
			if jobs := w.stagingJobs(); len(jobs) != 0 {
				t.Errorf("staging left %v", jobs)
			}
			// Nothing left beside the final file but the file itself.
			entries, _ := os.ReadDir(filepath.Dir(final))
			if len(entries) > 1 {
				t.Errorf("album folder holds %d entries", len(entries))
			}
		})
	}
}

// A binned file whose target has been taken meanwhile can't go in: the old file can't go
// back either, so the job is kept with every file where it is — nothing deleted.
func TestRecoverKeepsWhatItCantSettle(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "bin", true)
	w.write(final, "INTRUDER")
	staged := j.Items()[0].Staged
	binned := j.Items()[0].Binned
	rep, err := Recover(context.Background(), w.root, w.deps, w.record)
	if err == nil || rep.Kept != 1 {
		t.Fatalf("Recover: %v %+v", err, rep)
	}
	for _, p := range []string{staged, binned, final} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is gone: %v", p, err)
		}
	}
}

// A staged file whose copy vanished (a person cleaned up) while the old file is in the
// bin: the old file goes back where it was.
func TestRecoverPutsTheOldFileBack(t *testing.T) {
	w := newWorld(t)
	_, final, j := crashAfter(t, w, "bin", true)
	// Both of the new file's copies are gone (a person cleaned up).
	if err := os.Remove(j.Items()[0].Staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(j.Items()[0].Temp); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(final); string(b) != "OLD" {
		t.Errorf("final = %q, want the old file back", b)
	}
}

func TestRecoverRename(t *testing.T) {
	w := newWorld(t)
	a := w.write(filepath.Join(w.root, "Old", "01.flac"), "A")
	b := w.write(filepath.Join(w.root, "Old", "02.flac"), "B")
	c := w.write(filepath.Join(w.root, "Old", "03.flac"), "C")
	aTo, bTo, cTo := filepath.Join(w.root, "New", "01.flac"), filepath.Join(w.root, "New", "02.flac"), filepath.Join(w.root, "New", "03.flac")
	_, err := Start(w.root, Manifest{Kind: KindRename, Items: []Item{
		{Source: a, Final: aTo, Ref: 1}, {Source: b, Final: bTo, Ref: 2}, {Source: c, Final: cTo, Ref: 3},
	}}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	// a: moved fully; b: linked but the old name not yet removed; c: not moved.
	_ = os.MkdirAll(filepath.Dir(aTo), 0o755)
	if err := os.Rename(a, aTo); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(b, bTo); err != nil {
		t.Skip("no hard links:", err)
	}
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{aTo: true, bTo: true, b: false, c: true, cTo: false} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", p, err == nil, want)
		}
	}
	var refs []int64
	for _, it := range w.recorded {
		refs = append(refs, it.Ref)
	}
	if len(refs) != 2 || refs[0] != 1 || refs[1] != 2 {
		t.Errorf("recorded %v, want 1 and 2", refs)
	}
}

// Recover only ever touches folders named like its jobs.
func TestRecoverLeavesOtherFoldersAlone(t *testing.T) {
	w := newWorld(t)
	other := w.write(filepath.Join(Root(w.root), "not-a-job", "keep.flac"), "X")
	if _, err := Recover(context.Background(), w.root, w.deps, w.record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("Recover deleted a folder that isn't its job")
	}
}

func TestMoveNoClobberRename(t *testing.T) {
	w := newWorld(t)
	a := w.write(filepath.Join(w.root, "a"), "A")
	bb := w.write(filepath.Join(w.root, "b"), "B")
	if err := MoveNoClobber(a, bb); !errors.Is(err, ErrTargetTaken) {
		t.Fatalf("got %v", err)
	}
	if err := MoveNoClobber(a, filepath.Join(w.root, "sub", "c")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Error("source still there")
	}
}

func TestPlaceCoverKeepsAnExistingOne(t *testing.T) {
	w := newWorld(t)
	album := filepath.Join(w.root, "A", "Album")
	j, err := Start(w.root, Manifest{Kind: KindImport}, w.deps)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := j.PlaceCover(album, []byte("JPEG1")); !ok || err != nil {
		t.Fatalf("first cover: %v %v", ok, err)
	}
	if ok, err := j.PlaceCover(album, []byte("JPEG2")); ok || err != nil {
		t.Fatalf("second cover: %v %v", ok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(album, CoverName)); string(b) != "JPEG1" {
		t.Errorf("cover.jpg = %q", b)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	_, _ = Recover(context.Background(), w.root, w.deps, w.record)
}
