package automation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

func packKeys(n int) []epKey {
	out := make([]epKey, n)
	for i := range out {
		out[i] = epKey{1, i + 1}
	}
	return out
}

// verdicts answers packUpgradeDecision from fixed sets of better and worse episodes.
func verdicts(better, worse []int) func(epKey) (bool, bool) {
	return func(k epKey) (bool, bool) {
		in := func(set []int) bool {
			for _, e := range set {
				if e == k.episode {
					return true
				}
			}
			return false
		}
		return in(better), in(worse)
	}
}

func TestUpgradePackAcceptedWhenMajorityBetter(t *testing.T) {
	for _, c := range []struct {
		eps    int
		better []int
		want   int
	}{
		{10, []int{1, 2, 3, 4, 5}, 5},       // exactly half
		{10, []int{1, 2, 3, 4, 5, 6, 7}, 7}, // most
		{4, []int{1, 2, 3, 4}, 4},           // all
		{1, []int{1}, 1},
	} {
		ok, n := packUpgradeDecision(packKeys(c.eps), verdicts(c.better, nil))
		if !ok || n != c.want {
			t.Errorf("%d of %d better: ok=%v upgraded=%d, want accepted with %d", len(c.better), c.eps, ok, n, c.want)
		}
	}
}

func TestUpgradePackRejectedForMinorityOrDowngrade(t *testing.T) {
	for _, c := range []struct {
		why           string
		eps           int
		better, worse []int
	}{
		{"fewer than half", 10, []int{1, 2, 3, 4}, nil},
		{"nothing better", 10, nil, nil},
		{"one downgrade among upgrades", 10, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, []int{10}},
		{"no episodes with files", 0, nil, nil},
	} {
		if ok, _ := packUpgradeDecision(packKeys(c.eps), verdicts(c.better, c.worse)); ok {
			t.Errorf("%s: accepted", c.why)
		}
	}
}

// Over successive sweeps the per-season upgrade queries reach every season of a long show,
// a few at a time, resuming where the last sweep stopped — also as seasons drop out of the
// list between sweeps.
func TestUpgradeSeasonRotation(t *testing.T) {
	var seasons []int
	for sn := 1; sn <= 20; sn++ {
		seasons = append(seasons, sn)
	}
	seen := map[int]int{}
	cursor := 0
	for sweep := 0; sweep < 5; sweep++ {
		window, next, moved := upgradeSeasonWindow(seasons, cursor, maxUpgradeSeasonQueries)
		if len(window) != maxUpgradeSeasonQueries || !moved {
			t.Fatalf("sweep %d: window %v moved=%v", sweep, window, moved)
		}
		for _, sn := range window {
			seen[sn]++
		}
		cursor = next
	}
	for _, sn := range seasons {
		if seen[sn] != 1 {
			t.Errorf("season %d searched %d times in five sweeps, want once", sn, seen[sn])
		}
	}
	// A show that fits in one sweep is searched whole, and its cursor stays put.
	if w, next, moved := upgradeSeasonWindow([]int{1, 2, 3}, 7, maxUpgradeSeasonQueries); !reflect.DeepEqual(w, []int{1, 2, 3}) || moved || next != 7 {
		t.Errorf("small show = %v next=%d moved=%v", w, next, moved)
	}
	// Seasons that reached their target drop out; the cursor still points at a season
	// number, so the walk carries on from there and wraps.
	w, next, _ := upgradeSeasonWindow([]int{2, 5, 9, 14, 17, 18}, 15, maxUpgradeSeasonQueries)
	if !reflect.DeepEqual(w, []int{17, 18, 2, 5}) || next != 9 {
		t.Errorf("window = %v next=%d, want [17 18 2 5] next 9", w, next)
	}
}

// upgradeHarness is the stall harness with an upgrading series profile and a show whose
// episodes all have files.
type upgradeShow struct {
	h   *stallHarness
	id  int64
	ref string
}

