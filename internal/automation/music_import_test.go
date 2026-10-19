package automation

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
)

// blobTrack writes a stand-in "audio" file: a 300 KB byte blob (findAudioFiles skips
// anything under 256 KB as artwork), with a fill byte so two downloads differ.
func blobTrack(t *testing.T, dir, name string, fill byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	b := make([]byte, 300<<10)
	for i := range b {
		b[i] = fill
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// musicImportHarness is musicTestCoord with a real music folder and a recycle bin.
func musicImportHarness(t *testing.T) (*musicHarness, string) {
	t.Helper()
	h := musicTestCoord(t)
	bin := t.TempDir()
	h.c.imp.SetRecycleDir(bin)
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return h, bin
}

// importDownload imports one fake download (two tracks named for the two-track listing
// addArtist gives every album) into the album, with the given extension and release name.
func (h *musicHarness) importDownload(t *testing.T, a music.Artist, al music.Album, release, ext string, fill byte) albumImport {
	t.Helper()
	dl := filepath.Join(t.TempDir(), release)
	blobTrack(t, dl, "01 - One"+ext, fill)
	blobTrack(t, dl, "02 - Two"+ext, fill)
	fresh := h.album(t, a.ID, al.Title)
	if err := h.svc.EnsureTracks(h.ctx, fresh); err != nil { // as grabAlbum does before a grab
		t.Fatal(err)
	}
	return h.c.importAlbumContent(h.ctx, a, fresh, dl, release)
}

func albumFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, filepath.Base(p))
		}
		return nil
	})
	return out
}

// MUS-08: a held FLAC is never replaced by a lower tier — not on disk, not in the DB.
func TestReimportLowerTierKeepsHeld(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1); res.placed != 2 {
		t.Fatalf("first import placed %d, want 2", res.placed)
	}
	res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [MP3 320]", ".mp3", 2)
	if res.placed != 0 || res.kept != 2 {
		t.Fatalf("an MP3-320 over FLAC: placed %d kept %d, want 0 and 2", res.placed, res.kept)
	}
	files := albumFiles(t, h.c.imp.MusicDir())
	for _, f := range files {
		if strings.HasSuffix(f, ".mp3") {
			t.Errorf("an MP3 landed next to the FLACs: %v", files)
		}
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityFLAC) || !strings.HasSuffix(tr.FilePath, ".flac") {
			t.Errorf("track %q now %q at %s, want the FLAC kept", tr.Title, tr.Format, tr.FilePath)
		}
	}
}

// MUS-08: a better copy with another extension replaces the held one, and the old file goes
// to the recycle bin — an album folder never holds "01 - One.flac" and "01 - One.mp3".
func TestBetterTierReplacesAndRecycles(t *testing.T) {
	h, bin := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [MP3 320]", ".mp3", 1)

	res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 2)
	if res.placed != 2 {
		t.Fatalf("FLAC over MP3: placed %d, want 2 (%+v)", res.placed, res)
	}
	for _, f := range albumFiles(t, h.c.imp.MusicDir()) {
		if strings.HasSuffix(f, ".mp3") {
			t.Errorf("the replaced MP3 is still in the album folder: %s", f)
		}
	}
	recycled := 0
	for _, f := range albumFiles(t, bin) {
		if strings.HasSuffix(f, ".mp3") {
			recycled++
		}
	}
	if recycled != 2 {
		t.Errorf("recycled MP3s = %d, want 2 (bin holds %v)", recycled, albumFiles(t, bin))
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityFLAC) || !strings.HasSuffix(tr.FilePath, ".flac") {
			t.Errorf("track %q is %q at %s, want the FLAC", tr.Title, tr.Format, tr.FilePath)
		}
	}
}

// With no recycle bin the better copy is refused, and nothing held is touched: never a
// hard delete.
func TestBetterTierWithoutBinKeepsOld(t *testing.T) {
	h := musicTestCoord(t)
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [MP3 320]", ".mp3", 1)

	res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 2)
	if res.placed != 0 || res.refused != 2 {
		t.Fatalf("without a bin: placed %d refused %d, want 0 and 2", res.placed, res.refused)
	}
	files := albumFiles(t, h.c.imp.MusicDir())
	mp3s, flacs := 0, 0
	for _, f := range files {
		switch {
		case strings.HasSuffix(f, ".mp3"):
			mp3s++
		case strings.HasSuffix(f, ".flac"):
			flacs++
		}
	}
	if mp3s != 2 || flacs != 0 {
		t.Errorf("album folder holds %v, want the two MP3s only", files)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	for _, tr := range tracks {
		if tr.Format != string(music.QualityMP3320) {
			t.Errorf("track %q is %q, want MP3-320 unchanged", tr.Title, tr.Format)
		}
	}
}

