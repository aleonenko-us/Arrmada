package automation

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// The movie upgrade sweep tries a release once per track, as the series sweep does per
// show: a release grabbed for a track before — whatever became of that grab — is never
// that track's upgrade again. Otherwise a grab the import gate kept out (or found already
// imported) leaves the file as it was, and the same release wins again every sweep.

// upgradeMovieHarness is the stall harness with Heat on disk at 720p under a profile that
// upgrades.
func upgradeMovieHarness(t *testing.T) (*stallHarness, int64, string) {
	t.Helper()
	h := newStallHarness(t)
	id := h.addMovie(t, 90, "Heat", 1995)
	sp, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "Upgrades", UpgradesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	if _, err := h.c.db.Exec(`UPDATE movies SET quality_profile = ?, has_file = 1, movie_file_path = '/lib/Heat (1995)/Heat.mkv',
		source_release = 'Heat.1995.720p.WEB-DL.x264-OLD' WHERE id = ?`, ref, id); err != nil {
		t.Fatal(err)
	}
	return h, id, ref
}

// sweepMovie runs the upgrade search for one movie, as the sweep does.
func sweepMovie(t *testing.T, h *stallHarness, id int64) {
	t.Helper()
	m, err := h.c.movies.Get(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.c.upgradeMovie(h.ctx, m, nil); err != nil {
		t.Fatal(err)
	}
}

// movieGrabRow is one grab as the movie's history holds it.
type movieGrabRow struct {
	Version int64
	Title   string
}

func movieGrabRows(t *testing.T, h *stallHarness, id int64) []movieGrabRow {
	t.Helper()
	rows, err := h.c.db.Query(`SELECT version_id, title FROM grabs WHERE media_type = 'movie' AND movie_id = ? ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []movieGrabRow
	for rows.Next() {
		var g movieGrabRow
		if err := rows.Scan(&g.Version, &g.Title); err != nil {
			t.Fatal(err)
		}
		out = append(out, g)
	}
	return out
}

// tryBefore records a closed grab of title for track vid, as an earlier sweep left it.
func tryBefore(t *testing.T, h *stallHarness, id, vid int64, title, status string) {
	t.Helper()
	if _, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, version_id, title, indexer, media_type, info_hash, status)
		VALUES (?, ?, ?, 'Fake', 'movie', ?, ?)`, id, vid, title, hashFor(title), status); err != nil {
		t.Fatal(err)
	}
}

// A release grabbed for the movie before and closed as imported — the file still the old
// one, as when the import gate kept it — is not grabbed again; a different better release
// still is.
func TestMovieUpgradeSweepTriesAReleaseOnce(t *testing.T) {
	h, id, _ := upgradeMovieHarness(t)
	// The grab recorded the indexer's title in dots; the indexer now writes it in spaces.
	const tried = "Heat 1995 1080p BluRay x264-NEW"
	tryBefore(t, h, id, 0, "Heat.1995.1080p.BluRay.x264-NEW", grabStatusImported)
	h.ix.offer(tried)
	sweepMovie(t, h, id)
	if got := movieGrabRows(t, h, id); len(got) != 1 {
		t.Fatalf("grabs = %+v — the sweep grabbed a release it had tried before", got)
	}

	const other = "Heat 1995 1080p BluRay x264-OTHER"
	h.ix.offer(tried, other)
	sweepMovie(t, h, id)
	got := movieGrabRows(t, h, id)
	if len(got) != 2 || got[1] != (movieGrabRow{0, other}) {
		t.Fatalf("grabs = %+v, want %s grabbed for the default track", got, other)
	}
}

// Whatever became of the grab, the sweep doesn't come back for it: its first grab of a
// better release is its last.
func TestMovieUpgradeSweepDoesNotRegrabAfterAnyEnding(t *testing.T) {
	h, id, _ := upgradeMovieHarness(t)
	const better = "Heat 1995 1080p BluRay x264-NEW"
	h.ix.offer(better)
	sweepMovie(t, h, id)
	if got := movieGrabRows(t, h, id); !reflect.DeepEqual(got, []movieGrabRow{{0, better}}) {
		t.Fatalf("first sweep grabbed %+v, want %s", got, better)
	}
	for _, status := range []string{grabStatusImported, grabStatusFailed, grabStatusRemoved} {
		if _, err := h.c.db.Exec(`UPDATE grabs SET status = ?`, status); err != nil {
			t.Fatal(err)
		}
		sweepMovie(t, h, id)
		if got := movieGrabRows(t, h, id); len(got) != 1 {
			t.Fatalf("after a %s grab the sweep grabbed %+v — the same upgrade again", status, got)
		}
	}
}

// A release the user picks by hand is theirs to grab, tried before or not: the guard is
// the upgrade sweep's alone.
func TestManualGrabOfATriedReleaseStillWorks(t *testing.T) {
	h, id, _ := upgradeMovieHarness(t)
	const tried = "Heat.1995.1080p.BluRay.x264-NEW"
	tryBefore(t, h, id, 0, tried, grabStatusImported)
	hash, err := h.c.Grab(h.ctx, "Fake", magnetFor(tried), tried)
	if err != nil {
		t.Fatal(err)
	}
	h.c.RecordManualGrab(h.ctx, id, 0, tried, "Fake", hash)
	if got := movieGrabRows(t, h, id); len(got) != 2 || got[1] != (movieGrabRow{0, tried}) {
		t.Fatalf("grabs = %+v, want the manual grab recorded", got)
	}
}

// The rule is per track: the tracks are judged apart, so a release the default track tried
// (and kept out) can still be another track's upgrade — and is grabbed for that track, not
// handed back to the one that already tried it.
func TestTriedReleaseIsPerTrack(t *testing.T) {
	h, id, ref := upgradeMovieHarness(t)
	v, err := h.c.movies.AddVersion(h.ctx, id, "Second", ref, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.db.Exec(`UPDATE movie_versions SET has_file = 1, file_path = '/lib/Heat (1995)/Heat - Second.mkv',
		source_release = 'Heat.1995.720p.HDTV.x264-B' WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	const better = "Heat 1995 1080p BluRay x264-NEW"
	tryBefore(t, h, id, 0, better, grabStatusImported)
	h.ix.offer(better)
	sweepMovie(t, h, id)
	got := movieGrabRows(t, h, id)
	if len(got) != 2 || got[1] != (movieGrabRow{v.ID, better}) {
		t.Fatalf("grabs = %+v, want %s grabbed once, for the second track (%d)", got, better, v.ID)
	}
	// And once that track has tried it too, neither takes it again.
	if _, err := h.c.db.Exec(`UPDATE grabs SET status = ?`, grabStatusImported); err != nil {
		t.Fatal(err)
	}
	sweepMovie(t, h, id)
	if got := movieGrabRows(t, h, id); len(got) != 2 {
		t.Fatalf("grabs = %+v — a track grabbed a release it had tried", got)
	}
}
