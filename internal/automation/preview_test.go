package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// previewMeta is a metadata provider that knows one film and one show, for previews of
// titles not in the library yet.
type previewMeta struct{}

func (previewMeta) Available() bool { return true }
func (previewMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (previewMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("none")
}
func (previewMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	if id != 329865 {
		return nil, errors.New("unknown film")
	}
	return &metadata.MovieDetails{MovieResult: metadata.MovieResult{TMDBID: id, Title: "Arrival", Year: 2016},
		Runtime: 116, Status: "Released", ReleaseDate: "2016-11-11"}, nil
}
func (previewMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (previewMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	if id != 95396 {
		return nil, errors.New("unknown show")
	}
	eps := func(n int, aired string) []metadata.EpisodeDetails {
		var out []metadata.EpisodeDetails
		for i := 1; i <= n; i++ {
			out = append(out, metadata.EpisodeDetails{EpisodeNumber: i, AirDate: aired, Runtime: 50})
		}
		return out
	}
	return &metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: id, Title: "Severance", Year: 2022}, Status: "Returning Series",
		Seasons: []metadata.SeasonDetails{
			{SeasonNumber: 1, Episodes: eps(9, "2022-02-18")},
			{SeasonNumber: 2, Episodes: eps(10, "2025-01-17")},
			{SeasonNumber: 3, Episodes: eps(8, "2025-06-01")},
		}}, nil
}

