package automation

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The movie and series candidate builders attach the title's original language, so the
// Unwanted pack can tell a foreign-only dub; an unknown one stays "" (no judgement).
func TestCandidateBuildersAttachOriginalLanguage(t *testing.T) {
	m := movies.Movie{Runtime: 120, Extra: &movies.MovieExtra{OriginalLanguage: "ja"}}
	cands := tagMovie([]quality.Candidate{quality.NewCandidate("Film.2020.FRENCH.1080p.WEB-DL-GRP", 4, 10)}, m)
	if cands[0].OrigLanguage != "ja" || cands[0].RuntimeMin != 120 {
		t.Fatalf("movie candidate = %+v", cands[0])
	}
	if got := tagMovie([]quality.Candidate{quality.NewCandidate("Film.2020.1080p.WEB-DL-GRP", 4, 10)}, movies.Movie{}); got[0].OrigLanguage != "" {
		t.Fatalf("no metadata: %q", got[0].OrigLanguage)
	}

	s := runtimeShow()
	s.Extra = &series.SeriesExtra{OriginalLanguage: "ko"}
	c := &Coordinator{}
	cand := c.newSeriesCandidate(context.Background(), s, newRuntimeIndex(s), indexer.Release{Title: "Show.S01E01.1080p.WEB-DL-GRP", SizeBytes: 1 << 30, Seeders: 5})
	if cand.OrigLanguage != "ko" || cand.RuntimeMin != 45 {
		t.Fatalf("series candidate = %+v", cand)
	}
	s.Extra = nil
	if cand := c.newSeriesCandidate(context.Background(), s, newRuntimeIndex(s), indexer.Release{Title: "Show.S01E01.1080p.WEB-DL-GRP"}); cand.OrigLanguage != "" {
		t.Fatalf("no metadata: %q", cand.OrigLanguage)
	}
}