// PlaceAlbumTrack refuses (errors.Is ErrReplacementRefused) rather than delete.
func TestPlaceAlbumTrackRefusesWithoutBin(t *testing.T) {
	root := t.TempDir()
	im := library.NewImporter(root, slog.Default())
	src := blobTrack(t, t.TempDir(), "new.flac", 1)
	old := blobTrack(t, filepath.Join(im.MusicDir(), "A", "B"), "01 - X.mp3", 2)
	err := im.PlaceAlbumTrack(src, filepath.Join(im.MusicDir(), "A", "B", "01 - X.flac"), old)
	if !errors.Is(err, library.ErrReplacementRefused) {
		t.Fatalf("err = %v, want ErrReplacementRefused", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the old file must still be there: %v", err)
	}
}

// MUS-08: a partly held album only takes releases at least as good as what it has.
func TestAlbumSearchDropsBelowHeldTier(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	// One of the two tracks held, as FLAC.
	dl := filepath.Join(t.TempDir(), "partial")
	blobTrack(t, dl, "01 - One.flac", 1)
	if err := h.svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}
	if res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, al.Title), dl, "Radiohead - OK Computer [FLAC]"); res.placed != 1 {
		t.Fatalf("partial seed placed %d", res.placed)
	}
	h.releases = found(rel("Radiohead - OK Computer (1997) [MP3 320]"))
	out := h.c.grabAlbum(h.ctx, a, h.album(t, a.ID, al.Title))
	if out.Code != outcomeBelowProfile || !strings.Contains(out.Detail, "below the FLAC you already have") {
		t.Fatalf("outcome = %+v, want below_profile naming the held FLAC", out)
	}
	h.releases = found(rel("Radiohead - OK Computer (1997) [MP3 320]"), rel("Radiohead - OK Computer (1997) [ALAC]"))
	if out := h.c.grabAlbum(h.ctx, a, h.album(t, a.ID, al.Title)); out.Code != outcomeGrabbed || !strings.Contains(out.Release, "ALAC") {
		t.Errorf("outcome = %+v, want the lossless release grabbed", out)
	}
}

// MUS-08 end to end: a partial album's download is marked imported (so its seed rule runs),
// the album backs off, and its history names what's missing.
func TestPartialImportMarksGrabImported(t *testing.T) {
	h := newStallHarness(t)
	fake := &fakeMusicProvider{
		artists: map[string]metadata.ArtistDetails{"a1": {ArtistResult: metadata.ArtistResult{MBID: "a1", Name: "Radiohead"}}},
		albums:  map[string][]metadata.AlbumResult{"a1": {{MBID: "rg1", Title: "OK Computer", Type: "Album", ReleaseDate: "1997-05-21"}}},
		tracks: map[string][]metadata.TrackResult{"rg1": {
			{Title: "One", Disc: 1, Number: 1}, {Title: "Two", Disc: 1, Number: 2}, {Title: "Three", Disc: 1, Number: 3},
		}},
	}
	svc := music.NewService(h.c.db, fake, h.c.log)
	h.c.music = svc
	h.c.imp = library.NewImporter(t.TempDir(), h.c.log)
	h.c.imp.SetRecycleDir(t.TempDir())
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := svc.AddArtist(h.ctx, "a1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	al := a.Albums[0]
	if err := svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}

	const release = "Radiohead - OK Computer (1997) [FLAC]"
	dl := filepath.Join(t.TempDir(), release)
	blobTrack(t, dl, "01 - One.flac", 1)
	blobTrack(t, dl, "02 - Two.flac", 1)
	h.c.recordMusicGrab(h.ctx, "music", al.ID, release, "Fake", "", hashFor(release))
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(release), "name": release, "state": "uploading", "progress": 1.0,
		"size": 600 << 10, "amount_left": 0, "category": download.CategoryMusic, "content_path": dl,
	})
	h.qbit.mu.Unlock()

	h.c.ImportMusicDownloads(h.ctx)

	var status string
	if err := h.c.db.QueryRow(`SELECT status FROM grabs WHERE info_hash = ?`, hashFor(release)).Scan(&status); err != nil || status != grabStatusImported {
		t.Errorf("grab status = %q (err %v), want imported after a partial import", status, err)
	}
	if !h.c.hashAlreadyImported(h.ctx, hashFor(release)) {
		t.Error("the download's hash should be recorded as imported")
	}
	got := h.albumRow(t, svc, a.ID)
	if got.HaveTracks != 2 || got.SearchMisses != 1 {
		evs, _ := svc.Events(h.ctx, a.ID, 20)
		t.Errorf("album have=%d misses=%d, want 2 and 1 (backed off); history %+v", got.HaveTracks, got.SearchMisses, evs)
	}
	if n := artistEvents(t, svc, a.ID, `still missing: "Three"`); n != 1 {
		t.Errorf("history lines naming the missing track = %d, want 1", n)
	}
}

// MUS-08: a music grab whose download was already imported isn't failed over (or
// blocklisted) when its torrent leaves the client: it's imported.
func TestImportedAlbumGrabNotBlockedWhenGone(t *testing.T) {
	h := newStallHarness(t)
	withMusic(t, h)
	mustExec(t, h.c, `INSERT INTO albums (id, artist_id, mbid, title, monitored) VALUES (9, 5, 'rg9', 'OK Computer', 1)`)
	mustExec(t, h.c, `INSERT INTO tracks (album_id, disc_number, track_number, title) VALUES (9, 1, 1, 'Airbag'), (9, 1, 2, 'Paranoid Android')`)
	const release = "Radiohead - OK Computer (1997) [FLAC]"
	gid := h.addGoneGrab(t, "music", 9, release, grabStatusGrabbed)
	mustExec(t, h.c, `INSERT INTO imports (download_hash, source_path, target_path, title) VALUES (?, '', '', ?)`, hashFor(release), release)

	h.goneForTheGrace()
	if s := h.status(t, gid); s != grabStatusImported {
		t.Errorf("status = %q, want imported", s)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want none", n)
	}
	if n := len(h.qbit.callLog()); n != 0 {
		t.Errorf("client calls = %v, want none", h.qbit.callLog())
	}
}

// albumRow reads an artist's single album row.
func (h *stallHarness) albumRow(t *testing.T, svc *music.Service, artistID int64) music.Album {
	t.Helper()
	albums, err := svc.Albums(h.ctx, artistID)
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums: %v %v", albums, err)
	}
	return albums[0]
}
