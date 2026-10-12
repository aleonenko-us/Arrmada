package subtitles

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// Settings keys (stored in the shared settings service).
const (
	keyMoviesAuto = "subs_movies_auto"
	keySeriesAuto = "subs_series_auto"
	keyLanguages  = "subs_languages" // CSV of ISO 639-1 codes
)

// defaultLanguages is used until the admin configures otherwise.
var defaultLanguages = []string{"en"}

// Auto-ensure defaults ON. The point of the module is that a new download has its
// subtitles without anyone asking; a default that needed switching on meant the import
// hook and the sweep both existed and neither ever ran.
const (
	defaultMoviesAuto = true
	defaultSeriesAuto = true
)

// Service is the Subtitles module's logic: it derives per-title subtitle status from disk
// (sidecars) and grabs missing subtitles from the provider, saving them alongside media.
type Service struct {
	movies   *movies.Service
	series   *series.Service
	settings *settings.Service
	provider Provider
	ffmpeg   string
	ffprobe  string
	cache    *probeCache
	whisper  *whisperGen
	ledger   *Ledger // provenance, attempts and the blocklist (ledger.go); nil-safe
	log      *slog.Logger

	// Test seams for process(). Nil means the real code path: resolveFile, probeCached,
	// the ffmpeg extraction, and s.whisper.
	resolve func(ctx context.Context, job *Job) (fileRef, bool)
	probe   func(ctx context.Context, path string) (*mediaInfo, error)
	extract func(ctx context.Context, path string, picks []extractPick) error
	ai      aiRunner
	// rateWait replaces a 429's Retry-After wait when set (tests).
	rateWait time.Duration
	// ffprobeRun is probeCached's ffprobe call (probeSubs when nil).
	ffprobeRun func(ctx context.Context, ffprobe, path string) (*mediaInfo, error)

	mu        sync.Mutex
	jobs      []*Job           // recent subtitle-ensure jobs (newest first), for the Queue tab
	pending   [numPrios][]*Job // waiting for the worker, one oldest-first line per priority — unbounded, see Run
	active    map[string]*Job  // queued or running jobs by file key, for enqueue's dedupe
	wake      chan struct{}    // nudges the worker when pending gains a job
	nextID    int64
	running   *Job               // the job the worker is on, if any
	cancelRun context.CancelFunc // cancels the running job's context (Stop button)
	logMu     sync.Mutex
	logBuf    []LogLine // recent activity console lines, for the Logs tab

	snap librarySnapshot // the last library pass; what the Overview and Library tab read
}

// NewService wires the module over the shared Movies/Series catalogs + a subtitle provider. db is
// used for the probe cache; ffmpeg/ffprobe drive embedded-track probing and extraction;
// whisperModelsDir is where the local AI (whisper.cpp) model files live.
func NewService(db *sql.DB, mv *movies.Service, sr *series.Service, set *settings.Service, provider Provider, ffmpeg, ffprobe, whisperModelsDir string, log *slog.Logger) *Service {
	s := &Service{
		movies: mv, series: sr, settings: set, provider: provider,
		ffmpeg: ffmpeg, ffprobe: ffprobe, cache: &probeCache{db: db},
		whisper: detectWhisper(whisperModelsDir), ledger: NewLedger(db), log: log,
		wake: make(chan struct{}, 1),
	}
	// A silent fall-back from the oneAPI build would hide a broken GPU setup behind a
	// slow run; surface it as a warning event the first time it happens.
	s.whisper.note = func(level, msg string) { s.event(level, msg) }
	return s
}

// Ledger is the module's record of sidecar sources, attempts and blocked provider files.
func (s *Service) Ledger() *Ledger { return s.ledger }

