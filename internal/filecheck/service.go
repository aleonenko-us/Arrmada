package filecheck

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound is a finding id that isn't there.
var ErrNotFound = errors.New("file check finding not found")

// ErrNoReplacer is a replace asked for before the server wired one in.
var ErrNoReplacer = errors.New("replacing isn't available yet — try again in a moment")

// File is one library file to check, with what the library knows about it.
type File struct {
	Kind      string // KindMovie | KindEpisode
	MediaID   int64  // movies.id | episodes.id
	VersionID int64  // a movie's extra version (0 = the default track)
	OwnerID   int64  // the movie, or the episode's show
	Season    int
	Episode   int
	// Title names it in a sentence: "The Matrix (1999)", "Severance S01E02".
	Title        string
	Path         string
	Release      string // the release it was imported from ("" unknown)
	RuntimeMin   int
	OrigLanguage string
	Home         time.Time // a movie's home release (zero unknown)
	Theatrical   time.Time // a movie's cinema release (zero unknown)
	CamOK        bool      // its profile accepts cams
}

func (f File) key() fileKey { return fileKey{f.Kind, f.MediaID, f.VersionID} }

// Library lists the files to check.
type Library interface {
	// Files is every movie and episode file in the library.
	Files(ctx context.Context) ([]File, error)
	// MovieFiles is one movie's files (every track), SeriesFiles one show's.
	MovieFiles(ctx context.Context, movieID int64) ([]File, error)
	SeriesFiles(ctx context.Context, seriesID int64) ([]File, error)
}

// Settings is the settings service, as far as the check needs it.
type Settings interface {
	GetBool(ctx context.Context, key string, def bool) bool
	SetBool(ctx context.Context, key string, value bool) error
}

// ReplaceFunc replaces a bad copy: blocklists the release it came from (when known) and
// searches for another, keeping the file until a replacement is imported.
type ReplaceFunc func(ctx context.Context, f Stored) error

// Service runs checks, keeps what they found and replaces bad copies.
type Service struct {
	repo     repo
	lib      Library
	probe    Prober
	settings Settings
	wanted   func(ctx context.Context) []string
	log      *slog.Logger
	now      func() time.Time

	replace atomic.Pointer[ReplaceFunc]
	kick    atomic.Pointer[func()]
	pass    sync.Mutex // one library pass at a time
	// gaveUp remembers the titles whose give-up was logged, so a nightly pass doesn't say
	// it again.
	gaveUp sync.Map
}

// New builds the service. wanted is the household's audio languages (nil = English).
func New(db *sql.DB, lib Library, p Prober, st Settings, wanted func(ctx context.Context) []string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if p == nil {
		p = FFmpeg{}
	}
	return &Service{repo: repo{db}, lib: lib, probe: p, settings: st, wanted: wanted, log: log, now: time.Now}
}

// SetReplacer wires in how a bad copy is replaced (the HTTP layer, which owns the search
// queue). Until it's set, findings are only flagged.
func (s *Service) SetReplacer(fn ReplaceFunc) { s.replace.Store(&fn) }

// Log is the service's logger, for callers reporting a check that failed.
func (s *Service) Log() *slog.Logger { return s.log }

// SetAttentionKick has a change in findings refresh Needs you soon.
func (s *Service) SetAttentionKick(fn func()) { s.kick.Store(&fn) }

