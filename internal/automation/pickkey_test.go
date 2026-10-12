package automation

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// decidedOf reads what a grab recorded as its deciding key and mode.
func decidedOf(t *testing.T, h *stallHarness, title string) (string, string) {
	t.Helper()
	var by, mode string
	if err := h.c.db.QueryRow(`SELECT decided_by, decided_mode FROM grabs WHERE title = ?`, title).Scan(&by, &mode); err != nil {
		t.Fatalf("grab %q: %v", title, err)
	}
	return by, mode
}

// A sweep's pick records what decided it, on the acquisition record; classic ranking
// decides by default.
func TestMoviePickRecordsItsDecidingKey(t *testing.T) {
	h := newStallHarness(t)
	if _, err := h.c.db.Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability)
		VALUES (1, 1, 'Alpha', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	const uhd = "Alpha.2001.2160p.WEB-DL.H.265-GRP"
	h.ix.offer(uhd, "Alpha.2001.1080p.WEB-DL.x264-GRP")
	h.c.SearchMissing(h.ctx)
	if h.adds() != 1 {
		t.Fatalf("adds = %d", h.adds())
	}
	if by, mode := decidedOf(t, h, uhd); by != string(quality.KeyResolution) || mode != quality.RankingV1 {
		t.Fatalf("decided by %q (%q), want resolution (v1)", by, mode)
	}
	acqs, err := h.c.Active(h.ctx, "movie", 1)
	if err != nil || len(acqs) != 1 || acqs[0].DecidedBy != "resolution" || acqs[0].DecidedMode != "v1" {
		t.Fatalf("acquisition = %+v (%v)", acqs, err)
	}
}

// A lone release is recorded as the only one; under v2 the mode says so.
func TestLonePickRecordsOnlyUnderV2(t *testing.T) {
	h := newStallHarness(t)
	// The app's one settings service, switched to v2 the way Settings would.
	set := settings.NewService(h.c.db)
	if err := set.Set(h.ctx, quality.KeyRanking, quality.RankingV2); err != nil {
		t.Fatal(err)
	}
	h.c.quality = quality.NewServiceWith(h.c.db, set)
	if _, err := h.c.db.Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability)
		VALUES (1, 1, 'Alpha', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	const only = "Alpha.2001.1080p.WEB-DL.x264-GRP"
	h.ix.offer(only)
	h.c.SearchMissing(h.ctx)
	if by, mode := decidedOf(t, h, only); by != string(quality.KeyOnly) || mode != quality.RankingV2 {
		t.Fatalf("decided by %q (%q), want only (v2)", by, mode)
	}
}

// A series sweep's pick records its key too.
func TestSeriesPickRecordsItsDecidingKey(t *testing.T) {
	h := newStallHarness(t)
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 9, Title: "Show", Year: 2020, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2021-01-01", Monitored: true},
	}}}); err != nil {
		t.Fatal(err)
	}
	const best = "Show.S01E01.1080p.BluRay.x264-AAA"
	h.ix.offer(best, "Show.S01E01.1080p.HDTV.x264-BBB")
	h.c.SearchSeriesMissing(h.ctx)
	if by, mode := decidedOf(t, h, best); by != string(quality.KeySource) || mode != quality.RankingV1 {
		t.Fatalf("decided by %q (%q), want source (v1)", by, mode)
	}
}

// A grab no ranking chose records nothing, and the notes are nil-safe.
func TestPickKeyNotes(t *testing.T) {
	if by, mode := pickOf(context.Background(), "x"); by != "" || mode != "" {
		t.Fatal("no collector: no key")
	}
	var n *searchNotes
	n.picked("x", quality.KeyResolution, quality.RankingV1)
	n.notePick(quality.Decision{})
	ctx, notes := newSearchNotes(context.Background())
	notes.picked("Film.2021.1080p.WEB-DL-GRP", quality.KeyBitrate, decidedUpgrade)
	if by, mode := pickOf(ctx, "Film.2021.1080p.WEB-DL-GRP"); by != "bitrate" || mode != "upgrade" {
		t.Fatalf("got %q %q", by, mode)
	}
	if by, _ := pickOf(ctx, "something else"); by != "" {
		t.Fatalf("an unpicked release has a key: %q", by)
	}
}
