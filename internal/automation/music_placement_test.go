package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// houseFake stands in for audiotag on byte blobs: a file's "tags" are a JSON header line
// before its "audio". Like audiotag it writes a new file and renames it over the old one.
type houseFake struct{ corrupt bool }

const houseMagic = "HOUSETAGS:"

type houseHeader struct {
	Tags  audiotag.Tags
	Cover []byte
}

func splitHouse(b []byte) (houseHeader, []byte) {
	if !bytes.HasPrefix(b, []byte(houseMagic)) {
		return houseHeader{}, b
	}
	line, rest, _ := bytes.Cut(b, []byte("\n"))
	var h houseHeader
	_ = json.Unmarshal(line[len(houseMagic):], &h)
	return h, rest
}

func (f *houseFake) Read(p string) (audiotag.Tags, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	h, _ := splitHouse(b)
	return h.Tags, nil
}

func (f *houseFake) ReadFrontCover(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	h, _ := splitHouse(b)
	if len(h.Cover) == 0 {
		return nil, audiotag.ErrNoCover
	}
	return h.Cover, nil
}

func (f *houseFake) Write(p string, t audiotag.Tags, opts audiotag.WriteOptions) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	h, audio := splitHouse(b)
	h.Tags = t
	if f.corrupt {
		h.Tags = audiotag.Tags{audiotag.KeyTitle: {"garbled"}}
	}
	if opts.FrontCover != nil {
		h.Cover = opts.FrontCover.Data
	}
	hdr, _ := json.Marshal(h)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(append([]byte(houseMagic), hdr...), append([]byte("\n"), audio...)...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func houseHash(_ context.Context, p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	_, audio := splitHouse(b)
	s := sha256.Sum256(audio)
	return hex.EncodeToString(s[:]), nil
}

// fakeCovers serves a "JPEG" per size.
type fakeCovers struct{ calls int }

func (f *fakeCovers) Front(_ context.Context, _, _ string, size int) ([]byte, error) {
	f.calls++
	return append([]byte{0xFF, 0xD8, 0xFF}, []byte(strings.Repeat("c", size/100))...), nil
}

// withHouseTags turns house tags on for a harness, with covers.
func withHouseTags(h *musicHarness, tagger stage.Tagger) *fakeCovers {
	covers := &fakeCovers{}
	h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
		return MusicPlacementPrefs{WriteTags: true}
	}, covers, houseHash)
	h.c.musicPlace.tagger = tagger
	return covers
}

// download writes a two-track download with extras, one track also hardlinked elsewhere
// as a seeding client's copy would be.
func houseDownload(t *testing.T, release string) (dir string, seeding string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), release)
	blobTrack(t, dir, "01 - One.flac", 1)
	blobTrack(t, dir, "02 - Two.flac", 2)
	for name, body := range map[string]string{"booklet.pdf": "PDF", "01 - One.lrc": "[00:01]la", "cover.jpg": "DLCOVER", "rip.log": "EAC", "Scans/back.jpg": "SCAN", "x.exe": "NO"} {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seeding = filepath.Join(dir, "01 - One.flac")
	if err := os.Link(seeding, filepath.Join(t.TempDir(), "client-copy.flac")); err != nil {
		t.Skip("no hard links:", err)
	}
	return dir, seeding
}

type fileState struct {
	content []byte
	mtime   time.Time
	links   uint64
}

func stateOf(t *testing.T, p string) fileState {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	return fileState{b, fi.ModTime(), nlink(fi)}
}