// newPreviewHarness is the stall harness whose movies and series services can draft a title
// from previewMeta.
func newPreviewHarness(t *testing.T) *stallHarness {
	t.Helper()
	h := newStallHarness(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.c.movies = movies.NewService(h.c.db, previewMeta{}, nil, t.TempDir(), "", nil, log)
	h.c.series = series.NewService(h.c.db, previewMeta{}, t.TempDir(), log)
	return h
}

func previewRows(t *testing.T, h *stallHarness, table string) int {
	t.Helper()
	var n int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A preview of a film not in the library finds what a search would grab — and grabs
// nothing, adds nothing to the library and stores no search attempt.
func TestPreviewMovieIsADryRun(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP", "Arrival.2016.720p.WEB-DL.x264-GRP", "Inception.2010.1080p.BluRay.x264-GRP")

	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || p.Reason != ReasonGrabbed || len(p.Releases) != 1 || len(p.Tokens) != 1 {
		t.Fatalf("preview = %+v", p)
	}
	rel := p.Releases[0]
	if rel.Title != "Arrival.2016.1080p.BluRay.x264-GRP" || rel.Indexer != "Fake" || rel.Resolution != "1080p" || rel.Seeders != 25 || rel.SizeGB <= 0 || rel.Transport != "torrent" {
		t.Fatalf("release = %+v", rel)
	}
	if p.Returned != 3 || p.Matching != 2 || p.Queries != 1 || p.Searched != 1 {
		t.Fatalf("counts = returned %d matching %d queries %d searched %d", p.Returned, p.Matching, p.Queries, p.Searched)
	}
	// The fake counts every request, a capabilities read included: a preview costs what
	// exactly one movie search costs.
	before := h.ix.searchCount()
	if _, err := h.c.search(h.ctx, indexer.SearchQuery{Text: "Arrival 2016", MediaType: indexer.MediaMovie, Limit: 100}); err != nil {
		t.Fatal(err)
	}
	one := h.ix.searchCount() - before
	before = h.ix.searchCount()
	if _, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"}); err != nil {
		t.Fatal(err)
	}
	if n := h.ix.searchCount() - before; n != one {
		t.Fatalf("a preview made %d indexer requests; one search makes %d", n, one)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 {
		t.Fatalf("a preview reached the download client: %v", calls)
	}
	for _, table := range []string{"grabs", "search_attempts", "movies"} {
		if n := previewRows(t, h, table); n != 0 {
			t.Fatalf("a preview wrote %d %s rows", n, table)
		}
	}
	// The token is spendable only as a preview's: never at a grab endpoint.
	if _, err := h.c.ResolveReleaseToken(p.Tokens[0], ReleaseKindMovie, 0, 0); !errors.Is(err, ErrReleaseExpired) {
		t.Fatalf("a preview token resolved as an interactive one: %v", err)
	}
	if !h.c.PreviewTokensLive(p.Tokens, "request:1", ReleaseKindMovie) || h.c.PreviewTokensLive(p.Tokens, "request:2", ReleaseKindMovie) {
		t.Fatal("preview tokens must be live for their own request only")
	}
}

// Nothing fit: the preview says so with the reasons, by the shared reason codes.
func TestPreviewMovieNothingFound(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Inception.2010.1080p.BluRay.x264-GRP", "Tenet.2020.1080p.WEB-DL.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewNone || p.Reason != ReasonNoneForTitle || len(p.Releases) != 0 || len(p.Tokens) != 0 {
		t.Fatalf("preview = %+v", p)
	}
	if len(p.TopReasons) != 1 || p.TopReasons[0].Code != DropWrongTitle || p.TopReasons[0].Count != 2 || p.TopReasons[0].Label == "" {
		t.Fatalf("top reasons = %+v", p.TopReasons)
	}
}

// A preview is background work: an indexer backing off is left alone even when the
// caller's context says a person is searching, and the preview says indexers are
// unavailable rather than that nothing was found.
func TestPreviewLeavesPausedIndexersAlone(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	tr := connstatus.New(h.c.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.c.indexers.SetStatus(tr)
	list, err := h.c.indexers.List(h.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("indexers = %v, %v", list, err)
	}
	for i := 0; i < 3; i++ {
		tr.Record(connstatus.KindIndexer, strconv.FormatInt(list[0].ID, 10), connstatus.Outcome{Err: errors.New("timeout"), Backoff: true})
	}
	p, err := h.c.PreviewTitle(indexer.WithInteractive(h.ctx), PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewUnavailable || p.Reason != ReasonIndexersPaused {
		t.Fatalf("preview = %+v", p)
	}
	if n := h.ix.searchCount(); n != 0 {
		t.Fatalf("a paused indexer was asked %d times by a preview", n)
	}
	if len(p.IndexerIssues) != 1 || !p.IndexerIssues[0].Skipped {
		t.Fatalf("issues = %+v", p.IndexerIssues)
	}
}

// A library film being searched for real isn't previewed alongside, and one already
// downloading or on disk isn't searched at all.
func TestPreviewRespectsTheSearchClaim(t *testing.T) {
	h := newPreviewHarness(t)
	mid := h.addMovie(t, 329865, "Arrival", 2016)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	release, ok := h.c.claims.claim(movieKey(mid))
	if !ok {
		t.Fatal("claim")
	}
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	release()
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewBusy || p.Reason != ReasonAlreadySearching || h.ix.searchCount() != 0 {
		t.Fatalf("preview = %+v, searches = %d", p, h.ix.searchCount())
	}
	// With the claim free it previews; the claim is never taken by the preview itself, so
	// a real search is never turned away by one.
	if _, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"}); err != nil {
		t.Fatal(err)
	}
	if h.c.claims.claimed(movieKey(mid)) {
		t.Fatal("the preview left the title claimed")
	}
}

// A library film's search state — the missing-sweep's backoff — is untouched by a preview
// that finds nothing, and no attempt is stored for it.
func TestPreviewLeavesBackoffAlone(t *testing.T) {
	h := newPreviewHarness(t)
	mid := h.addMovie(t, 329865, "Arrival", 2016)
	lastBefore, missesBefore := h.c.movies.SearchState(h.ctx, mid)
	h.ix.offer("Tenet.2020.1080p.WEB-DL.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:1"})
	if err != nil || p.Outcome != PreviewNone {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if last, misses := h.c.movies.SearchState(h.ctx, mid); last != lastBefore || misses != missesBefore {
		t.Fatalf("search state moved: %q/%d → %q/%d", lastBefore, missesBefore, last, misses)
	}
	if n := attemptCount(t, h, AttemptMovie, mid); n != 0 {
		t.Fatalf("a preview stored %d search attempts", n)
	}
}

// Approve & grab: the previewed release is grabbed by its token, without searching again.
// Once the token has expired, it searches the ordinary way and grabs what that picks.
func TestGrabPreviewedMovie(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:7"})
	if err != nil || p.Outcome != PreviewFound {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	// The indexer now offers something better; the approver chose the previewed one.
	h.ix.offer("Arrival.2016.2160p.WEB-DL.x265-GRP", "Arrival.2016.1080p.BluRay.x264-GRP")
	before := h.ix.searchCount()
	mid := h.addMovie(t, 329865, "Arrival", 2016) // what approving did
	out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindMovie, mid, p.Tokens, "request:7")
	if err != nil {
		t.Fatal(err)
	}
	if out.Grabbed != 1 || out.GrabbedTitles[0] != "Arrival.2016.1080p.BluRay.x264-GRP" {
		t.Fatalf("outcome = %+v", out)
	}
	if h.ix.searchCount() != before {
		t.Fatal("grabbing the previewed release searched again")
	}
	calls := h.qbit.callLog()
	if len(calls) != 1 || !strings.Contains(calls[0], hashFor("Arrival.2016.1080p.BluRay.x264-GRP")) {
		t.Fatalf("client calls = %v", calls)
	}
	if n := previewRows(t, h, "grabs"); n != 1 {
		t.Fatalf("%d grab rows", n)
	}
	// Grabbing again finds it already on its way: no second copy.
	if out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindMovie, mid, p.Tokens, "request:7"); err != nil || out.Grabbed != 0 {
		t.Fatalf("second grab = %+v, %v", out, err)
	}
	if n := len(h.qbit.callLog()); n != 1 {
		t.Fatalf("%d adds after a second Approve & grab", n)
	}
}

func TestGrabPreviewedFallsBackWhenExpired(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Arrival.2016.1080p.BluRay.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "movie", TMDBID: 329865, Key: "request:7"})
	if err != nil || len(p.Tokens) != 1 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	store := h.c.previewTokens()
	store.mu.Lock()
	store.now = func() time.Time { return time.Now().Add(previewTokenTTL + time.Minute) }
	store.mu.Unlock()
	if h.c.PreviewTokensLive(p.Tokens, "request:7", ReleaseKindMovie) {
		t.Fatal("an expired preview token is still live")
	}
	h.ix.offer("Arrival.2016.2160p.WEB-DL.x265-GRP")
	mid := h.addMovie(t, 329865, "Arrival", 2016)
	before := h.ix.searchCount()
	out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindMovie, mid, p.Tokens, "request:7")
	if err != nil {
		t.Fatal(err)
	}
	if h.ix.searchCount() == before || out.Grabbed != 1 || out.GrabbedTitles[0] != "Arrival.2016.2160p.WEB-DL.x265-GRP" {
		t.Fatalf("fallback = %+v (searches %d → %d)", out, before, h.ix.searchCount())
	}
}

