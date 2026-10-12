package subtitles

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// librarySnapshot is the last full pass over the library's subtitle coverage: the flat
// movie list and the per-show roll-up the Library tab shows, and the totals behind the
// Overview's percentage.
//
// The pages used to build all of this on every open — a directory listing per file, and
// there are 25,000 files on a spinning array — so opening the Overview WAS a library
// scan, and it ran again each time you came back. Now one pass runs at startup, every
// six hours, and on demand from the ↻ button; the pages read the result, and a job that
// writes a subtitle patches its own file into the snapshot so the numbers stay right
// between passes.
type librarySnapshot struct {
	mu       sync.Mutex
	movies   []FileSubs
	groups   []SeriesGroup
	at       time.Time // zero until the first pass completes
	scanning bool
	// langsKey is the kept-language list (langsKey()) the snapshot was counted against;
	// when it differs from the current list the figures are stale. passKey is the list
	// the running pass counts against, and followUp asks for one more pass after it
	// because the languages changed while it ran.
	langsKey string
	passKey  string
	followUp bool
}

// Coverage is the Overview's summary, straight from the snapshot.
type Coverage struct {
	Files     int            `json:"files"`
	Covered   int            `json:"covered"`
	Missing   int            `json:"missing"`
	Movies    CoverageCounts `json:"movies"`
	TV        CoverageCounts `json:"tv"`
	ScannedAt int64          `json:"scanned_at"` // unix seconds; 0 = no pass has finished yet
	Scanning  bool           `json:"scanning"`
	// Stale: the figures were counted against a different set of kept languages. A pass
	// to recount them is running or about to.
	Stale bool `json:"stale"`
}

// CoverageCounts is one media type's share of the totals.
type CoverageCounts struct {
	Files   int `json:"files"`
	Covered int `json:"covered"`
	Missing int `json:"missing"`
}

// langsKey is the kept-language list as one comparable string. Order matters: the first
// language is the one an untagged sidecar counts for.
func langsKey(langs []string) string { return strings.Join(langs, ",") }

// Rescan starts a full pass in the background, unless one is already running (then it
// reports false and the running one stands — except that a pass counting against
// languages that have since changed gets exactly one more pass after it). ctx should
// outlive the caller: a pass that an HTTP request started must not stop when the
// response goes out.
func (s *Service) Rescan(ctx context.Context) bool {
	if s.passFn == nil && (s.movies == nil || s.series == nil) {
		return false
	}
	s.snap.mu.Lock()
	if s.snap.scanning {
		if s.snap.passKey != langsKey(s.languages(ctx)) {
			s.snap.followUp = true
		}
		s.snap.mu.Unlock()
		return false
	}
	s.snap.scanning = true
	s.snap.mu.Unlock()
	go func() {
		for {
			key := langsKey(s.languages(ctx))
			s.snap.mu.Lock()
			s.snap.passKey, s.snap.followUp = key, false
			s.snap.mu.Unlock()
			ok := s.libraryPass(ctx, key)
			s.snap.mu.Lock()
			// The follow-up is decided and scanning cleared under one lock: a Rescan
			// between the two would otherwise ask for a pass nobody runs.
			again := ok && s.snap.followUp
			if !again {
				s.snap.scanning = false
			}
			s.snap.mu.Unlock()
			if !ok {
				return
			}
			if !again {
				break
			}
		}
		// The one-shot forced-track repair starts once a pass has completed (repair.go);
		// later passes find its settings key set and leave it.
		s.startForcedRepair(ctx)
	}()
	return true
}

// libraryPass is one full pass: it counts every file against the kept languages (key)
// and replaces the snapshot. false when it didn't finish.
func (s *Service) libraryPass(ctx context.Context, key string) bool {
	started := time.Now()
	var movies []FileSubs
	var groups []SeriesGroup
	var merr, gerr error
	if s.passFn != nil {
		movies, groups, merr = s.passFn(ctx)
	} else {
		movies, merr = s.Library(ctx, "movies")
		groups, gerr = s.SeriesGroups(ctx)
	}
	if ctx.Err() != nil {
		return false // shutting down — a half-finished pass is worse than the last complete one
	}
	if merr != nil || gerr != nil {
		s.log.Warn("subtitles: library scan failed", "movies_err", merr, "tv_err", gerr)
		return false
	}
	s.snap.mu.Lock()
	s.snap.movies = movies
	s.snap.groups = groups
	s.snap.at = time.Now()
	s.snap.langsKey = key
	s.snap.mu.Unlock()
	s.log.Info("subtitles: library scanned", "movies", len(movies), "shows", len(groups),
		"took", time.Since(started).Round(time.Second).String())
	return true
}

// recountIfLanguagesChanged starts a pass when the kept languages are no longer the ones
// in before — the Overview shows "Recounting…" meanwhile instead of a percentage for
// the old list. A pass already running gets one more after it (Rescan).
func (s *Service) recountIfLanguagesChanged(ctx context.Context, before string) {
	if langsKey(s.languages(ctx)) == before {
		return
	}
	s.Rescan(context.WithoutCancel(ctx))
}

// staleLocked reports whether the snapshot was counted against other languages than the
// kept ones now. Settings reads are in memory, so this is cheap. s.snap.mu must be held.
func (s *Service) staleLocked(ctx context.Context) bool {
	return !s.snap.at.IsZero() && s.settings != nil && s.snap.langsKey != langsKey(s.languages(ctx))
}

