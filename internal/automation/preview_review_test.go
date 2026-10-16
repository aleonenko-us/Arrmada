package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// endedMeta knows an ended four-season show, so a complete-series pack can win a preview.
type endedMeta struct{ previewMeta }

func (endedMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	if id != 1399 {
		return nil, errors.New("unknown show")
	}
	eps := func(n int) []metadata.EpisodeDetails {
		var out []metadata.EpisodeDetails
		for i := 1; i <= n; i++ {
			out = append(out, metadata.EpisodeDetails{EpisodeNumber: i, AirDate: "2015-01-01", Runtime: 50})
		}
		return out
	}
	return &metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: id, Title: "Endshow", Year: 2011}, Status: "Ended",
		Seasons: []metadata.SeasonDetails{
			{SeasonNumber: 1, Episodes: eps(8)}, {SeasonNumber: 2, Episodes: eps(8)},
			{SeasonNumber: 3, Episodes: eps(8)}, {SeasonNumber: 4, Episodes: eps(8)},
		}}, nil
}

func newEndedHarness(t *testing.T) *stallHarness {
	t.Helper()
	h := newStallHarness(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.c.movies = movies.NewService(h.c.db, endedMeta{}, nil, t.TempDir(), "", nil, log)
	h.c.series = series.NewService(h.c.db, endedMeta{}, t.TempDir(), log)
	return h
}

// A request for S1–S4 of an ended show previews the complete pack. Approved trimmed to S1,
// the grab re-reads what the show wants: four seasons for one isn't proportionate, so the
// pack is skipped and the search that follows takes the season-1 pack instead.
func TestGrabPreviewedTrimmedSkipsCompletePack(t *testing.T) {
	h := newEndedHarness(t)
	complete, s1 := "Endshow.Complete.Series.S01-S04.1080p.WEB-DL.x264-GRP", "Endshow.S01.1080p.WEB-DL.x264-GRP"
	h.ix.offer(complete, s1)
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: 1399, Seasons: []int{1, 2, 3, 4}, Key: "request:9"})
	if err != nil || p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Title != complete {
		t.Fatalf("preview = %+v, %v; want the complete pack", p, err)
	}
	off := false
	sr, err := h.c.series.AddWith(h.ctx, 1399, "", series.AddOptions{Monitored: true, Seasons: map[int]bool{1: true}, MonitorNewSeasons: &off})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.GrabPreviewed(h.ctx, ReleaseKindSeries, sr.ID, p.Tokens, "request:9"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(h.qbit.callLog(), "\n")
	if strings.Contains(calls, hashFor(complete)) {
		t.Fatalf("a trimmed-to-S1 approval grabbed the complete-series pack: %s", calls)
	}
	if !strings.Contains(calls, hashFor(s1)) {
		t.Fatalf("the follow-up search didn't take the season pack: %s", calls)
	}
}

// What's already on its way isn't grabbed again: a second Approve & grab with the same
// preview finds season 1 in flight and grabs nothing.
func TestGrabPreviewedSkipsSeasonsInFlight(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Severance.S01.1080p.WEB-DL.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: 95396, Seasons: []int{1}, Key: "request:4"})
	if err != nil || p.Outcome != PreviewFound {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	off := false
	sr, err := h.c.series.AddWith(h.ctx, 95396, "", series.AddOptions{Monitored: true, Seasons: map[int]bool{1: true}, MonitorNewSeasons: &off})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindSeries, sr.ID, p.Tokens, "request:4"); err != nil || out.Grabbed != 1 {
		t.Fatalf("first grab = %+v, %v", out, err)
	}
	titles, err := h.c.tryPreviewedSeries(h.ctx, sr.ID, mustRefs(t, h, p.Tokens, "request:4", ReleaseKindSeries))
	if err != nil || len(titles) != 0 {
		t.Fatalf("grabbed %v again while season 1 is in flight (%v)", titles, err)
	}
	if n := len(h.qbit.callLog()); n != 1 {
		t.Fatalf("%d adds", n)
	}
}