func newUpgradeShow(t *testing.T, anime bool) upgradeShow {
	t.Helper()
	h := newStallHarness(t)
	sp, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "Upgrades", UpgradesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("custom:%d", sp.ID)
	kind := "standard"
	if anime {
		kind = "anime"
	}
	res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, year, monitored, status, quality_profile, series_type)
		VALUES (5, 'Show', 2015, 1, 'Ended', ?, ?)`, ref, kind)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return upgradeShow{h: h, id: id, ref: ref}
}

func (u upgradeShow) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := u.h.c.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// addEpisode gives the show an aired episode with a file at path.
func (u upgradeShow) addEpisode(t *testing.T, season, ep, abs int, path, source string, sizeBytes int64, held bool) {
	t.Helper()
	u.exec(t, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, u.id, season)
	u.exec(t, `INSERT INTO episodes (series_id, season_number, episode_number, absolute_number, air_date, runtime, monitored,
		has_file, file_path, size_bytes, source_release, upgrade_hold)
		VALUES (?, ?, ?, ?, '2016-01-01', 45, 1, 1, ?, ?, ?, ?)`,
		u.id, season, ep, abs, path, sizeBytes, source, boolToInt(held))
}

func (u upgradeShow) grabbedTitles(t *testing.T) []string {
	t.Helper()
	rows, err := u.h.c.db.Query(`SELECT title, manual, scope FROM grabs WHERE media_type = 'series' ORDER BY title`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title, scope string
		var manual int
		if err := rows.Scan(&title, &manual, &scope); err != nil {
			t.Fatal(err)
		}
		if manual != 0 || scope != "" {
			t.Errorf("%s was grabbed manual=%d scope=%q — an upgrade must leave the import gate on", title, manual, scope)
		}
		out = append(out, title)
	}
	return out
}

// sparseFile makes a file of n bytes without writing them.
func sparseFile(t *testing.T, path string, n int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(n); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// A finished show with 720p files and a 1080p BluRay season pack on offer grabs the pack
// — not the single episode also on offer for that season — as an automatic grab. When it
// arrives, the import replaces only the episodes the pack beats: not one already as good,
// and never a held one.
func TestUpgradeSeasonPackReplacesOnlyWhatItBeats(t *testing.T) {
	u := newUpgradeShow(t, false)
	lib := t.TempDir()
	const mb = 1 << 20
	files := map[int]string{}
	for e := 1; e <= 4; e++ {
		files[e] = filepath.Join(lib, "Show", "Season 01", fmt.Sprintf("Show - S01E%02d.mkv", e))
		sparseFile(t, files[e], 30*mb)
	}
	u.addEpisode(t, 1, 1, 1, files[1], "Show.S01E01.720p.WEB-DL.x264-OLD", 1<<30, false)
	u.addEpisode(t, 1, 2, 2, files[2], "Show.S01E02.720p.WEB-DL.x264-OLD", 1<<30, false)
	u.addEpisode(t, 1, 3, 3, files[3], "Show.S01E03.1080p.BluRay.x264-GOOD", 6<<30, false)
	u.addEpisode(t, 1, 4, 4, files[4], "Show.S01E04.720p.WEB-DL.x264-OLD", 1<<30, true)
	const pack = "Show.S01.1080p.BluRay.x264-GRP"
	u.h.ix.offer(pack, "Show.S01E01.1080p.WEB-DL.x264-NEW")

	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); !reflect.DeepEqual(got, []string{pack}) {
		t.Fatalf("grabbed %v, want only the season pack", got)
	}

	// The pack arrives.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	u.h.c.imp = library.NewImporter(t.TempDir(), log)
	dl := filepath.Join(t.TempDir(), pack)
	for e := 1; e <= 4; e++ {
		sparseFile(t, filepath.Join(dl, fmt.Sprintf("Show.S01E%02d.1080p.BluRay.x264-GRP.mkv", e)), 60*mb)
	}
	s, err := u.h.c.series.Get(u.h.ctx, u.id)
	if err != nil {
		t.Fatal(err)
	}
	placed, _, _, failed := u.h.c.importSeriesInto(u.h.ctx, s, dl, u.h.c.grabForce(u.h.ctx, hashFor(pack)))
	if failed != 0 {
		t.Fatalf("%d files failed to place", failed)
	}
	sort.Slice(placed, func(i, j int) bool { return placed[i].Episode < placed[j].Episode })
	if want := []series.EpisodeRef{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}; !reflect.DeepEqual(placed, want) {
		t.Fatalf("replaced %v, want only E01 and E02 (E03 is as good, E04 is held)", placed)
	}

	// Next sweep, once the grab is closed out: the pack has been tried, so it isn't grabbed
	// again for the episodes it didn't beat.
	u.exec(t, `UPDATE grabs SET status = ?`, grabStatusImported)
	// E03 and E04 now look beatable (half the season), so only the guard holds it back.
	u.exec(t, `UPDATE episodes SET source_release = 'Show.S01E03.720p.WEB-DL.x264-OLD' WHERE season_number = 1 AND episode_number = 3`)
	u.exec(t, `UPDATE episodes SET upgrade_hold = 0 WHERE season_number = 1 AND episode_number = 4`)
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := u.h.c.db.QueryRow(`SELECT COUNT(*) FROM grabs WHERE title = ?`, pack).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the pack was grabbed %d times, want once", n)
	}
}

// A pack that would downgrade any episode, or upgrade fewer than half of the season, isn't
// grabbed; the season's single-episode upgrades still are.
func TestUpgradeSeasonPackRefused(t *testing.T) {
	t.Run("downgrade", func(t *testing.T) {
		u := newUpgradeShow(t, false)
		u.addEpisode(t, 1, 1, 1, "/lib/e1.mkv", "Show.S01E01.480p.WEB-DL.x264-OLD", 1<<29, false)
		u.addEpisode(t, 1, 2, 2, "/lib/e2.mkv", "Show.S01E02.480p.WEB-DL.x264-OLD", 1<<29, false)
		u.addEpisode(t, 1, 3, 3, "/lib/e3.mkv", "Show.S01E03.1080p.WEB-DL.x264-OLD", 1<<30, false)
		u.h.ix.offer("Show.S01.720p.BluRay.x264-GRP")
		if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
			t.Fatal(err)
		}
		if got := u.grabbedTitles(t); len(got) != 0 {
			t.Fatalf("grabbed %v, want nothing — the pack is 720p and S01E03 is 1080p", got)
		}
	})
	t.Run("minority", func(t *testing.T) {
		u := newUpgradeShow(t, false)
		u.addEpisode(t, 1, 1, 1, "/lib/e1.mkv", "Show.S01E01.720p.WEB-DL.x264-OLD", 1<<30, false)
		for e := 2; e <= 4; e++ {
			u.addEpisode(t, 1, e, e, fmt.Sprintf("/lib/e%d.mkv", e), fmt.Sprintf("Show.S01E%02d.1080p.BluRay.x264-GOOD", e), 6<<30, false)
		}
		u.h.ix.offer("Show.S01.1080p.BluRay.x264-GRP", "Show.S01E01.1080p.WEB-DL.x264-NEW")
		if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
			t.Fatal(err)
		}
		if got := u.grabbedTitles(t); !reflect.DeepEqual(got, []string{"Show.S01E01.1080p.WEB-DL.x264-NEW"}) {
			t.Fatalf("grabbed %v, want only the single-episode upgrade (the pack beats 1 of 4)", got)
		}
	})
	t.Run("season still airing", func(t *testing.T) {
		u := newUpgradeShow(t, false)
		u.addEpisode(t, 1, 1, 1, "/lib/e1.mkv", "Show.S01E01.720p.WEB-DL.x264-OLD", 1<<30, false)
		u.addEpisode(t, 1, 2, 2, "/lib/e2.mkv", "Show.S01E02.720p.WEB-DL.x264-OLD", 1<<30, false)
		u.exec(t, `INSERT INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, 1, 3, '2099-01-01', 1)`, u.id)
		u.h.ix.offer("Show.S01.1080p.BluRay.x264-GRP")
		if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
			t.Fatal(err)
		}
		if got := u.grabbedTitles(t); len(got) != 0 {
			t.Fatalf("grabbed %v, want nothing while the season is still airing", got)
		}
	})
}

// An anime episode released under its absolute number upgrades its TMDB episode.
func TestUpgradeResolvesAnimeAbsolute(t *testing.T) {
	u := newUpgradeShow(t, true)
	for e := 1; e <= 12; e++ {
		u.addEpisode(t, 1, e, e, fmt.Sprintf("/lib/s1e%d.mkv", e), fmt.Sprintf("[Grp] Show - %02d [1080p]", e), 1<<30, false)
	}
	for e := 1; e <= 12; e++ {
		u.addEpisode(t, 2, e, 12+e, fmt.Sprintf("/lib/s2e%d.mkv", e), fmt.Sprintf("[Grp] Show - %02d [1080p]", 12+e), 1<<30, false)
	}
	u.exec(t, `UPDATE episodes SET source_release = '[Grp] Show - 17 [720p]' WHERE series_id = ? AND season_number = 2 AND episode_number = 5`, u.id)
	const abs = "[SubsPlease] Show - 17 (1080p) [ABCD1234]"
	// A batch holding the episode too is a pack in all but name: not one episode's upgrade.
	u.h.ix.offer(abs, "[SubsPlease] Show - 18 (1080p) [ABCD1234]", "[Grp] Show - 13-24 (BD 1080p) [Batch]")
	if err := u.h.c.upgradeSeries(u.h.ctx, u.id, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.grabbedTitles(t); !reflect.DeepEqual(got, []string{abs}) {
		t.Fatalf("grabbed %v, want %s for S02E05", got, abs)
	}
}

// The cursor lives on the show and moves only when the seasons don't fit in one sweep.
func TestUpgradeSeasonQueriesAdvanceCursor(t *testing.T) {
	u := newUpgradeShow(t, false)
	s := series.Series{ID: u.id}
	ctx := context.Background()
	all := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := u.h.c.upgradeSeasonQueries(ctx, s, all); !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("first sweep = %v", got)
	}
	if got := u.h.c.upgradeSeasonQueries(ctx, s, all); !reflect.DeepEqual(got, []int{5, 6, 7, 8}) {
		t.Fatalf("second sweep = %v", got)
	}
	if got := u.h.c.upgradeSeasonQueries(ctx, s, all); !reflect.DeepEqual(got, []int{9, 10, 1, 2}) {
		t.Fatalf("third sweep = %v", got)
	}
}
