package subtitles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// Interactive search (SUB-28): the drawer's "Search OpenSubtitles" for one video and one
// language. It lists every candidate scored against the file, exactly as the download rung
// would rank them, with the blocklisted and rejected ones flagged rather than hidden — the
// owner is choosing. A search spends no download quota, but it does spend the API's rate
// limit, so the raw results are kept for a few minutes and only a deliberate refresh asks
// again.

var (
	// ErrSearchUnavailable: OpenSubtitles isn't set up, or its login was rejected.
	ErrSearchUnavailable = errors.New("OpenSubtitles isn't available")
	// ErrBadWant: a language or variant a subtitle can't be searched for.
	ErrBadWant = errors.New("not a language and variant to search for")
)

// searchCacheFor is how long a search's raw results are reused before asking again.
const searchCacheFor = 10 * time.Minute

// SearchView is POST /subtitles/search's answer.
type SearchView struct {
	Lang       string            `json:"lang"`
	Variant    string            `json:"variant,omitempty"`
	Candidates []ScoredCandidate `json:"candidates"` // best first; blocklisted and rejected ones flagged, last
	// CurrentFileID is the provider file the sidecar for this language came from, when it
	// was a download: the drawer marks it as the one in use.
	CurrentFileID  string `json:"current_file_id,omitempty"`
	MinScore       int    `json:"min_score"`
	CanDownload    bool   `json:"can_download"`    // an account is set: "Use this" can download
	QuotaRemaining int    `json:"quota_remaining"` // -1 = unknown
	QuotaResetAt   int64  `json:"quota_reset_at"`  // unix seconds; 0 = not paused
	ProviderState  string `json:"provider_state"`
	SearchedAt     int64  `json:"searched_at"` // when OpenSubtitles was last asked (a cached answer is older)
}

// searchCache keeps raw provider results per video, language and variant.
type searchCache struct {
	mu   sync.Mutex
	hits map[string]searchHit
}

type searchHit struct {
	at      time.Time
	path    string // the video they were found for; an upgrade makes them stale
	results []SubtitleResult
}

func (c *searchCache) get(key, path string, now time.Time) (searchHit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.hits[key]
	if !ok || h.path != path || now.Sub(h.at) > searchCacheFor {
		return searchHit{}, false
	}
	return h, true
}

func (c *searchCache) put(key string, h searchHit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hits == nil {
		c.hits = map[string]searchHit{}
	}
	// Bounded: drop the expired ones when it grows; a drawer session is a handful.
	if len(c.hits) > 200 {
		for k, v := range c.hits {
			if time.Since(v.at) > searchCacheFor {
				delete(c.hits, k)
			}
		}
	}
	c.hits[key] = h
}

// wantFor checks a language and variant from the client: a language the table knows, and
// full, SDH or forced.
func wantFor(lang, variant string) (string, string, error) {
	l, ok := langs.Lookup(strings.TrimSpace(lang))
	if !ok {
		return "", "", fmt.Errorf("%w: unknown language %q", ErrBadWant, lang)
	}
	switch v := strings.ToLower(strings.TrimSpace(variant)); v {
	case VariantFull, "full":
		return l.Code, VariantFull, nil
	case VariantSDH, VariantForced:
		return l.Code, v, nil
	}
	return "", "", fmt.Errorf("%w: unknown variant %q", ErrBadWant, variant)
}

// SearchCandidates searches OpenSubtitles for one video's language and scores every
// result against the file. refresh asks again even when a recent answer is kept. Refused
// while OpenSubtitles isn't configured or its login is rejected; a rate limit comes back
// as the provider's RateLimitError.
func (s *Service) SearchCandidates(ctx context.Context, r MediaRef, lang, variant string, refresh bool) (SearchView, error) {
	lang, variant, err := wantFor(lang, variant)
	if err != nil {
		return SearchView{}, err
	}
	ref, err := s.resolveMedia(ctx, r)
	if err != nil {
		return SearchView{}, err
	}
	st := s.providerStatus()
	if s.provider == nil || !s.provider.Available() || st.State == ProviderNotConfigured {
		return SearchView{}, fmt.Errorf("%w: add an OpenSubtitles API key first", ErrSearchUnavailable)
	}
	if st.State == ProviderAuthFailed {
		return SearchView{}, fmt.Errorf("%w: %s — check the credentials", ErrSearchUnavailable, st.Detail)
	}
	if mi, err := s.probeFile(ctx, ref.Path); err == nil && mi != nil {
		ref.FPS = mi.FPS
	}

	key := ref.MediaKey + "|" + lang + "|" + dbVariant(variant)
	now := time.Now()
	hit, cached := s.searches.get(key, ref.Path, now)
	if !cached || refresh {
		hash, herr := osHash(ref.Path)
		if herr != nil {
			s.log.Debug("subtitles: could not hash file for an interactive search", "path", ref.Path, "err", herr)
		}
		req := SearchRequest{IMDBID: ref.IMDB, Title: ref.Title, Year: ref.Year, Season: ref.Season, Episode: ref.Episode, Language: lang, MovieHash: hash}
		if variant == VariantForced {
			req.ForeignParts = "only"
		}
		results, err := s.provider.Search(ctx, req)
		if err != nil {
			return SearchView{}, err
		}
		hit = searchHit{at: now, path: ref.Path, results: results}
		s.searches.put(key, hit)
	}

	blocked, err := s.blockedFiles(ctx, ref.MediaKey)
	if err != nil {
		return SearchView{}, fmt.Errorf("read subtitle blocklist: %w", err)
	}
	m := matchContextFor(ref)
	m.Forced = variant == VariantForced
	v := SearchView{
		Lang: lang, Variant: variant, Candidates: rankCandidates(hit.results, m, blocked),
		MinScore: s.minScore(ctx), CanDownload: s.provider.CanDownload(), QuotaRemaining: -1,
		ProviderState: st.State, SearchedAt: hit.at.Unix(),
	}
	if q, ok := s.provider.(quotaReporter); ok {
		remaining, resetAt := q.Quota()
		v.QuotaRemaining = remaining
		if resetAt.After(now) {
			v.QuotaResetAt = resetAt.Unix()
		}
	}
	// The download in use for this language, if the sidecar came from one. A full want
	// may be covered by a hearing-impaired download, saved as .sdh.
	paths := []string{sidecarPathV(ref.Path, lang, variant)}
	if variant == VariantFull {
		paths = append(paths, sidecarPathV(ref.Path, lang, VariantSDH))
	}
	for _, p := range paths {
		if row, ok, err := s.ledger.LiveFile(ctx, p); err == nil && ok && row.ProviderFileID != "" {
			v.CurrentFileID = row.ProviderFileID
			break
		}
	}
	return v, nil
}