// MUS-33: with house tags on, the files land tagged in the house style from a staging
// copy; the seeding original is byte-identical, with the same link count and mtime; the
// cover is embedded and cover.jpg written; staging is left empty.
func TestImportWritesHouseTagsFromStaging(t *testing.T) {
	h, _ := musicImportHarness(t)
	covers := withHouseTags(h, &houseFake{})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if err := h.svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}
	dl, seeding := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
	before := stateOf(t, seeding)
	time.Sleep(10 * time.Millisecond)

	res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "Radiohead - OK Computer (1997) [FLAC]")
	if res.placed != 2 || res.tagFailed != 0 || res.failed != 0 {
		t.Fatalf("import = %+v", res)
	}
	if now := stateOf(t, seeding); !bytes.Equal(now.content, before.content) || !now.mtime.Equal(before.mtime) || now.links != before.links {
		t.Errorf("the seeding file changed: links %d -> %d, mtime %v -> %v", before.links, now.links, before.mtime, now.mtime)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if !tr.HasFile || tr.TagState != music.TagStateHouse {
			t.Errorf("track %q: has %v, tag state %q", tr.Title, tr.HasFile, tr.TagState)
			continue
		}
		got, _ := (&houseFake{}).Read(tr.FilePath)
		if got.Get(audiotag.KeyTitle) != tr.Title || got.Get(audiotag.KeyAlbum) != "OK Computer" || got.Get(audiotag.KeyDiscNumber) != "1" {
			t.Errorf("track %q tags: %v", tr.Title, got)
		}
		if c, _ := (&houseFake{}).ReadFrontCover(tr.FilePath); len(c) == 0 {
			t.Errorf("track %q has no embedded cover", tr.Title)
		}
		fi, _ := os.Stat(tr.FilePath)
		if nlink(fi) != 1 {
			t.Errorf("track %q shares its bytes with the download", tr.Title)
		}
	}
	albumDir := filepath.Dir(tracks[0].FilePath)
	if b, err := os.ReadFile(filepath.Join(albumDir, "cover.jpg")); err != nil || !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) || len(b) != 15 {
		t.Errorf("cover.jpg = %d bytes, %v (want the 1200 px front)", len(b), err)
	}
	if covers.calls == 0 {
		t.Error("no cover was fetched")
	}
	var found string
	_ = h.st.DB().QueryRowContext(h.ctx, `SELECT tags_json FROM music_track_files WHERE path = ?`, tracks[0].FilePath).Scan(&found)
	if found != "" {
		t.Errorf("a blob had no tags, but tags_json = %q", found)
	}
	if entries, _ := os.ReadDir(stage.Root(h.c.imp.MusicDir())); len(entries) != 2 { // .plexignore and .ignore only
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staging holds %v after the import", names)
	}
}

// With "Write tags" off, files are hardlinked straight in, exactly as before.
func TestImportKeepsFilesAsDownloaded(t *testing.T) {
	h, _ := musicImportHarness(t)
	h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs { return MusicPlacementPrefs{} }, &fakeCovers{}, houseHash)
	h.c.musicPlace.tagger = &houseFake{}
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	dl, seeding := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
	before := stateOf(t, seeding)
	res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "Radiohead - OK Computer (1997) [FLAC]")
	if res.placed != 2 {
		t.Fatalf("import = %+v", res)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	si, _ := os.Stat(seeding)
	di, _ := os.Stat(tracks[0].FilePath)
	if !os.SameFile(si, di) {
		t.Error("tags off should hardlink the file in as it is")
	}
	if tracks[0].TagState != music.TagStateAsFound {
		t.Errorf("tag state %q", tracks[0].TagState)
	}
	if now := stateOf(t, seeding); !bytes.Equal(now.content, before.content) || now.links != before.links+1 {
		t.Errorf("hardlink import: links %d -> %d", before.links, now.links)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(tracks[0].FilePath), "cover.jpg")); err == nil {
		t.Error("cover.jpg written with tags off")
	}
}

// A tag write that doesn't check out leaves the download untouched, places nothing and
// sends the download to Review with the reason.
func TestImportTagFailureGoesToReview(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTags(h, &houseFake{corrupt: true})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	dl, seeding := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
	before := stateOf(t, seeding)
	res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "Radiohead - OK Computer (1997) [FLAC]")
	if res.tagFailed != 2 || res.placed != 0 || !strings.Contains(res.tagWhy, "didn't check out") {
		t.Fatalf("import = %+v", res)
	}
	if now := stateOf(t, seeding); !bytes.Equal(now.content, before.content) || now.links != before.links {
		t.Error("the download changed")
	}
	if files := albumFiles(t, h.c.imp.MusicDir()); len(files) > 2 { // the staging markers only
		t.Errorf("library holds %v", files)
	}
	it := download.Item{Hash: "taghash", Name: "Radiohead - OK Computer (1997) [FLAC]", ContentPath: dl}
	h.c.musicTagReview(h.ctx, it, res, al.ID, al.Title)
	if !h.c.hasReview(h.ctx, it.Hash) {
		t.Error("no review for a failed tag write")
	}
}

