package subtitles

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
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
	log      *slog.Logger
	bin      library.Bin // where replaced/removed sidecars go (retireSidecar); nil = refuse
	// adoptHook records an adopted orphan as Arrmada's own (SetAdoptHook); nil = no record.
	adoptHook func(ctx context.Context, a AdoptedSidecar) error
	// libraryChanged hears about the folder of each sidecar change (Plex's scanner; events.go).
	libraryChanged atomic.Pointer[func(kind, dir string)]

	// repair is the one-shot forced-track repair pass (repair.go).
	repair forcedRepair

	// Test seams for process(). Nil means the real code path: resolveFile, probeCached,
	// the ffmpeg extraction, and s.whisper.
	resolve func(ctx context.Context, job *Job) (fileRef, bool)
	probe   func(ctx context.Context, path string) (*mediaInfo, error)
	extract func(ctx context.Context, path string, picks []extractPick) error
	ai      aiRunner
	// ffprobeRun is probeCached's ffprobe call (probeSubs when nil).
	ffprobeRun func(ctx context.Context, ffprobe, path string) (*mediaInfo, error)
	// passFn is one library pass (Library + SeriesGroups when nil).
	passFn func(ctx context.Context) ([]FileSubs, []SeriesGroup, error)

	// blocklist returns the provider file ids never to download again for a media key
	// (Job.key() format). The ledger (SUB-11) sets it; nil blocks nothing. An error stops
	// the download rung for that language (fail closed).
	blocklist func(ctx context.Context, mediaKey string) (map[string]bool, error)
	// syncGate checks a downloaded non-hash candidate against the file's audio before it
	// is saved, returning the bytes to save (shifted into sync if need be). SUB-19 sets
	// it. While it's nil, candidates at or above the minimum score are saved as they
	// come, and the band between scoreFloor and the minimum is refused.
	syncGate func(ctx context.Context, ref fileRef, lang string, c ScoredCandidate, srt []byte) (out []byte, ok bool, detail string)

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
		whisper: detectWhisper(whisperModelsDir), log: log,
		wake: make(chan struct{}, 1),
	}
	// A silent fall-back from the oneAPI build would hide a broken GPU setup behind a
	// slow run; surface it as a warning event the first time it happens.
	s.whisper.note = func(level, msg string) { s.event(level, msg) }
	return s
}

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
	// Stale: the Library and Overview figures were counted for a different set of kept
	// languages; a recount is running or about to.
	Stale bool `json:"stale"`
	// MinScore is the match score (0-100) a download without a hash match needs.
	MinScore int `json:"min_score"`
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
		MinScore:       s.minScore(ctx),
	}
	s.snap.mu.Lock()
	out.Stale = s.staleLocked(ctx)
	s.snap.mu.Unlock()
	if q, ok := s.provider.(quotaReporter); ok {
		remaining, resetAt := q.Quota()
		out.QuotaRemaining = remaining
		if !resetAt.IsZero() && resetAt.After(time.Now()) {
			out.QuotaResetAt = resetAt.Unix()
		}
	}
	return out
}

// quotaReporter is implemented by providers that meter downloads.
type quotaReporter interface {
	// Quota returns downloads remaining (-1 unknown) and when a spent quota resets (zero
	// time when not paused).
	Quota() (remaining int, resetAt time.Time)
}

// SetSettings updates whichever fields are provided (nil = leave unchanged).
func (s *Service) SetSettings(ctx context.Context, moviesAuto, seriesAuto *bool, languages []string) error {
	// A change of languages makes every count stale: recount now rather than at the next
	// six-hourly pass. Saving the same list starts nothing.
	defer s.recountIfLanguagesChanged(ctx, langsKey(s.languages(ctx)))
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
		if err := s.settings.Set(ctx, keyLanguages, strings.Join(clean, ",")); err != nil {
			return err
		}
	}
	return nil
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

