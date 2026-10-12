package subtitles

import (
	"context"
	"errors"
	"testing"
)

// statusProvider is a fakeProvider that reports a provider state.
type statusProvider struct {
	*fakeProvider
	state string
}

func (p statusProvider) Status() ProviderStatus {
	return ProviderStatus{State: p.state, Detail: "OpenSubtitles login rejected (401)", Allowed: -1}
}

// The interactive search lists every candidate ranked as the download rung would, flags
// the blocklisted and rejected ones instead of hiding them, says which file is in use,
// and reuses its answer until a refresh — searches cost rate limit.
func TestSearchHandlerScoresAndFlagsBlocklisted(t *testing.T) {
	prov := &fakeProvider{canDownload: true, results: map[string][]SubtitleResult{"en": {
		{FileID: "weak", Release: "Movie.2020.720p.WEBRip.x264-OTHER", Downloads: 900},
		{FileID: "hash", Release: "Movie.2020.1080p.BluRay.x264-GRP", HashMatch: true, Downloads: 10},
		{FileID: "blocked", Release: "Movie.2020.1080p.BluRay.x264-GRP", Downloads: 50},
		{FileID: "machine", Release: "Movie.2020.1080p.BluRay.x264-GRP", AITranslated: true},
	}}}
	s, video := ladderFixture(t, "en", englishAudio(), prov, &fakeAI{})
	ctx := context.Background()
	if err := s.ledger.Block(ctx, BlockEntry{Provider: providerOpenSubtitles, FileID: "blocked", MediaKey: "m:1", Lang: "en", Reason: BlockRedo}); err != nil {
		t.Fatal(err)
	}
	s.blocklist = s.ledgerBlocklist
	if _, err := s.ledger.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: video,
		Path: sidecarPath(video, "en"), Lang: "en", Source: SourceOpenSubtitles, Provider: providerOpenSubtitles, ProviderFileID: "weak"}); err != nil {
		t.Fatal(err)
	}

	v, err := s.SearchCandidates(ctx, movieRef(1), "eng", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Lang != "en" || len(v.Candidates) != 4 || v.MinScore != defaultMinScore || !v.CanDownload || v.CurrentFileID != "weak" {
		t.Fatalf("view = %+v", v)
	}
	got := map[string]ScoredCandidate{}
	for _, c := range v.Candidates {
		got[c.FileID] = c
	}
	if v.Candidates[0].FileID != "hash" || got["hash"].Score != hashMatchScore || len(got["hash"].Reasons) == 0 {
		t.Errorf("first = %+v, want the hash match at 100 with its reason", v.Candidates[0])
	}
	if !got["blocked"].Blocklisted || got["machine"].Rejected == "" || got["weak"].Blocklisted {
		t.Errorf("flags: blocked=%+v machine=%+v weak=%+v", got["blocked"], got["machine"], got["weak"])
	}
	// Unusable ones go last, whatever their score.
	if last := v.Candidates[3]; last.usable() || v.Candidates[2].usable() {
		t.Errorf("order = %v", candidateIDs(v.Candidates))
	}

	// Cached: no second request until a refresh.
	if _, err := s.SearchCandidates(ctx, movieRef(1), "en", "", false); err != nil || prov.searches != 1 {
		t.Errorf("second search: err %v, searches %d, want the cached answer", err, prov.searches)
	}
	if _, err := s.SearchCandidates(ctx, movieRef(1), "en", "", true); err != nil || prov.searches != 2 {
		t.Errorf("refresh: err %v, searches %d", err, prov.searches)
	}

	for _, bad := range [][2]string{{"klingonese", ""}, {"en", "director"}} {
		if _, err := s.SearchCandidates(ctx, movieRef(1), bad[0], bad[1], false); !errors.Is(err, ErrBadWant) {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
}

func candidateIDs(cs []ScoredCandidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.FileID)
	}
	return out
}

// No search while OpenSubtitles has no key or a rejected login; a spent download quota
// doesn't stop one (searching is free).
func TestSearchRefusedWhenProviderUnavailable(t *testing.T) {
	prov := &fakeProvider{results: map[string][]SubtitleResult{}}
	s, _ := ladderFixture(t, "en", englishAudio(), statusProvider{prov, ProviderAuthFailed}, &fakeAI{})
	if _, err := s.SearchCandidates(context.Background(), movieRef(1), "en", "", false); !errors.Is(err, ErrSearchUnavailable) {
		t.Errorf("auth failed: err = %v", err)
	}
	s.provider = statusProvider{prov, ProviderNotConfigured}
	if _, err := s.SearchCandidates(context.Background(), movieRef(1), "en", "", false); !errors.Is(err, ErrSearchUnavailable) {
		t.Errorf("not configured: err = %v", err)
	}
	if prov.searches != 0 {
		t.Errorf("searched %d times while unavailable", prov.searches)
	}
	s.provider = statusProvider{prov, ProviderQuotaSpent} // a spent quota still searches
	if _, err := s.SearchCandidates(context.Background(), movieRef(1), "en", "forced", false); err != nil || prov.searches != 1 {
		t.Errorf("quota spent: err %v, searches %d", err, prov.searches)
	}
}