// A better copy replaces a held one through the staging job: the old file is in the bin.
func TestStagedUpgradeBinsTheOldFile(t *testing.T) {
	h, bin := musicImportHarness(t)
	withHouseTags(h, &houseFake{})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [MP3 320]", ".mp3", 1); res.placed != 2 {
		t.Fatalf("first import %+v", res)
	}
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 2); res.placed != 2 {
		t.Fatalf("upgrade %+v", res)
	}
	binned := strings.Join(albumFiles(t, bin), ",")
	if !strings.Contains(binned, "01 - One.mp3") || !strings.Contains(binned, "02 - Two.mp3") {
		t.Errorf("bin holds %v, want the two replaced MP3s", binned)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if filepath.Ext(tr.FilePath) != ".flac" || tr.TagState != music.TagStateHouse {
			t.Errorf("track %q: %s %s", tr.Title, tr.FilePath, tr.TagState)
		}
	}
}

// A crash between placing a file and recording it is finished on the next start: the
// file is recorded against its track.
func TestRecoverMusicStagingRecordsPlacedFiles(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTags(h, &houseFake{})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	src := blobTrack(t, t.TempDir(), "01 - One.flac", 3)
	final := filepath.Join(h.c.imp.MusicDir(), "Radiohead", "OK Computer", "01 - One.flac")
	meta, _ := json.Marshal(placedMeta{Quality: music.QualityFLAC, TagState: music.TagStateHouse, SourceRelease: "rel"})
	job, err := stage.Start(h.c.imp.MusicDir(), stage.Manifest{Kind: stage.KindImport, AlbumID: al.ID,
		Items: []stage.Item{{Source: src, Final: final, Ref: tracks[0].ID, Meta: meta}}}, h.c.stageDeps())
	if err != nil {
		t.Fatal(err)
	}
	if err := job.Stage(0); err != nil {
		t.Fatal(err)
	}
	if err := job.Tag(h.ctx, 0, audiotag.Tags{audiotag.KeyTitle: {"One"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := job.Place(0); err != nil {
		t.Fatal(err)
	}
	// ...and the process stops here.
	rep, err := h.c.RecoverMusicStaging(h.ctx)
	if err != nil || rep.Finished != 1 {
		t.Fatalf("recover: %+v %v", rep, err)
	}
	tracks, _ = h.svc.Tracks(h.ctx, al.ID)
	if !tracks[0].HasFile || tracks[0].FilePath != final || tracks[0].TagState != music.TagStateHouse {
		t.Errorf("track after recovery: %+v", tracks[0])
	}
}

// MUS-33 step 7: a retag stages a tagged copy and swaps it in; the original goes to the bin.
func TestRetagAlbumSwapsThroughTheBin(t *testing.T) {
	h, bin := musicImportHarness(t)
	h.c.musicPlace.tagger = &houseFake{}
	h.c.musicPlace.audioHash = houseHash
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1); res.placed != 2 {
		t.Fatalf("import %+v", res)
	}
	res, err := h.c.RetagAlbum(h.ctx, al.ID)
	if err != nil || res.Retagged != 2 || res.Failed != 0 {
		t.Fatalf("retag: %+v %v", res, err)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		got, _ := (&houseFake{}).Read(tr.FilePath)
		if got.Get(audiotag.KeyTitle) != tr.Title || tr.TagState != music.TagStateHouse {
			t.Errorf("track %q: tags %v, state %s", tr.Title, got, tr.TagState)
		}
	}
	if binned := albumFiles(t, bin); len(binned) < 2 {
		t.Errorf("the originals aren't in the bin: %v", binned)
	}
}

// A format the tag library doesn't write (WAV) goes in as downloaded, hardlinked, even
// with house tags on; nothing is staged for it.
func TestUntaggableFormatGoesInAsDownloaded(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTags(h, &houseFake{})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [WAV]", ".wav", 1); res.placed != 2 || res.tagFailed != 0 {
		t.Fatalf("import %+v", res)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.TagState != music.TagStateAsFound {
			t.Errorf("track %q: tag state %q", tr.Title, tr.TagState)
		}
		if got, _ := (&houseFake{}).Read(tr.FilePath); len(got) != 0 {
			t.Errorf("track %q was written: %v", tr.Title, got)
		}
	}
}
