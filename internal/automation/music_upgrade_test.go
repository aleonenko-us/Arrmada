package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// musicProfileRef saves a music profile and returns its ref.
func musicProfileRef(t *testing.T, h *musicHarness, name string, scores map[string]int, upgrades bool) string {
	t.Helper()
	sp, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaMusic, Name: name,
		FormatScores: scores, MinFormatScore: 1, UpgradesEnabled: upgrades})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("custom:%d", sp.ID)
}

// losslessOrMP3 is the "Lossless, or the best MP3" ladder.
var losslessOrMP3 = map[string]int{
	"FLAC-24": 130, "FLAC": 120, "ALAC": 110, "WAV": 100,
	"MP3-320": 60, "MP3-V0": 55, "AAC-256": 45, "MP3-256": 40, "MP3-V2": 30,
}

// holdAlbum gives every track of an album (n of them when n > 0) a file of the given tier
// on record (rows only: searching never touches the files).
func holdAlbum(t *testing.T, h *musicHarness, al music.Album, tier string, bitrate, n int) music.Album {
	t.Helper()
	if err := h.svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}
	tracks, err := h.svc.Tracks(h.ctx, al.ID)
	if err != nil || len(tracks) == 0 {
		t.Fatalf("tracks: %v (%d)", err, len(tracks))
	}
	for i, tr := range tracks {
		if n > 0 && i >= n {
			break
		}
		path := fmt.Sprintf("/music/%d/%02d.mp3", al.ID, tr.TrackNumber)
		if err := h.svc.MarkTrackImported(h.ctx, tr.ID, path, tier, bitrate, 8<<20, "seed"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.svc.AlbumRecord(h.ctx, al.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// upgradeGrabs lists the music grabs recorded as upgrades.
func upgradeGrabs(t *testing.T, h *musicHarness) []string {
	t.Helper()
	rows, err := h.c.db.Query(`SELECT title FROM grabs WHERE media_type = 'music' AND decided_mode = ?`, decidedUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// MUS-16: an album held as MP3-320 under "Lossless, or the best MP3" is searched and the
// FLAC grabbed, recorded as an upgrade; a V0 (scored lower, and no better) is never taken.
func TestMusicUpgradeBelowCutoffIsSearched(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer"}, nil, "1997-05-21")
	al := holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
	if !al.Complete() {
		t.Fatalf("album %d/%d, want complete", al.HaveTracks, al.TrackCount)
	}

	// Only a V0 and another 320 out there: searched, nothing grabbed, a miss.
	h.releases = found(rel("Radiohead - OK Computer (1997) [MP3 V0]"), rel("Radiohead - OK Computer (1997) [MP3 320]"))
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != 1 || len(h.grabs) != 0 {
		t.Fatalf("queries %v grabs %v, want one search and nothing grabbed", h.queries, h.grabs)
	}
	st, _ := h.svc.UpgradeStateOf(h.ctx, al.ID)
	if st.Misses != 1 || st.LastAt == "" {
		t.Errorf("upgrade state = %+v, want one miss", st)
	}
	// The backoff holds it until its wait runs out.
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != 1 {
		t.Fatalf("searched again inside the backoff: %v", h.queries)
	}
	// The missing-album sweep never touches a complete album.
	h.c.SearchMusicMissing(h.ctx)
	if len(h.queries) != 1 {
		t.Fatalf("the missing sweep searched a complete album: %v", h.queries)
	}

	// Once the backoff is over a FLAC turns up: grabbed, as an upgrade.
	if _, err := h.c.db.Exec(`UPDATE albums SET last_upgrade_search_at = datetime('now', '-1 day') WHERE id = ?`, al.ID); err != nil {
		t.Fatal(err)
	}
	h.releases = found(rel("Radiohead - OK Computer (1997) [MP3 V0]"), rel("Radiohead - OK Computer (1997) [FLAC]"))
	h.c.UpgradeMusic(h.ctx)
	if len(h.grabs) != 1 || !strings.Contains(h.grabs[0], "FLAC") {
		t.Fatalf("grabs %v, want the FLAC", h.grabs)
	}
	if ug := upgradeGrabs(t, h); len(ug) != 1 {
		t.Errorf("grabs recorded as upgrades = %v, want the FLAC", ug)
	}
	if st, _ := h.svc.UpgradeStateOf(h.ctx, al.ID); st.Misses != 0 {
		t.Errorf("a grab should clear the upgrade backoff: %+v", st)
	}
	if n := artistEvents(t, h.svc, a.ID, "Upgrading"); n != 1 {
		t.Errorf("history lines about the upgrade = %d, want 1", n)
	}
	// Recorded as an upgrade search, not as "why is it missing".
	all, _ := h.c.SearchAttempts(h.ctx, AttemptMusic, al.ID, 0, 20)
	for _, at := range all {
		if at.Scope != ScopeUpgrade || at.Trigger != TriggerUpgrade {
			t.Errorf("attempt %+v, want scope and trigger upgrade", at)
		}
	}
	// Downloading now (its grab is in flight): the next sweep leaves it alone.
	if _, err := h.c.db.Exec(`UPDATE albums SET last_upgrade_search_at = NULL WHERE id = ?`, al.ID); err != nil {
		t.Fatal(err)
	}
	before := len(h.queries)
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != before {
		t.Errorf("searched an album whose upgrade is downloading")
	}
	// Imported, but only part of it was better (the album is still below its cutoff): the
	// same release is never grabbed again, or every sweep would download it once more.
	if _, err := h.c.db.Exec(`UPDATE grabs SET status = ?`, grabStatusImported); err != nil {
		t.Fatal(err)
	}
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != before+1 || len(h.grabs) != 1 {
		t.Errorf("queries %d grabs %v, want one more search and the same release left alone", len(h.queries)-before, h.grabs)
	}
}

// At its cutoff (FLAC held: lossless never replaces lossless), with upgrades off, part
// held, or a file whose tier isn't known: never searched.
func TestMusicUpgradeLeavesAlone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upgrades bool
		tier     string
		bitrate  int
		held     int // tracks held (0 = all)
	}{
		{"at its cutoff", true, "FLAC", 0, 0},
		{"hi-res held", true, "FLAC-24", 0, 0},
		{"upgrades off", false, "MP3-320", 320, 0},
		{"part held", true, "MP3-320", 320, 1},
		{"tier unknown", true, "MP3", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := musicTestCoord(t)
			ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, tc.upgrades)
			a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer"}, nil, "1997-05-21")
			holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), tc.tier, tc.bitrate, tc.held)
			h.releases = found(rel("Radiohead - OK Computer (1997) [FLAC 24bit]"), rel("Radiohead - OK Computer (1997) [FLAC]"))
			h.c.UpgradeMusic(h.ctx)
			if len(h.queries) != 0 || len(h.grabs) != 0 {
				t.Fatalf("queries %v grabs %v, want nothing searched", h.queries, h.grabs)
			}
		})
	}
}