// Settings is the module's configuration + provider readiness for the dashboard.
type Settings struct {
	MoviesAuto    bool     `json:"movies_auto"`
	SeriesAuto    bool     `json:"series_auto"`
	Languages     []string `json:"languages"`
	ProviderReady bool     `json:"provider_ready"` // can search
	CanDownload   bool     `json:"can_download"`   // can actually grab (needs account)
	AIReady       bool     `json:"ai_ready"`       // local whisper.cpp binary + a model present
	AIBackend     string   `json:"ai_backend"`     // "sycl" | "vulkan" | "cpu" | "" until the first run
	AINote        string   `json:"ai_note"`        // why the oneAPI build isn't in use, when it was bundled but set aside
	// Download quota, when the provider reports one. OpenSubtitles free accounts get a
	// small number of downloads a day; once it's spent every download fails until the
	// reset, and the UI should say that rather than showing a run of failures.
	QuotaRemaining int   `json:"quota_remaining"` // -1 = unknown
	QuotaResetAt   int64 `json:"quota_reset_at"`  // unix seconds; 0 = not paused
	Pending        int   `json:"pending"`         // jobs waiting for the worker
	// What OpenSubtitles actually said: not_configured | needs_account | unverified | ok |
	// auth_failed | quota_spent | rate_limited. Only "ok" means a login has worked.
	ProviderState    string `json:"provider_state"`
	ProviderDetail   string `json:"provider_detail"`
	VIP              bool   `json:"vip"`
	AllowedDownloads int    `json:"allowed_downloads"` // per day; -1 = unknown
}

// GetSettings returns the current configuration + provider status.
func (s *Service) GetSettings(ctx context.Context) Settings {
	out := Settings{
		MoviesAuto:     s.settings.GetBool(ctx, keyMoviesAuto, defaultMoviesAuto),
		SeriesAuto:     s.settings.GetBool(ctx, keySeriesAuto, defaultSeriesAuto),
		Languages:      s.languages(ctx),
		ProviderReady:  s.provider.Available(),
		CanDownload:    s.provider.CanDownload(),
		AIReady:        s.whisper.available(),
		AIBackend:      s.whisper.Backend(),
		AINote:         s.whisper.SyclNote(),
		QuotaRemaining: -1,
		Pending:        s.Pending(),
	}
	st := s.providerStatus()
	out.ProviderState, out.ProviderDetail, out.VIP, out.AllowedDownloads = st.State, st.Detail, st.VIP, st.Allowed
	if q, ok := s.provider.(quotaReporter); ok {
		remaining, resetAt := q.Quota()
		out.QuotaRemaining = remaining
		if !resetAt.IsZero() && resetAt.After(time.Now()) {
			out.QuotaResetAt = resetAt.Unix()
		}
	}
	return out
}

// providerStatus is the provider's real state; one that can't report it is judged by
// what's configured, and never "ok".
func (s *Service) providerStatus() ProviderStatus {
	if sr, ok := s.provider.(statusReporter); ok {
		return sr.Status()
	}
	switch {
	case s.provider == nil || !s.provider.Available():
		return ProviderStatus{State: ProviderNotConfigured, Allowed: -1}
	case !s.provider.CanDownload():
		return ProviderStatus{State: ProviderNeedsAccount, Allowed: -1}
	}
	return ProviderStatus{State: ProviderUnverified, Allowed: -1}
}

// quotaReporter is implemented by providers that meter downloads.
type quotaReporter interface {
	// Quota returns downloads remaining (-1 unknown) and when a spent quota resets (zero
	// time when not paused).
	Quota() (remaining int, resetAt time.Time)
}

// SetSettings updates whichever fields are provided (nil = leave unchanged).
func (s *Service) SetSettings(ctx context.Context, moviesAuto, seriesAuto *bool, languages []string) error {
	if moviesAuto != nil {
		if err := s.settings.SetBool(ctx, keyMoviesAuto, *moviesAuto); err != nil {
			return err
		}
	}
	if seriesAuto != nil {
		if err := s.settings.SetBool(ctx, keySeriesAuto, *seriesAuto); err != nil {
			return err
		}
	}
	if languages != nil {
		clean := make([]string, 0, len(languages))
		for _, l := range languages {
			if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
				clean = append(clean, l)
			}
		}
		if len(clean) == 0 {
			clean = defaultLanguages
		}
		before := strings.Join(s.languages(ctx), ",")
		if err := s.settings.Set(ctx, keyLanguages, strings.Join(clean, ",")); err != nil {
			return err
		}
		// A language the AI couldn't make from that audio may be makeable now (or no
		// longer wanted); let the next sweep look again.
		if strings.Join(clean, ",") != before {
			s.ClearBackoff(ctx, EventLangs)
		}
	}
	return nil
}