// AutoGrab queues subtitle-ensure jobs for whichever media types have auto-mode enabled — the
// scheduled sweep. The worker then makes each missing kept-language SRT by the best-source
// pipeline (extract → download → …), so auto-mode isn't download-only.
func (s *Service) AutoGrab(ctx context.Context) {
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

// grabResult is what one language's download attempt came to.
type grabResult struct {
	Written bool
	Path    string          // the sidecar written
	Pick    ScoredCandidate // the candidate saved, or the best one refused
	Outcome string          // "done" | "no_match" | "below_score" | "out_of_sync"
	Detail  string          // human wording for the job note
}

// grabOne searches for the best subtitle for one media file + language and writes it as
// a sidecar, if one is good enough. Candidates are scored against the file (scoring.go):
// a hash match always wins; otherwise the best must reach the minimum match score. A
// candidate between scoreFloor and the minimum is used only if the sync check (syncGate,
// SUB-19) passes it — refused while there is none. Blocklisted and rejected candidates
// are never downloaded.
func (s *Service) grabOne(ctx context.Context, ref fileRef, hash, lang string) (grabResult, error) {
	// A paused quota means no requests at all: searching would spend the API's rate
	// limit on results that can't be downloaded until the reset.
	if s.providerPaused() {
		return grabResult{}, ErrQuotaExhausted
	}
	results, err := s.provider.Search(ctx, SearchRequest{
		IMDBID: ref.IMDB, Title: ref.Title, Year: ref.Year, Season: ref.Season, Episode: ref.Episode,
		Language: lang, MovieHash: hash,
	})
	if err != nil {
		return grabResult{}, err
	}
	noMatch := grabResult{Outcome: "no_match", Detail: "no OpenSubtitles match"}
	if len(results) == 0 {
		return noMatch, nil
	}
	blocked, err := s.blockedFiles(ctx, ref.MediaKey)
	if err != nil {
		return grabResult{}, fmt.Errorf("read subtitle blocklist: %w", err)
	}
	ranked := rankCandidates(results, matchContextFor(ref), blocked)
	if !ranked[0].usable() {
		noMatch.Detail = "no usable OpenSubtitles match"
		return noMatch, nil
	}
	best := ranked[0]
	need := s.minScore(ctx)
	s.log.Info("subtitle candidates scored", "media", ref.MediaKey, "lang", lang, "candidates", len(results),
		"best", best.Score, "hash_match", best.HashMatch, "min", need)

	tries := 0
	var refusal string
	for _, c := range ranked {
		if !c.usable() || c.Score < scoreFloor || tries == maxDownloadTries {
			break
		}
		if !c.HashMatch && c.Score < need && s.syncGate == nil {
			break // SUB-19: this band is accepted only when a sync check passes
		}
		data, err := s.provider.Download(ctx, c.FileID)
		if err != nil {
			return grabResult{}, err
		}
		tries++
		if !c.HashMatch && s.syncGate != nil {
			out, ok, detail := s.syncGate(ctx, ref, lang, c, data)
			if !ok {
				refusal = detail
				continue
			}
			data = out
		}
		// A hearing-impaired upload is still the full subtitle; the ".sdh" qualifier lets
		// Plex label it as such.
		variant := VariantFull
		if c.HearingImpaired {
			variant = VariantSDH
		}
		dst := sidecarPathV(ref.Path, lang, variant)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return grabResult{}, err
		}
		s.log.Info("subtitle saved", "path", dst, "lang", lang, "release", c.Release, "score", c.Score,
			"hash_match", c.HashMatch, "hearing_impaired", c.HearingImpaired)
		s.event("info", fmt.Sprintf("%s: %s subtitle scored %d (%s)", ref.Title, lang, c.Score, strings.Join(c.Reasons, ", ")))
		return grabResult{Written: true, Path: dst, Pick: c, Outcome: "done", Detail: "downloaded"}, nil
	}
	if refusal != "" {
		return grabResult{Pick: best, Outcome: "out_of_sync", Detail: refusal}, nil
	}
	detail := fmt.Sprintf("best OpenSubtitles match scored %d, below %d: %s", best.Score, need, best.mismatches())
	s.event("info", fmt.Sprintf("%s: %s — %s", ref.Title, lang, detail))
	return grabResult{Pick: best, Outcome: "below_score", Detail: detail}, nil
}

// nonNil returns an empty (non-nil) slice for nil, so JSON emits [] not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