// A show's preview covers the first season asked for, deciding a season pack against
// single episodes as the sweep does, and says the rest weren't checked. Two searches: the
// broad one and the season's.
func TestPreviewSeriesFirstSeason(t *testing.T) {
	h := newPreviewHarness(t)
	h.ix.offer("Severance.S01.1080p.WEB-DL.x264-GRP", "Severance.S01E01.1080p.WEB-DL.x264-GRP", "Severance.S02E01.1080p.WEB-DL.x264-GRP")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: 95396, Seasons: []int{1, 2, 3}, Key: "request:3"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || p.Season != 1 || len(p.Releases) != 1 || len(p.Tokens) != 1 {
		t.Fatalf("preview = %+v", p)
	}
	if r := p.Releases[0]; r.Title != "Severance.S01.1080p.WEB-DL.x264-GRP" || r.Kind != "pack" || r.Covers != "S01" {
		t.Fatalf("release = %+v", r)
	}
	if p.SeasonsNote != "2 more seasons not checked" || p.Queries != 2 {
		t.Fatalf("note %q, queries %d", p.SeasonsNote, p.Queries)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 || previewRows(t, h, "series") != 0 {
		t.Fatalf("a series preview grabbed or added something: %v", calls)
	}
}

// Asked for one season with no pack on offer, the episodes are what a search would take.
func TestPreviewSeriesEpisodesWhenNoPack(t *testing.T) {
	h := newPreviewHarness(t)
	var offer []string
	for e := 1; e <= 9; e++ {
		offer = append(offer, "Severance.S01E0"+strconv.Itoa(e)+".1080p.WEB-DL.x264-GRP")
	}
	h.ix.offer(offer...)
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: 95396, Seasons: []int{1}, Key: "request:3"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 9 || p.Releases[0].Kind != "episode" || p.SeasonsNote != "" {
		t.Fatalf("preview = %+v", p)
	}
}

// Approve & grab for a show grabs the previewed pack, then searches for the rest of what
// was asked for with the pack held back as in flight: no second copy of season 1.
func TestGrabPreviewedSeries(t *testing.T) {
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
	h.ix.offer("Severance.S01.2160p.WEB-DL.x265-GRP", "Severance.S01E01.1080p.WEB-DL.x264-GRP")
	out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindSeries, sr.ID, p.Tokens, "request:4")
	if err != nil {
		t.Fatal(err)
	}
	calls := h.qbit.callLog()
	if out.Grabbed != 1 || len(calls) != 1 || !strings.Contains(calls[0], hashFor("Severance.S01.1080p.WEB-DL.x264-GRP")) {
		t.Fatalf("outcome = %+v, client calls = %v", out, calls)
	}
}

// A book not in the library yet is previewed by its title and author; the edition the
// profile prefers wins, and nothing is grabbed or added.
func TestPreviewBookNotInLibrary(t *testing.T) {
	h := newPreviewHarness(t)
	h.c.books = books.NewService(h.c.db, nil, h.c.log)
	h.ix.offer("Frank Herbert - Dune [PDF]", "Frank Herbert - Dune [EPUB]", "Isaac Asimov - Foundation [EPUB]")
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "book", Title: "Dune", Author: "Frank Herbert", Key: "request:5"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Format != "EPUB" || p.Releases[0].Kind != "ebook" || len(p.Tokens) != 1 {
		t.Fatalf("preview = %+v", p)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 || previewRows(t, h, "books") != 0 || previewRows(t, h, "search_attempts") != 0 {
		t.Fatalf("a book preview grabbed or stored something: %v", calls)
	}
}