// CredentialsChanged is called after an OpenSubtitles credential is saved or cleared:
// the provider forgets the last login (the next job or Test signs in fresh), and files
// parked on a rejected login are tried again by the next sweep.
func (s *Service) CredentialsChanged(ctx context.Context) {
	if r, ok := s.provider.(interface{ ResetAuth() }); ok {
		r.ResetAuth()
	}
	s.ClearBackoff(ctx, EventCreds)
}

func (s *Service) languages(ctx context.Context) []string {
	raw := s.settings.Get(ctx, keyLanguages, strings.Join(defaultLanguages, ","))
	var out []string
	for _, l := range strings.Split(raw, ",") {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return defaultLanguages
	}
	return out
}

// MovieStatus is a movie's subtitle coverage for the dashboard.
type MovieStatus struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Year      int      `json:"year"`
	PosterURL string   `json:"poster_url,omitempty"`
	Present   []string `json:"present"`
	Missing   []string `json:"missing"`
}

// MovieStatuses lists downloaded movies with their per-language subtitle coverage.
func (s *Service) MovieStatuses(ctx context.Context) ([]MovieStatus, error) {
	langs := s.languages(ctx)
	list, err := s.movies.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []MovieStatus
	for _, m := range list {
		if !m.HasFile || m.MovieFilePath == "" {
			continue
		}
		present := scanSidecars(m.MovieFilePath, langs, "movie").Present
		out = append(out, MovieStatus{
			ID: m.ID, Title: m.Title, Year: m.Year, PosterURL: m.PosterURL,
			Present: nonNil(present), Missing: nonNil(missingOf(langs, present)),
		})
	}
	return out, nil
}

// SeriesStatus is a series' subtitle coverage rolled up across episodes.
type SeriesStatus struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	PosterURL   string `json:"poster_url,omitempty"`
	Episodes    int    `json:"episodes"`     // episodes with a file on disk
	Complete    int    `json:"complete"`     // episodes with every wanted language
	MissingSubs int    `json:"missing_subs"` // episodes missing at least one language
}

// SeriesStatuses lists series with rolled-up episode subtitle coverage.
func (s *Service) SeriesStatuses(ctx context.Context) ([]SeriesStatus, error) {
	langs := s.languages(ctx)
	list, err := s.series.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []SeriesStatus
	for _, sr := range list {
		full, err := s.series.Get(ctx, sr.ID)
		if err != nil {
			continue
		}
		st := SeriesStatus{ID: sr.ID, Title: sr.Title, Year: sr.Year, PosterURL: sr.PosterURL}
		for _, season := range full.Seasons {
			for _, ep := range season.Episodes {
				if !ep.HasFile || ep.FilePath == "" {
					continue
				}
				st.Episodes++
				if len(missingOf(langs, scanSidecars(ep.FilePath, langs, "episode").Present)) == 0 {
					st.Complete++
				} else {
					st.MissingSubs++
				}
			}
		}
		if st.Episodes > 0 {
			out = append(out, st)
		}
	}
	return out, nil
}

// GrabMovie fetches every missing wanted-language subtitle for one movie. Returns how many
// subtitle files were written.
func (s *Service) GrabMovie(ctx context.Context, id int64) (int, error) {
	m, err := s.movies.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if !m.HasFile || m.MovieFilePath == "" {
		return 0, nil
	}
	langs := s.languages(ctx)
	missing := missingOf(langs, scanSidecars(m.MovieFilePath, langs, "movie").Present)
	grabbed := 0
	hash, _ := osHash(m.MovieFilePath) // "" on error: the search still works, just unranked by sync
	for _, lang := range missing {
		got, err := s.grabOne(ctx, m.IMDBID, m.Title, m.Year, 0, 0, m.MovieFilePath, hash, lang)
		if err != nil {
			s.log.Warn("subtitle grab failed", "movie", m.Title, "lang", lang, "err", err)
			continue
		}
		if got != nil {
			grabbed++
		}
	}
	return grabbed, nil
}