// Coverage summarises the snapshot for the Overview.
func (s *Service) Coverage() Coverage {
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	// Background: the settings read behind Stale is in memory and takes no context.
	c := Coverage{Scanning: s.snap.scanning, Stale: s.staleLocked(context.Background())}
	if !s.snap.at.IsZero() {
		c.ScannedAt = s.snap.at.Unix()
	}
	for _, m := range s.snap.movies {
		c.Movies.Files++
		if m.Missing == 0 {
			c.Movies.Covered++
		} else {
			c.Movies.Missing++
		}
	}
	for _, g := range s.snap.groups {
		c.TV.Files += g.Episodes
		c.TV.Covered += g.Covered
		c.TV.Missing += g.Missing
	}
	c.Files = c.Movies.Files + c.TV.Files
	c.Covered = c.Movies.Covered + c.TV.Covered
	c.Missing = c.Movies.Missing + c.TV.Missing
	return c
}

// SnapshotMovies returns the flat movie list from the last pass, and whether a pass has
// completed at all (before the first one the list is empty, not "no movies").
func (s *Service) SnapshotMovies() ([]FileSubs, bool) {
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	out := make([]FileSubs, len(s.snap.movies))
	copy(out, s.snap.movies)
	return out, !s.snap.at.IsZero()
}

// SnapshotGroups returns the per-show roll-up from the last pass, and whether one has completed.
func (s *Service) SnapshotGroups() ([]SeriesGroup, bool) {
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	out := make([]SeriesGroup, len(s.snap.groups))
	copy(out, s.snap.groups)
	return out, !s.snap.at.IsZero()
}

// refreshSnapshot recomputes one job's file (a movie) or show (an episode) and patches it
// into the snapshot, so a subtitle written between passes shows up without a pass.
func (s *Service) refreshSnapshot(ctx context.Context, job *Job) {
	s.snap.mu.Lock()
	seen := !s.snap.at.IsZero()
	s.snap.mu.Unlock()
	if !seen {
		return // the first pass is still running; it will see the new sidecar itself
	}
	langs := s.languages(ctx)
	if job.Kind == "episode" {
		g, ok := s.groupFor(ctx, job.SeriesID, langs)
		s.snap.mu.Lock()
		defer s.snap.mu.Unlock()
		for i := range s.snap.groups {
			if s.snap.groups[i].SeriesID == job.SeriesID {
				if ok {
					s.snap.groups[i] = g
				} else {
					s.snap.groups = append(s.snap.groups[:i], s.snap.groups[i+1:]...)
				}
				return
			}
		}
		if ok {
			s.snap.groups = append(s.snap.groups, g)
		}
		return
	}
	m, err := s.movies.Get(ctx, job.MovieID)
	if err != nil || !m.HasFile || m.MovieFilePath == "" {
		return
	}
	s.patchMovie(ctx, m, langs)
}

// patchMovie recomputes one movie's entry in the snapshot from its current file.
func (s *Service) patchMovie(ctx context.Context, m movies.Movie, langs []string) {
	fs := FileSubs{Kind: "movie", MovieID: m.ID, Title: m.Title, Year: m.Year, PosterURL: m.PosterURL, Path: m.MovieFilePath}
	s.fillCoverage(ctx, &fs, langs, s.provider != nil && s.provider.CanDownload())
	if bs, err := s.ledger.BackoffsFor(ctx, fmt.Sprintf("m:%d", m.ID)); err == nil {
		applyBackoff(&fs, bs, time.Now())
	}
	s.fillHealth(ctx, &fs)
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	for i := range s.snap.movies {
		if s.snap.movies[i].MovieID == m.ID {
			s.snap.movies[i] = fs
			return
		}
	}
	s.snap.movies = append(s.snap.movies, fs)
}

// dropMovie removes a movie's entry from the snapshot.
func (s *Service) dropMovie(movieID int64) {
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	kept := s.snap.movies[:0]
	for _, fs := range s.snap.movies {
		if fs.MovieID != movieID {
			kept = append(kept, fs)
		}
	}
	s.snap.movies = kept
}

// groupFor builds one show's roll-up: how many of its episodes on disk have every kept
// language as a sidecar. Directory listings only — nothing is probed. ok is false when
// the show has nothing on disk.
func (s *Service) groupFor(ctx context.Context, seriesID int64, langs []string) (SeriesGroup, bool) {
	full, err := s.series.Get(ctx, seriesID)
	if err != nil {
		return SeriesGroup{}, false
	}
	g := SeriesGroup{SeriesID: full.ID, Title: full.Title, Year: full.Year, PosterURL: full.PosterURL}
	for _, sn := range full.Seasons {
		seasonHasFile := false
		for _, e := range sn.Episodes {
			if !e.HasFile || e.FilePath == "" {
				continue
			}
			seasonHasFile = true
			g.Episodes++
			have := map[string]bool{}
			for _, p := range scanSidecars(e.FilePath, langs, "episode").Present {
				have[strings.ToLower(p)] = true
			}
			complete := true
			for _, l := range langs {
				if !have[strings.ToLower(l)] {
					complete = false
					break
				}
			}
			if complete {
				g.Covered++
			} else {
				g.Missing++
			}
		}
		if seasonHasFile {
			g.Seasons++
		}
	}
	if g.Episodes == 0 {
		return SeriesGroup{}, false // nothing on disk — nothing to subtitle
	}
	return g, true
}

// fmtAudio renders a duration in seconds as "42m" / "1h 03m", for the speed line.
func fmtAudio(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Minute)
	if d >= time.Hour {
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// Stale reports whether the snapshot's figures were counted for other kept languages than
// today's (a recount is running or about to), for the Library tab's notice.
func (s *Service) Stale() bool {
	s.snap.mu.Lock()
	defer s.snap.mu.Unlock()
	return s.staleLocked(context.Background())
}