func (s *Service) kickAttention() {
	if fn := s.kick.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

// Check reads one file, unless it is unchanged since its last check and force is false.
// A file not on disk is left alone (the missing-file checks deal with it). It reports
// whether the file was read.
func (s *Service) Check(ctx context.Context, f File, force bool) (bool, error) {
	fi, err := os.Stat(f.Path)
	if err != nil || fi.IsDir() {
		return false, nil
	}
	id := identity{Path: f.Path, Size: fi.Size(), Mtime: fi.ModTime().Unix()}
	if !force {
		prev, ok, err := s.repo.identity(ctx, f.key())
		if err != nil {
			return false, err
		}
		if ok && prev == id {
			return false, nil
		}
	}
	p := s.probe.Probe(ctx, f.Path)
	if ctx.Err() != nil {
		return false, ctx.Err() // a probe cut short by shutdown says nothing about the file
	}
	in := Input{Kind: f.Kind, Release: f.Release, Filename: filepath.Base(f.Path), RuntimeMin: f.RuntimeMin,
		OrigLanguage: f.OrigLanguage, Home: f.Home, Theatrical: f.Theatrical, CamOK: f.CamOK}
	if s.wanted != nil {
		in.Wanted = s.wanted(ctx)
	}
	if f.Kind == KindMovie && (!f.Home.IsZero() || !f.Theatrical.IsZero()) {
		// When it was grabbed; failing that, when the file was written, which for a
		// download is about when it arrived.
		in.GrabbedAt = s.repo.grabbedAt(ctx, f.MediaID, f.VersionID, f.Release)
		if in.GrabbedAt.IsZero() {
			in.GrabbedAt = fi.ModTime()
		}
	}
	found := Classify(in, p)
	if err := s.repo.record(ctx, f, id, found, s.now()); err != nil {
		return true, err
	}
	for _, fd := range found {
		s.log.Info("file check: found a problem", "title", f.Title, "problem", fd.Code, "detail", fd.Detail, "path", f.Path)
	}
	return true, nil
}

// CheckMovie checks a movie's files after an import (only those that changed), forgets
// checks of files it no longer has, and replaces a bad copy if that's on.
func (s *Service) CheckMovie(ctx context.Context, movieID int64) error {
	files, err := s.lib.MovieFiles(ctx, movieID)
	if err != nil {
		return err
	}
	return s.checkOwner(ctx, KindMovie, movieID, files)
}

// CheckSeries is CheckMovie for a show's episodes.
func (s *Service) CheckSeries(ctx context.Context, seriesID int64) error {
	files, err := s.lib.SeriesFiles(ctx, seriesID)
	if err != nil {
		return err
	}
	return s.checkOwner(ctx, KindEpisode, seriesID, files)
}

func (s *Service) checkOwner(ctx context.Context, kind string, owner int64, files []File) error {
	keep := map[fileKey]bool{}
	for _, f := range files {
		keep[f.key()] = true
		if _, err := s.Check(ctx, f, false); err != nil {
			return err
		}
	}
	if err := s.repo.forget(ctx, kind, owner, keep); err != nil {
		return err
	}
	s.AutoReplace(ctx)
	s.kickAttention()
	return nil
}

// PassResult is what a library pass did.
type PassResult struct {
	Files   int `json:"files"`   // in the library
	Checked int `json:"checked"` // read this time (new or changed, or every one when forced)
	Errors  int `json:"errors"`
}

// Pass checks the whole library: files new or changed since their last check, or every
// file when force is set. One pass runs at a time; a second call while one runs returns
// at once. Checks of files no longer in the library are forgotten.
func (s *Service) Pass(ctx context.Context, force bool) (PassResult, error) {
	var res PassResult
	if !s.pass.TryLock() {
		return res, nil
	}
	defer s.pass.Unlock()
	files, err := s.lib.Files(ctx)
	if err != nil {
		return res, err
	}
	res.Files = len(files)
	keep := map[string]map[fileKey]bool{KindMovie: {}, KindEpisode: {}}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		keep[f.Kind][f.key()] = true
		did, err := s.Check(ctx, f, force)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			res.Errors++
			s.log.Warn("file check: couldn't record a check", "path", f.Path, "err", err)
			continue
		}
		if did {
			res.Checked++
		}
	}
	for kind, k := range keep {
		if err := s.repo.forget(ctx, kind, 0, k); err != nil {
			return res, err
		}
	}
	s.AutoReplace(ctx)
	s.kickAttention()
	return res, nil
}

// Findings lists the findings still standing (not ignored), or with all set every one.
func (s *Service) Findings(ctx context.Context, all bool) ([]Problem, error) {
	list, err := s.repo.findings(ctx, !all, "", 0)
	if err != nil {
		return nil, err
	}
	return s.describe(ctx, list)
}

// FindingsFor lists one movie's (kind movie) or show's (kind episode) standing findings.
func (s *Service) FindingsFor(ctx context.Context, kind string, owner int64) ([]Problem, error) {
	list, err := s.repo.findings(ctx, true, kind, owner)
	if err != nil {
		return nil, err
	}
	return s.describe(ctx, list)
}

// Dismiss is "Ignore": the finding stays recorded but is no longer shown or acted on,
// until the file changes.
func (s *Service) Dismiss(ctx context.Context, id int64) error {
	if err := s.repo.dismiss(ctx, id); err != nil {
		return err
	}
	s.kickAttention()
	return nil
}

// ReplaceNow is "Find a better copy": blocklist the release the file came from and search
// for another. Asked for by hand, it isn't held to the automatic limits.
func (s *Service) ReplaceNow(ctx context.Context, id int64) (Stored, error) {
	f, err := s.repo.finding(ctx, id)
	if err != nil {
		return f, err
	}
	fn := s.replace.Load()
	if fn == nil || *fn == nil {
		return f, ErrNoReplacer
	}
	if err := (*fn)(ctx, f); err != nil {
		return f, err
	}
	s.log.Info("file check: replacing a bad copy", "title", f.Title, "problem", f.Code, "blocklisted", f.Release, "by", "hand")
	if err := s.repo.addReplacement(ctx, f.Kind, mediaOf(f), f.Code, f.Release, false, s.now()); err != nil {
		return f, err
	}
	s.kickAttention()
	return f, nil
}

// mediaOf is what replacements are counted against: the movie (all its tracks) or the
// episode.
func mediaOf(f Stored) int64 {
	if f.Kind == KindMovie {
		return f.OwnerID
	}
	return f.MediaID
}