// A profile that prefers MP3 over FLAC never "upgrades" its MP3s to FLAC, even though FLAC
// is better audio: the profile's preference is the cutoff.
func TestMusicUpgradeFollowsTheProfilesPreference(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "MP3 first", map[string]int{"MP3-320": 100, "FLAC": 50}, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer"}, nil, "1997-05-21")
	holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "MP3-320", 320, 0)
	h.releases = found(rel("Radiohead - OK Computer (1997) [FLAC]"))
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != 0 || len(h.grabs) != 0 {
		t.Fatalf("queries %v grabs %v, want nothing: MP3-320 is this profile's top", h.queries, h.grabs)
	}
}

// The first sweep after an update doesn't search a whole library: at most musicUpgradeCap
// albums a run, never-searched first; the per-sweep upgrade budget caps the grabs.
func TestMusicUpgradeIsPaced(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	const albums = musicUpgradeCap + 4
	var titles []string
	for i := 0; i < albums; i++ {
		titles = append(titles, fmt.Sprintf("Album %02d", i))
	}
	a := h.addArtist(t, "Prolific", ref, titles, nil, "2001-01-01")
	for _, title := range titles {
		holdAlbum(t, h, h.album(t, a.ID, title), "MP3-320", 320, 0)
	}

	h.c.UpgradeMusic(h.ctx) // nothing out there
	if len(h.queries) != musicUpgradeCap {
		t.Fatalf("first sweep searched %d albums, want the cap %d", len(h.queries), musicUpgradeCap)
	}
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != albums {
		t.Fatalf("second sweep: %d searches in all, want the remaining %d searched (the others back off)", len(h.queries), albums-musicUpgradeCap)
	}
	h.c.UpgradeMusic(h.ctx)
	if len(h.queries) != albums {
		t.Fatalf("third sweep searched again inside every album's backoff: %d", len(h.queries))
	}

	// With FLACs everywhere, a budget of 2 grabs 2 and stops searching.
	if _, err := h.c.db.Exec(`UPDATE albums SET last_upgrade_search_at = NULL, upgrade_misses = 0`); err != nil {
		t.Fatal(err)
	}
	h.c.SetUpgradeBudget(func(context.Context) int { return 2 })
	h.releases = func(q indexer.SearchQuery) (indexer.SearchResult, error) {
		return indexer.SearchResult{Releases: []indexer.Release{rel(strings.Replace(q.Text, "Prolific ", "Prolific - ", 1) + " [FLAC]")}}, nil
	}
	before := len(h.queries)
	h.c.UpgradeMusic(h.ctx)
	if len(h.grabs) != 2 {
		t.Fatalf("grabs %v, want the budget's 2", h.grabs)
	}
	if n := len(h.queries) - before; n != 2 {
		t.Errorf("searched %d albums, want the sweep to stop once the budget was spent", n)
	}
}