// GrabSeries fetches missing subtitles for every downloaded episode of a series.
func (s *Service) GrabSeries(ctx context.Context, id int64) (int, error) {
	full, err := s.series.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	langs := s.languages(ctx)
	grabbed := 0
	for _, season := range full.Seasons {
		for _, ep := range season.Episodes {
			if !ep.HasFile || ep.FilePath == "" {
				continue
			}
			missing := missingOf(langs, scanSidecars(ep.FilePath, langs, "episode").Present)
			if len(missing) == 0 {
				continue
			}
			hash, _ := osHash(ep.FilePath)
			for _, lang := range missing {
				got, err := s.grabOne(ctx, full.IMDBID, full.Title, full.Year, ep.SeasonNumber, ep.EpisodeNumber, ep.FilePath, hash, lang)
				if err != nil {
					s.log.Warn("subtitle grab failed", "series", full.Title, "s", ep.SeasonNumber, "e", ep.EpisodeNumber, "lang", lang, "err", err)
					continue
				}
				if got != nil {
					grabbed++
				}
			}
		}
	}
	return grabbed, nil
}

// AutoGrab queues subtitle-ensure jobs for whichever media types have auto-mode enabled — the
// scheduled sweep. The worker then makes each missing kept-language SRT by the best-source
// pipeline (extract → download → …), so auto-mode isn't download-only.
func (s *Service) AutoGrab(ctx context.Context) {
	if n, err := s.ledger.PruneAttempts(ctx, time.Now().Add(-AttemptRetention)); err != nil {
		s.log.Warn("subtitles: could not prune old attempts", "err", err)
	} else if n > 0 {
		s.log.Info("subtitles: pruned old attempts", "count", n)
	}
	set := s.GetSettings(ctx)
	if set.MoviesAuto {
		if n, _ := s.SweepMissing(ctx, "movies"); n > 0 {
			s.log.Info("subtitles: auto-sweep queued movies", "count", n)
		}
	}
	if set.SeriesAuto {
		if n, _ := s.SweepMissing(ctx, "tv"); n > 0 {
			s.log.Info("subtitles: auto-sweep queued series", "count", n)
		}
	}
}

// providerOpenSubtitles is the provider name the ledger records downloads under.
const providerOpenSubtitles = "opensubtitles"

// grabbed is the sidecar grabOne wrote and the candidate it came from.
type grabbed struct {
	Result  SubtitleResult
	Path    string
	Variant string
}

// grabOne searches for and downloads the best subtitle for one media file + language,
// writing it as a sidecar. Returns what was written, or nil when nothing matched.
func (s *Service) grabOne(ctx context.Context, imdb, title string, year, season, episode int, mediaPath, hash, lang string) (*grabbed, error) {
	// A paused quota means no requests at all: searching would spend the API's rate
	// limit on results that can't be downloaded until the reset.
	if s.providerPaused() {
		if res, msg := s.pauseOutcome(); res == ResultAuthFailed {
			return nil, fmt.Errorf("%w: %s", ErrAuthFailed, msg)
		}
		return nil, ErrQuotaExhausted
	}
	results, err := s.provider.Search(ctx, SearchRequest{
		IMDBID: imdb, Title: title, Year: year, Season: season, Episode: episode, Language: lang,
		MovieHash: hash,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	data, err := s.provider.Download(ctx, results[0].FileID)
	if err != nil {
		return nil, err
	}
	// A hearing-impaired upload is still the full subtitle; the ".sdh" qualifier lets Plex
	// label it as such.
	variant := VariantFull
	if results[0].HearingImpaired {
		variant = VariantSDH
	}
	dst := sidecarPathV(mediaPath, lang, variant)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return nil, err
	}
	s.log.Info("subtitle saved", "path", dst, "lang", lang, "release", results[0].Release,
		"hash_match", results[0].HashMatch, "hearing_impaired", results[0].HearingImpaired)
	return &grabbed{Result: results[0], Path: dst, Variant: variant}, nil
}

// nonNil returns an empty (non-nil) slice for nil, so JSON emits [] not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