func mustRefs(t *testing.T, h *stallHarness, tokens []string, key, kind string) []ReleaseRef {
	t.Helper()
	refs, err := h.c.previewTokens().resolve(tokens, key, kind)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

// A film already in the library is ranked under its own track's profile, whatever the
// request carries — and SearchProfile says so, for Approve & grab to compare with.
func TestPreviewLibraryTitleUsesItsOwnProfile(t *testing.T) {
	h := newPreviewHarness(t)
	own, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "Own"})
	if err != nil {
		t.Fatal(err)
	}
	ownRef := "custom:" + strconv.FormatInt(own.ID, 10)
	m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 329865, Title: "Arrival", Year: 2016, Monitored: true, QualityProfile: ownRef})
	if err != nil {
		t.Fatal(err)
	}
	asked := h.c.quality.DefaultProfile(h.ctx, quality.MediaMovie)
	if asked == ownRef {
		t.Fatal("test needs two profiles")
	}
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	in := PreviewInput{MediaType: "movie", TMDBID: 329865, Profile: asked, Key: "request:1"}
	p, err := h.c.PreviewTitle(h.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !p.InLibrary || p.Profile != ownRef {
		t.Fatalf("preview of library film %d ranked under %q (in library %v), want its own %q", m.ID, p.Profile, p.InLibrary, ownRef)
	}
	if got := h.c.SearchProfile(h.ctx, in); got != ownRef {
		t.Fatalf("SearchProfile = %q, want %q", got, ownRef)
	}
	// A film not in the library is ranked under what was asked.
	if got := h.c.SearchProfile(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 1, Profile: asked}); got != asked {
		t.Fatalf("SearchProfile of a new film = %q, want %q", got, asked)
	}
}

// A film wanting several tracks is shown but not offered to grab, and Approve & grab with
// an older preview's token searches instead.
func TestPreviewSeveralVersionsNotGrabbed(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	early, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil || len(early.Tokens) != 1 {
		t.Fatalf("preview = %+v, %v", early, err)
	}
	mid := h.addMovie(t, 329865, "Arrival", 2016)
	if _, err := h.c.movies.AddVersion(h.ctx, mid, "4K", "", "", true); err != nil {
		t.Fatal(err)
	}
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil || p.Outcome != PreviewFound || len(p.Tokens) != 0 || p.Note == "" {
		t.Fatalf("preview with two wanted tracks = %+v, %v", p, err)
	}
	before := h.ix.searchCount()
	if _, err := h.c.GrabPreviewed(h.ctx, ReleaseKindMovie, mid, early.Tokens, "request:1"); err != nil {
		t.Fatal(err)
	}
	if h.ix.searchCount() == before {
		t.Fatal("Approve & grab for a film with two wanted tracks didn't search")
	}
}

// After grabbing a book's previewed ebook, the audiobook the book also wants is searched
// right away, not left to the sweep.
func TestGrabPreviewedBookSearchesTheOtherEdition(t *testing.T) {
	h := newPreviewHarness(t)
	h.c.books = books.NewService(h.c.db, nil, h.c.log)
	sp, err := h.c.quality.Create(h.ctx, quality.StoredProfile{MediaType: quality.MediaBook, Name: "Both", FormatScores: map[string]int{"EPUB": 40, "M4B": 40}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "custom:" + strconv.FormatInt(sp.ID, 10)
	b, err := books.NewRepo(h.c.db).Create(h.ctx, books.Book{OLKey: "OL1W", Title: "Dune", Author: "Frank Herbert", Monitored: true, QualityProfile: ref})
	if err != nil {
		t.Fatal(err)
	}
	h.ix.offer("Frank Herbert - Dune [EPUB]")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "book", BookID: b.ID, Key: "request:5"})
	if err != nil || p.Outcome != PreviewFound || len(p.Tokens) != 1 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	h.ix.offer("Frank Herbert - Dune [EPUB]", "Frank Herbert - Dune [M4B]")
	out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindBook, b.ID, p.Tokens, "request:5")
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(h.qbit.callLog(), "\n")
	if out.Grabbed != 2 || !strings.Contains(calls, hashFor("Frank Herbert - Dune [EPUB]")) || !strings.Contains(calls, hashFor("Frank Herbert - Dune [M4B]")) {
		t.Fatalf("outcome %+v, client calls %s", out, calls)
	}
}

// A preview's failures show on the indexer's row but never pause it: three failed previews
// leave background searches free to ask it, where three failed real searches pause it.
func TestPreviewFailuresDontPauseIndexers(t *testing.T) {
	h := newPreviewHarness(t)
	tr := connstatus.New(h.c.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.c.indexers.SetStatus(tr)
	list, err := h.c.indexers.List(h.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("indexers = %v, %v", list, err)
	}
	ref := strconv.FormatInt(list[0].ID, 10)
	h.ix.mu.Lock()
	h.ix.down = true
	h.ix.mu.Unlock()
	for i := 0; i < 3; i++ {
		p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
		if err != nil || p.Outcome != PreviewUnavailable || len(p.IndexerIssues) == 0 {
			t.Fatalf("preview %d = %+v, %v", i, p, err)
		}
	}
	st, _ := tr.Get(connstatus.KindIndexer, ref)
	if st.LastError == "" {
		t.Fatal("the preview's failure isn't on the indexer's row")
	}
	if ok, _ := tr.Allow(connstatus.KindIndexer, ref, false); !ok {
		t.Fatalf("previews paused the indexer: %+v", st)
	}
}