// A person's Search now on a complete album is an upgrade search, and only below its
// cutoff; at its cutoff nothing is searched (another copy would be a download nobody
// wanted). An album with a download in flight isn't searched at all.
func TestSearchAlbumNowOnACompleteAlbum(t *testing.T) {
	h := musicTestCoord(t)
	ref := musicProfileRef(t, h, "Lossless, or the best MP3", losslessOrMP3, true)
	a := h.addArtist(t, "Radiohead", ref, []string{"OK Computer", "Kid A"}, nil, "2000-10-02")
	holdAlbum(t, h, h.album(t, a.ID, "OK Computer"), "FLAC", 0, 0)
	holdAlbum(t, h, h.album(t, a.ID, "Kid A"), "MP3-320", 320, 0)
	h.releases = func(q indexer.SearchQuery) (indexer.SearchResult, error) {
		return indexer.SearchResult{Releases: []indexer.Release{rel(strings.Replace(q.Text, "Radiohead ", "Radiohead - ", 1) + " [FLAC]")}}, nil
	}

	out, err := h.c.SearchAlbumNow(h.ctx, h.album(t, a.ID, "OK Computer").ID)
	if err != nil || out.Reason != ReasonNothingWanted || len(h.queries) != 0 {
		t.Fatalf("at cutoff: %+v %v, queries %v; want nothing searched", out, err, h.queries)
	}
	kid := h.album(t, a.ID, "Kid A")
	out, err = h.c.SearchAlbumNow(h.ctx, kid.ID)
	if err != nil || out.Grabbed != 1 || len(h.grabs) != 1 || !strings.Contains(h.grabs[0], "Kid A") {
		t.Fatalf("below cutoff: %+v %v, grabs %v; want the FLAC", out, err, h.grabs)
	}
	if ug := upgradeGrabs(t, h); len(ug) != 1 {
		t.Errorf("upgrade grabs = %v", ug)
	}
	// Now downloading: a second click searches nothing and says why.
	out, err = h.c.SearchAlbumNow(h.ctx, kid.ID)
	if err != nil || out.Reason != ReasonAlreadyDownloading || len(h.queries) != 1 {
		t.Fatalf("in flight: %+v %v, queries %v; want already downloading", out, err, h.queries)
	}
}

// The upgrade cutoff helpers.
func TestUpgradeTiers(t *testing.T) {
	if got := music.Cutoff(losslessOrMP3); got != music.QualityFLAC24 {
		t.Errorf("cutoff = %s, want FLAC-24", got)
	}
	for _, tc := range []struct {
		held, cand music.Quality
		want       bool
	}{
		{music.QualityMP3320, music.QualityFLAC, true},
		{music.QualityMP3320, music.QualityMP3V0, false}, // sideways, scored lower
		{music.QualityMP3320, music.QualityMP3320, false},
		{music.QualityMP3V2, music.QualityMP3V0, true},
		{music.QualityFLAC, music.QualityFLAC24, false},   // lossless never replaces lossless
		{music.QualityMP3128, music.QualityMP3192, false}, // not on the ladder
		{music.QualityUnknown, music.QualityFLAC, false},  // never on a guess
	} {
		if got := music.IsUpgrade(tc.cand, tc.held, losslessOrMP3); got != tc.want {
			t.Errorf("IsUpgrade(%s over %s) = %v, want %v", tc.cand, tc.held, got, tc.want)
		}
	}
}

