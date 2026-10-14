package automation

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// The update to a build with stall fail-over on by default met months of grab rows that
// were never closed: the same release grabbed again every day it failed to import, torrents
// Arrmada itself had removed at their seed goal, packs whose episodes were long in the
// library. Each row failed over on its own — The Expanse's complete pack nine times, with a
// blocklist line, a history line and a search apiece. These pin the behaviour that replaced
// it: one decision per download, and a download that has finished closes without a search.

// addGoneGrab records a grab made seven hours ago, with a one-hour stall window, whose
// torrent the client no longer lists.
func (h *stallHarness) addGoneGrab(t *testing.T, mediaType string, id int64, release, status string) int64 {
	t.Helper()
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash, grabbed_at, status)
		VALUES (?, ?, 'Fake', 60, ?, ?, datetime('now', '-7 hours'), ?)`, id, release, mediaType, hashFor(release), status)
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	return gid
}

// goneForTheGrace runs the stall check on a client that doesn't list the torrents, then
// again once that has held for the full grace — the first check that may act on it.
func (h *stallHarness) goneForTheGrace() {
	h.observe()
	h.later(stallEvidenceGrace)
	h.observe()
}

// addShow is a monitored show with one aired season of two episodes, both on disk when
// onDisk is set.
func (h *stallHarness) addShow(t *testing.T, onDisk bool) int64 {
	t.Helper()
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 9, Title: "Show", Year: 2020, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	ep := func(e int) series.Episode {
		return series.Episode{SeasonNumber: 1, EpisodeNumber: e, AirDate: "2021-01-01", Monitored: true}
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{
		{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{ep(1), ep(2)}},
	}); err != nil {
		t.Fatal(err)
	}
	if onDisk {
		if _, err := h.c.db.Exec(`UPDATE episodes SET has_file = 1, file_path = '/tv/Show/S01E0' || episode_number || '.mkv'
			WHERE series_id = ?`, sr.ID); err != nil {
			t.Fatal(err)
		}
	}
	return sr.ID
}

func (h *stallHarness) seriesFailedEvents(t *testing.T, id int64) int {
	t.Helper()
	evs, err := h.c.series.Events(h.ctx, id, 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Event == "failed" {
			n++
		}
	}
	return n
}

// captureLog points the coordinator's log at a buffer.
func (h *stallHarness) captureLog() *bytes.Buffer {
	var buf bytes.Buffer
	h.c.log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

// The same release recorded three times for one film — re-grabbed on later days while the
// first never imported — and then gone from the client: one fail-over, not three. One
// history line, one search, every row closed (and stamped vanished) by it, and no
// blocklist row — the first disappearance isn't held against the release.
func TestDuplicateGrabsFailOverOnce(t *testing.T) {
	h := newStallHarness(t)
	const gone = "Rare.Film.1971.1080p.BluRay.x264-OLD"
	const alt = "Rare.Film.1971.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	var gids []int64
	for range 3 {
		gids = append(gids, h.addGoneGrab(t, "movie", mid, gone, grabStatusGrabbed))
	}
	h.ix.offer(gone, alt)

	h.goneForTheGrace()

	for _, gid := range gids {
		if s := h.status(t, gid); s != grabStatusFailed {
			t.Errorf("grab %d status = %q, want failed with the others", gid, s)
		}
		var at int64
		if err := h.c.db.QueryRow(`SELECT vanished_at FROM grabs WHERE id = ?`, gid).Scan(&at); err != nil || at == 0 {
			t.Errorf("grab %d vanished_at = %d (%v), want it stamped with the others", gid, at, err)
		}
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want none", n)
	}
	if n := h.movieEvents(t, mid, gone+" disappeared from the download client"); n != 1 {
		t.Errorf("'disappeared' history lines = %d, want 1", n)
	}
	if calls := h.qbit.callLog(); len(calls) != 1 || calls[0] != "add "+magnetFor(alt) {
		t.Errorf("client calls = %q, want the alternate added once", calls)
	}
	searches := h.ix.searchCount()
	if searches == 0 {
		t.Error("the fail-over should have searched for an alternate")
	}

	// Nothing is left to fail over on the next check.
	h.later(stallEvidenceGrace)
	h.observe()
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("after another check, blocklist rows = %d, want still none", n)
	}
	if n := h.ix.searchCount(); n != searches {
		t.Errorf("the next check searched again (%d more requests)", n-searches)
	}
}

// A single search request per fail-over is what TestDuplicateGrabsFailOverOnce compares
// against: the same fail-over for one row.
func TestSingleGrabFailOverSearchesOnce(t *testing.T) {
	one, three := newStallHarness(t), newStallHarness(t)
	const gone = "Rare.Film.1971.1080p.BluRay.x264-OLD"
	for i, h := range []*stallHarness{one, three} {
		mid := h.addMovie(t, 1, "Rare Film", 1971)
		for range 1 + 2*i {
			h.addGoneGrab(t, "movie", mid, gone, grabStatusGrabbed)
		}
		h.ix.offer(gone) // nothing to replace it with: every row searches if it fails over
		h.goneForTheGrace()
	}
	if a, b := one.ix.searchCount(), three.ix.searchCount(); a == 0 || a != b {
		t.Errorf("search requests: one row %d, three duplicate rows %d — want the same, non-zero", a, b)
	}
}

// A season pack recorded twice whose episodes are already in the library, and whose torrent
// is gone (removed at its goal by an older build, or by hand): there is nothing to fail over
// for. Both rows close as imported — no blocklist, no search, no "failed" history — and the
// log says once how many stale grabs were closed.
func TestVanishedPackAlreadyInLibraryClosesQuietly(t *testing.T) {
	h := newStallHarness(t)
	logs := h.captureLog()
	sid := h.addShow(t, true)
	const pack = "Show.S01.1080p.WEB-DL.x264-GRP"
	a := h.addGoneGrab(t, "series", sid, pack, grabStatusGrabbed)
	b := h.addGoneGrab(t, "series", sid, pack, grabStatusGrabbed)
	h.ix.offer("Show.S01.1080p.BluRay.x264-NEW")

	h.goneForTheGrace()

	for _, gid := range []int64{a, b} {
		if s := h.status(t, gid); s != grabStatusImported {
			t.Errorf("grab %d status = %q, want imported", gid, s)
		}
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want none — the pack landed", n)
	}
	if n := h.ix.searchCount(); n != 0 {
		t.Errorf("searches = %d, want none — the library has every episode", n)
	}
	if n := h.seriesFailedEvents(t, sid); n != 0 {
		t.Errorf("'failed' history lines = %d, want none", n)
	}
	out := logs.String()
	if strings.Contains(out, "disappeared from the client") {
		t.Errorf("a finished download was logged as disappeared:\n%s", out)
	}
	if n := strings.Count(out, "automation: stall check finished"); n != 1 {
		t.Fatalf("summary lines = %d, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "closed=2") || !strings.Contains(out, "failed_over=0") {
		t.Errorf("summary should count 2 closed and 0 failed over:\n%s", out)
	}
}

// Another row for the same torrent has already been settled — seeded and removed by
// Arrmada at its goal, removed by the user, failed over before, or waiting in Review. The
// stale duplicate takes that outcome quietly (or waits, for Review) and never fails over
// a second time, even though the show still wants the episodes.
func TestDuplicateOfASettledGrabClosesQuietly(t *testing.T) {
	for _, tc := range []struct{ twin, want string }{
		{grabStatusSeeded, grabStatusSeeded},
		{grabStatusImported, grabStatusImported},
		{grabStatusRemoved, grabStatusRemoved},
		{grabStatusFailed, grabStatusFailed},
		{grabStatusHeld, grabStatusGrabbed}, // the review decides; nothing happens meanwhile
	} {
		t.Run(tc.twin, func(t *testing.T) {
			h := newStallHarness(t)
			sid := h.addShow(t, false)
			const rel = "Show.S01E01.1080p.WEB-DL.x264-GRP"
			h.addGoneGrab(t, "series", sid, rel, tc.twin)
			dup := h.addGoneGrab(t, "series", sid, rel, grabStatusGrabbed)
			h.ix.offer("Show.S01E01.1080p.WEB-DL.x264-NEW")

			h.goneForTheGrace()

			if s := h.status(t, dup); s != tc.want {
				t.Errorf("duplicate status = %q, want %q", s, tc.want)
			}
			if n := h.blocklistCount(t); n != 0 {
				t.Errorf("blocklist rows = %d, want none", n)
			}
			if n := h.ix.searchCount(); n != 0 {
				t.Errorf("searches = %d, want none", n)
			}
			if calls := h.qbit.callLog(); len(calls) != 0 {
				t.Errorf("client calls = %q, want none", calls)
			}
		})
	}
}

// A torrent Arrmada removes at its seed goal closes every row recorded for it, not only the
// one seed cleanup matched — a duplicate left 'grabbed' reads as a vanished download.
func TestSeedCleanupClosesDuplicateRows(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	stale := h.seriesGrab(t, name, hash) // the same release grabbed again, never closed
	done := h.seriesGrab(t, name, hash)
	if _, err := h.c.db.Exec(`UPDATE grabs SET status = ? WHERE id = ?`, grabStatusImported, done); err != nil {
		t.Fatal(err)
	}
	h.seededTorrent(hash, name)

	h.c.ManageSeeding(h.ctx)

	if len(*h.removed) != 1 {
		t.Fatalf("removals = %+v, want the torrent removed once", *h.removed)
	}
	for _, gid := range []int64{stale, done} {
		if s := h.status(t, gid); s != grabStatusSeeded {
			t.Errorf("grab %d status = %q, want seeded", gid, s)
		}
	}
}

// "Remove" on the Downloads page closes every in-flight row for the torrent: a duplicate
// left 'grabbed' would be failed over (blocklisted, re-searched) once the torrent is gone.
func TestRemoveDownloadClosesDuplicateRows(t *testing.T) {
	c, _ := removeCoord(t)
	ctx := context.Background()
	mid := addMovie(t, c, "Arrival")
	const rel = "Arrival.2016.1080p.BluRay.x264-GRP"
	older := addGrab(t, c, "movie", mid, rel, hashA)
	newer := addGrab(t, c, "movie", mid, rel, hashA)

	if _, err := c.RemoveDownload(ctx, hashA, "", RemoveKeepFiles, false); err != nil {
		t.Fatal(err)
	}
	for _, gid := range []int64{older, newer} {
		if s := grabStatus(t, c, gid); s != grabStatusRemoved {
			t.Errorf("grab %d status = %q, want removed", gid, s)
		}
	}
}

// "Try another release" on a download recorded twice closes both rows with the one
// replacement.
func TestFailOverNowClosesDuplicateRows(t *testing.T) {
	h := newStallHarness(t)
	const stuck = "Quiet.Film.1983.1080p.BluRay.x264-OLD"
	const alt = "Quiet.Film.1983.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Quiet Film", 1983)
	a := h.addStalledGrab(t, "movie", mid, stuck, "arrmada")
	b := h.addGoneGrab(t, "movie", mid, stuck, grabStatusGrabbed) // same hash, the torrent is listed once
	h.ix.offer(stuck, alt)

	out, err := h.c.FailOverNow(h.ctx, hashFor(stuck))
	if err != nil || out.Replacement != alt {
		t.Fatalf("FailOverNow = %+v, %v; want the alternate", out, err)
	}
	for _, gid := range []int64{a, b} {
		if s := h.status(t, gid); s != grabStatusFailed {
			t.Errorf("grab %d status = %q, want failed", gid, s)
		}
	}
}
