package automation

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A download split into CD1/CD2 folders gives each file its disc, so "CD2/01 - X" lands on
// disc 2 rather than taking disc 1's first track.
func TestFindAudioFilesDiscFolders(t *testing.T) {
	dl := t.TempDir()
	blobTrack(t, filepath.Join(dl, "CD1"), "01 - Opening.flac", 1)
	blobTrack(t, filepath.Join(dl, "Disc 02"), "01 - Closing.flac", 1)
	blobTrack(t, filepath.Join(dl, "Scans"), "01 - Not a disc.flac", 1)
	discs := map[string]int{}
	for _, f := range findAudioFiles(dl) {
		discs[filepath.Base(f.Path)] = f.Disc
	}
	if discs["01 - Opening.flac"] != 1 || discs["01 - Closing.flac"] != 2 || discs["01 - Not a disc.flac"] != 0 {
		t.Fatalf("discs = %v", discs)
	}
	tracks := []music.Track{
		{ID: 11, DiscNumber: 1, TrackNumber: 1, Title: "Opening"},
		{ID: 21, DiscNumber: 2, TrackNumber: 1, Title: "Closing"},
	}
	// Titles that don't help, so only the disc hint can tell the two "01"s apart.
	files := []music.AudioFile{{Path: "/d/CD1/01 - x.flac", Disc: 1}, {Path: "/d/CD2/01 - y.flac", Disc: 2}}
	m, _ := music.MatchTracks(tracks, files)
	if m[11].Path != "/d/CD1/01 - x.flac" || m[21].Path != "/d/CD2/01 - y.flac" {
		t.Errorf("matched = %v", m)
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
// importRig is a stall harness with Music, a three-track album (One, Two, Three) and a
// finished download in the music category holding the given files.
type importRig struct {
	h       *stallHarness
	svc     *music.Service
	artist  music.Artist
	album   music.Album
	release string
	dl      string
	bin     string
}

func newImportRig(t *testing.T, bin bool, files ...string) *importRig {
	t.Helper()
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
	r := &importRig{h: h, svc: svc, release: "Radiohead - OK Computer (1997) [FLAC]"}
	if bin {
		r.bin = t.TempDir()
		h.c.imp.SetRecycleDir(r.bin)
	}
	if err := os.MkdirAll(h.c.imp.MusicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := svc.AddArtist(h.ctx, "a1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r.artist, r.album = a, a.Albums[0]
	if err := svc.EnsureTracks(h.ctx, r.album); err != nil {
		t.Fatal(err)
	}
	r.dl = filepath.Join(t.TempDir(), r.release)
	for _, f := range files {
		blobTrack(t, r.dl, f, 1)
	}
	h.c.recordMusicGrab(h.ctx, "music", r.album.ID, r.release, "Fake", "", hashFor(r.release))
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(r.release), "name": r.release, "state": "uploading", "progress": 1.0,
		"size": 600 << 10, "amount_left": 0, "category": download.CategoryMusic, "content_path": r.dl,
	})
	h.qbit.mu.Unlock()
	return r
}

func (r *importRig) grabStatus(t *testing.T) string {
	t.Helper()
	var status string
	if err := r.h.c.db.QueryRow(`SELECT status FROM grabs WHERE info_hash = ?`, hashFor(r.release)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// A download whose import left files that matched no track is closed so seed cleanup
// keeps its files ('dismissed'): those files were left for a person, and 'imported' lets
// the torrent go with its data at its seed goal.
func TestPartialImportWithLeftoversKeepsFiles(t *testing.T) {
	r := newImportRig(t, true, "01 - One.flac", "02 - Two.flac", "bonus - Untitled Jam.flac")
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if s := r.grabStatus(t); s != grabStatusDismissed {
		t.Fatalf("grab status = %q, want dismissed (files kept at the seed goal)", s)
	}
	if !r.h.c.hashAlreadyImported(r.h.ctx, hashFor(r.release)) {
		t.Error("what did land is still recorded as imported")
	}
	if _, err := os.Stat(filepath.Join(r.dl, "bonus - Untitled Jam.flac")); err != nil {
		t.Errorf("the unmatched file must still be in the download: %v", err)
	}
	if n := artistEvents(t, r.svc, r.artist.ID, "left in the download for you"); n != 1 {
		t.Errorf("history lines about the leftovers = %d, want 1", n)
	}
}

// A better copy the recycle bin can't make room for isn't retried every 30 s: it backs off,
// and after a few tries goes to Review. Nothing held is touched meanwhile.
func TestBinRefusalBacksOffThenReviews(t *testing.T) {
	r := newImportRig(t, false, "01 - One.flac", "02 - Two.flac")
	// The album already holds MP3s, so these FLACs would replace them — and there's no bin.
	tracks, _ := r.svc.Tracks(r.h.ctx, r.album.ID)
	for _, tr := range tracks[:2] {
		p := blobTrack(t, filepath.Join(r.h.c.imp.MusicDir(), "Radiohead", "OK Computer (1997)"), tr.Title+".mp3", 9)
		if err := r.svc.MarkTrackImported(r.h.ctx, tr.ID, p, string(music.QualityMP3320), 320, 1, "old"); err != nil {
			t.Fatal(err)
		}
	}
	r.h.c.ImportMusicDownloads(r.h.ctx)
	if s := r.grabStatus(t); s != grabStatusGrabbed {
		t.Fatalf("after a refusal: status %q, want still grabbed", s)
	}
	if r.h.c.musicRetryDue(hashFor(r.release)) {
		t.Fatal("a refused download must wait before the next try")
	}
	// Time moves on: each try is refused again, until the fifth goes to Review.
	for i := 0; i < musicRetryReviewAfter-1; i++ {
		r.h.later(13 * time.Hour)
		r.h.c.ImportMusicDownloads(r.h.ctx)
	}
	if !r.h.c.hasReview(r.h.ctx, hashFor(r.release)) {
		t.Error("after repeated refusals the download should be in Review")
	}
	tracks, _ = r.svc.Tracks(r.h.ctx, r.album.ID)
	for _, tr := range tracks[:2] {
		if tr.Format != string(music.QualityMP3320) || !strings.HasSuffix(tr.FilePath, ".mp3") {
			t.Errorf("track %q changed to %q at %s", tr.Title, tr.Format, tr.FilePath)
		}
		if _, err := os.Stat(tr.FilePath); err != nil {
			t.Errorf("the held MP3 must still be there: %v", err)
		}
	}
}

// An in-flight grab for an album another download has since completed is closed keeping
// its files, never as 'imported' (whose seed cleanup may take its data).
func TestCompleteAlbumInFlightGrabKeepsFiles(t *testing.T) {
	h := newStallHarness(t)
	withMusic(t, h) // album 5 is complete
	const release = "Portishead - Dummy (1994) [FLAC]"
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash)
		VALUES (5, ?, 'Fake', 60, 'music', ?)`, release, hashFor(release))
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	h.qbit.stalledTorrent(hashFor(release), release, download.CategoryMusic)
	h.observe()
	if s := h.status(t, gid); s != grabStatusDismissed {
		t.Errorf("status = %q, want dismissed", s)
	}
}

func TestPartialImportMarksGrabImported(t *testing.T) {
	r := newImportRig(t, true, "01 - One.flac", "02 - Two.flac")
	h, svc, a, release := r.h, r.svc, r.artist, r.release

	h.c.ImportMusicDownloads(h.ctx)

	if status := r.grabStatus(t); status != grabStatusImported {
		t.Errorf("grab status = %q, want imported after a clean partial import", status)
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
// blocklisted) when its torrent leaves the client: it's closed, keeping its files.
func TestImportedAlbumGrabNotBlockedWhenGone(t *testing.T) {
	h := newStallHarness(t)
	withMusic(t, h)
	mustExec(t, h.c, `INSERT INTO albums (id, artist_id, mbid, title, monitored) VALUES (9, 5, 'rg9', 'OK Computer', 1)`)
	putTracks(t, h.c, 9, "Airbag", "Paranoid Android")
	const release = "Radiohead - OK Computer (1997) [FLAC]"
	gid := h.addGoneGrab(t, "music", 9, release, grabStatusGrabbed)
	mustExec(t, h.c, `INSERT INTO imports (download_hash, source_path, target_path, title) VALUES (?, '', '', ?)`, hashFor(release), release)

	h.goneForTheGrace()
	if s := h.status(t, gid); s != grabStatusDismissed {
		t.Errorf("status = %q, want dismissed: done with, files kept", s)
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