// --- the import side: an upgrade never changes the album's edition ------------------

// upgradeRig is a matchRig whose album holds MP3s of the standard edition on disk, and
// whose download was grabbed by the upgrade sweep.
func upgradeRig(t *testing.T, files []string, pinned bool) (*matchRig, []string) {
	t.Helper()
	r := newMatchRig(t, false, files)
	if pinned {
		std := r.editionID(t, "rel-std")
		if _, err := r.svc.SetEdition(r.h.ctx, r.album.ID, std, true); err != nil {
			t.Fatal(err)
		}
	}
	tracks, err := r.svc.Tracks(r.h.ctx, r.album.ID)
	if err != nil || len(tracks) != len(okcTitles) {
		t.Fatalf("tracks %d (%v)", len(tracks), err)
	}
	dir := filepath.Join(r.h.c.imp.MusicDir(), "Radiohead", "OK Computer (1997)")
	var held []string
	for i, tr := range tracks {
		p := blobTrack(t, dir, fmt.Sprintf("%02d - %s.mp3", i+1, okcTitles[i]), 9)
		if err := r.svc.MarkTrackImported(r.h.ctx, tr.ID, p, "MP3-320", 320, 8<<20, "seed"); err != nil {
			t.Fatal(err)
		}
		held = append(held, p)
	}
	ctx, notes := newSearchNotes(r.h.ctx)
	notes.picked(r.release, quality.KeyCustom, decidedUpgrade)
	r.h.c.recordMusicGrab(ctx, "music", r.album.ID, r.release, "Fake", "", hashFor(r.release))
	return r, held
}

// An upgrade of the album's own edition imports: the FLACs replace the MP3s, which go to
// the recycle bin.
func TestMusicUpgradeImportReplacesIntoTheBin(t *testing.T) {
	files := trackFileNames(okcTitles)
	r, held := upgradeRig(t, files, true)
	r.tagAll(files, okcTitles)
	r.h.c.ImportMusicDownloads(r.h.ctx)
	al, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
	if !al.Complete() {
		t.Fatalf("album %d/%d after the upgrade", al.HaveTracks, al.TrackCount)
	}
	if q, _, _ := r.svc.HeldTiers(r.h.ctx, al.ID); q != music.QualityFLAC {
		t.Fatalf("held tier = %s, want FLAC", q)
	}
	for _, p := range held {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the replaced MP3 %s is still in the library (%v)", filepath.Base(p), err)
		}
	}
	if n := countFiles(t, r.bin); n < len(held) {
		t.Errorf("recycle bin holds %d files, want the %d replaced MP3s", n, len(held))
	}
}

// The upgrade sweep's download of another edition (the deluxe) never switches the album,
// pinned or not, and never puts another edition's files on an album kept on its edition:
// it waits in Reviews, and nothing is replaced.
func TestMusicUpgradeImportKeepsTheEdition(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			all := append(append([]string(nil), okcTitles...), okcBonus...)
			files := trackFileNames(all)
			r, held := upgradeRig(t, files, pinned)
			r.tagAll(files, all)
			for _, f := range files {
				tg := r.tags[f]
				tg.ReleaseMBID = "rel-dlx"
				r.tags[f] = tg
			}
			before, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
			r.h.c.ImportMusicDownloads(r.h.ctx)
			after, _ := r.svc.AlbumRecord(r.h.ctx, r.album.ID)
			if after.TargetEditionID != before.TargetEditionID || after.EditionPinned != pinned {
				t.Fatalf("edition %d pinned=%v, want it left on %d", after.TargetEditionID, after.EditionPinned, before.TargetEditionID)
			}
			rv := reviewFor(t, r.h.c, hashFor(r.release))
			if rv.ReasonCode != ReasonCloseEditions || !strings.Contains(rv.Reason, "another edition") {
				t.Fatalf("review %+v, want the edition hold", rv)
			}
			if q, _, _ := r.svc.HeldTiers(r.h.ctx, after.ID); q != music.QualityMP3320 {
				t.Errorf("held tier = %s, want the MP3s untouched", q)
			}
			for _, p := range held {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("a held MP3 was touched: %v", err)
				}
			}
			if n := artistEvents(t, r.svc, r.artist.ID, "Switched"); n != 0 {
				t.Errorf("history says the album switched edition (%d lines)", n)
			}
		})
	}
}

// countFiles counts the regular files under dir.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
