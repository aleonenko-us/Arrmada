package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// Orphaned movie subtitles: adopt (rename it to pair with the video) or replace (make a
// fresh one, then retire the orphan to the recycle bin). Either way the owner decides once
// and the file stops being an orphan; nothing is ever deleted outright.

var (
	// ErrNotOrphan: the named file isn't one of this movie's orphaned subtitles right now.
	ErrNotOrphan = errors.New("that file isn't an orphaned subtitle of this movie")
	// ErrOrphanTargetExists: the movie already has a paired subtitle under the name an
	// adopt would give it; adopting would overwrite it.
	ErrOrphanTargetExists = errors.New("the movie already has a subtitle under that name")
	// ErrBadLanguage: the language or variant asked for isn't one a sidecar can be named with.
	ErrBadLanguage = errors.New("not a language or variant a subtitle can be named with")
	// ErrNotKept: a replace for a language that isn't kept — nothing would make it.
	ErrNotKept = errors.New("that language isn't one you keep, so nothing would replace it — adopt it instead")
	// ErrForcedOrphan: a forced orphan isn't a language's subtitle, so a fresh one can't
	// stand in for it.
	ErrForcedOrphan = errors.New("a forced subtitle can't be replaced — adopt it instead")
	// ErrJobBusy: a job for the movie is running, so the replace can't ride along with it.
	ErrJobBusy = errors.New("a subtitle job for this movie is running — try again when it finishes")
)

// AdoptedSidecar is one orphan the owner adopted: from now on Arrmada owns it.
type AdoptedSidecar struct {
	MediaKey  string // "m:<id>"
	VideoPath string
	Path      string // the sidecar's new, paired path
	Lang      string
	Variant   string
}

// SetAdoptHook registers who records an adopted sidecar as Arrmada's own (the
// subtitle_files ledger). Until one is set, adoption only renames the file.
func (s *Service) SetAdoptHook(fn func(ctx context.Context, a AdoptedSidecar) error) { s.adoptHook = fn }

// recordAdopted hands an adoption to the ownership hook. The rename has already happened,
// so a failure here is logged, not returned.
func (s *Service) recordAdopted(ctx context.Context, a AdoptedSidecar) {
	if s.adoptHook == nil {
		return
	}
	if err := s.adoptHook(ctx, a); err != nil {
		s.log.Warn("subtitles: couldn't record an adopted subtitle", "path", a.Path, "err", err)
	}
}

// orphanRetire is one orphan a replace job retires once its language has been made.
type orphanRetire struct {
	Name string // file name in the video's folder
	Lang string // the kept language it stands for
}

// langTagRe is what a sidecar language tag may look like ("en", "eng", "pt-br"): it ends
// up in a file name, so nothing else gets through.
var langTagRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,4})?$`)

// sidecarLangTag picks the code a sidecar for a language token is named with: the kept
// language it matches (an "eng" orphan adopted for a kept "en" becomes ".en"), else the
// token's two-letter code, else the token as it is. ok is false for anything that isn't a
// language.
func sidecarLangTag(tok string, kept []string) (string, bool) {
	t := normLang(strings.ToLower(strings.TrimSpace(tok)))
	if !langTagRe.MatchString(t) {
		return "", false
	}
	for _, k := range kept {
		if langMatches(t, k) {
			return strings.ToLower(k), true
		}
	}
	if !isKnownLang(t) {
		return "", false
	}
	for two, three := range twoToThree {
		if three == t {
			return two, true
		}
	}
	return t, true
}

// isKept reports whether lang is one of the kept languages.
func isKept(lang string, kept []string) bool {
	for _, k := range kept {
		if strings.EqualFold(k, lang) {
			return true
		}
	}
	return false
}

// movieOrphan looks up one of a movie's orphans by file name, from the folder as it is
// now — never from a path the client sent.
func (s *Service) movieOrphan(ctx context.Context, movieID int64, name string) (movies.Movie, Orphan, []string, error) {
	m, err := s.movies.Get(ctx, movieID)
	if err != nil {
		return movies.Movie{}, Orphan{}, nil, err
	}
	langs := s.languages(ctx)
	if !m.HasFile || m.MovieFilePath == "" {
		return m, Orphan{}, langs, ErrNotOrphan
	}
	for _, o := range scanSidecars(m.MovieFilePath, langs, "movie").Orphans {
		if o.Name == name {
			return m, o, langs, nil
		}
	}
	return m, Orphan{}, langs, ErrNotOrphan
}

// orphanLang is the language an orphan stands for: its own tag, or the first kept
// language when its name has none.
func orphanLang(o Orphan, kept []string) (string, bool) {
	if o.Lang == "" {
		return strings.ToLower(kept[0]), true
	}
	return sidecarLangTag(o.Lang, kept)
}

// AdoptOrphan renames one of a movie's orphaned subtitles so it pairs with the movie's
// file: "<video base>.<lang>[.<variant>]<ext>", keeping its extension. lang and variant
// default to what the orphan's name says (an untagged one takes the first kept
// language). It never overwrites: ErrOrphanTargetExists when that name is taken. Returns
// the new file name.
func (s *Service) AdoptOrphan(ctx context.Context, movieID int64, name string, lang, variant *string) (string, error) {
	m, o, langs, err := s.movieOrphan(ctx, movieID, name)
	if err != nil {
		return "", err
	}
	tag, ok := orphanLang(o, langs)
	if lang != nil {
		tag, ok = sidecarLangTag(*lang, langs)
	}
	if !ok {
		return "", ErrBadLanguage
	}
	v := o.Variant
	if variant != nil {
		v = strings.ToLower(strings.TrimSpace(*variant))
	}
	if v != VariantFull && v != VariantForced && v != VariantSDH {
		return "", ErrBadLanguage
	}
	newName, err := s.adoptOne(ctx, m, o, tag, v)
	if err != nil {
		return "", err
	}
	s.event("info", fmt.Sprintf("%s: adopted %s as %s", m.Title, o.Name, newName))
	return newName, nil
}

// adoptOne does the rename, records the ownership, tells Plex and patches the Library.
func (s *Service) adoptOne(ctx context.Context, m movies.Movie, o Orphan, lang, variant string) (string, error) {
	video := m.MovieFilePath
	dir := filepath.Dir(video)
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	newName := base + "." + lang
	if variant != VariantFull {
		newName += "." + variant
	}
	newName += strings.ToLower(filepath.Ext(o.Name))
	dst := filepath.Join(dir, newName)
	if err := renameNoClobber(filepath.Join(dir, o.Name), dst); err != nil {
		return "", err
	}
	s.recordAdopted(ctx, AdoptedSidecar{MediaKey: fmt.Sprintf("m:%d", m.ID), VideoPath: video, Path: dst, Lang: lang, Variant: variant})
	s.patchSeenMovie(ctx, m)
	return newName, nil
}

// renameNoClobber moves src to dst unless dst exists. A hard link refuses an existing
// name atomically; where links aren't possible it checks, then renames.
func renameNoClobber(src, dst string) error {
	err := os.Link(src, dst)
	switch {
	case err == nil:
		if rerr := os.Remove(src); rerr != nil {
			_ = os.Remove(dst) // keep exactly one copy, under the old name
			return rerr
		}
		return nil
	case errors.Is(err, os.ErrExist):
		return ErrOrphanTargetExists
	}
	if _, serr := os.Lstat(dst); serr == nil {
		return ErrOrphanTargetExists
	} else if !errors.Is(serr, os.ErrNotExist) {
		return serr
	}
	return os.Rename(src, dst)
}

// patchSeenMovie recomputes a movie's Library entry when a pass has run (before the first
// one there's nothing to patch; the pass will see the change).
func (s *Service) patchSeenMovie(ctx context.Context, m movies.Movie) {
	s.snap.mu.Lock()
	seen := !s.snap.at.IsZero()
	s.snap.mu.Unlock()
	if seen {
		s.patchMovie(ctx, m, s.languages(ctx))
	}
}

// ReplaceOrphan queues a manual job that makes the orphan's language as a properly named
// subtitle; once that language has been written, the orphan goes to the recycle bin. If
// nothing gets made, the orphan stays.
func (s *Service) ReplaceOrphan(ctx context.Context, movieID int64, name string) (*Job, error) {
	m, o, langs, err := s.movieOrphan(ctx, movieID, name)
	if err != nil {
		return nil, err
	}
	if !coversFull(o.Variant) {
		return nil, ErrForcedOrphan
	}
	tag, ok := orphanLang(o, langs)
	if !ok || !isKept(tag, langs) {
		return nil, ErrNotKept
	}
	return s.queueReplace(m, []orphanRetire{{Name: o.Name, Lang: tag}})
}

// queueReplace queues (or joins) the movie's manual job carrying the orphans to retire.
// A job already running for the movie can't take them on: ErrJobBusy.
func (s *Service) queueReplace(m movies.Movie, retire []orphanRetire) (*Job, error) {
	job := s.enqueue(&Job{Kind: "movie", MovieID: m.ID, Title: m.Title, Priority: PrioManual, retire: retire})
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range retire {
		found := false
		for _, have := range job.retire {
			if have.Name == r.Name {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrJobBusy
		}
	}
	return job, nil
}

// retireReplaced runs at the end of a replace job: each orphan whose language the job
// made goes to the recycle bin. One whose language wasn't made stays, and so does one
// that is no longer an orphan (renamed or adopted meanwhile). A bin failure keeps the file.
func (s *Service) retireReplaced(ctx context.Context, job *Job, ref fileRef, outcomes []langOutcome) {
	if len(job.retire) == 0 {
		return
	}
	made := map[string]bool{}
	for _, o := range outcomes {
		if o.Result == "done" {
			made[strings.ToLower(o.Lang)] = true
		}
	}
	still := map[string]bool{}
	for _, o := range scanSidecars(ref.Path, s.languages(ctx), "movie").Orphans {
		still[o.Name] = true
	}
	for _, r := range job.retire {
		switch {
		case !made[strings.ToLower(r.Lang)]:
			s.event("info", fmt.Sprintf("%s: kept %s — no new %s subtitle was made", ref.Title, r.Name, r.Lang))
		case !still[r.Name]:
			// Gone, or no longer an orphan: nothing to retire.
		default:
			p := filepath.Join(filepath.Dir(ref.Path), r.Name)
			if err := s.retireSidecar(p); err != nil {
				s.event("warn", fmt.Sprintf("%s: kept %s — %v", ref.Title, r.Name, err))
				continue
			}
			s.event("info", fmt.Sprintf("%s: replaced %s; the old file is in the recycle bin", ref.Title, r.Name))
		}
	}
}

// BulkSkip is one orphan a bulk action left alone, and why.
type BulkSkip struct {
	MovieID int64  `json:"movie_id"`
	Title   string `json:"title"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
}

// BulkResult is what a bulk adopt or replace did.
type BulkResult struct {
	Done    int        `json:"done"` // orphans adopted, or queued to be replaced
	Skipped []BulkSkip `json:"skipped"`
	Errors  []BulkSkip `json:"errors"`
}

// BulkOrphans adopts or replaces ("adopt" | "replace") the orphans of every movie the last
// library pass found with any (or of just movieIDs, when given). Adopt skips an orphan
// whose language its name doesn't say, one sharing its language with another orphan of
// the same movie (which is the right one is the owner's call), and one whose paired name
// is taken. Replace skips orphans in languages that aren't kept or are already covered,
// and forced ones.
func (s *Service) BulkOrphans(ctx context.Context, action string, movieIDs []int64) (BulkResult, error) {
	res := BulkResult{Skipped: []BulkSkip{}, Errors: []BulkSkip{}}
	if action != "adopt" && action != "replace" {
		return res, fmt.Errorf("unknown action %q", action)
	}
	want := map[int64]bool{}
	for _, id := range movieIDs {
		want[id] = true
	}
	list, _ := s.SnapshotMovies()
	for _, fs := range list {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if len(fs.Orphans) == 0 || (len(want) > 0 && !want[fs.MovieID]) {
			continue
		}
		m, err := s.movies.Get(ctx, fs.MovieID)
		if err != nil || !m.HasFile || m.MovieFilePath == "" {
			continue // gone since the pass
		}
		langs := s.languages(ctx)
		sc := scanSidecars(m.MovieFilePath, langs, "movie")
		skip := func(o Orphan, reason string) {
			res.Skipped = append(res.Skipped, BulkSkip{m.ID, m.Title, o.Name, reason})
		}
		if action == "adopt" {
			s.bulkAdopt(ctx, m, sc.Orphans, langs, &res, skip)
			continue
		}
		have := map[string]bool{}
		for _, p := range sc.Present {
			have[strings.ToLower(p)] = true
		}
		var retire []orphanRetire
		var names []Orphan
		for _, o := range sc.Orphans {
			tag, ok := orphanLang(o, langs)
			switch {
			case !coversFull(o.Variant):
				skip(o, "forced subtitles aren't replaced")
			case !ok || !isKept(tag, langs):
				skip(o, "not one of your kept languages")
			case have[tag]:
				skip(o, "the movie already has a paired "+tag+" subtitle")
			default:
				retire = append(retire, orphanRetire{Name: o.Name, Lang: tag})
				names = append(names, o)
			}
		}
		if len(retire) == 0 {
			continue
		}
		if _, err := s.queueReplace(m, retire); err != nil {
			for _, o := range names {
				res.Errors = append(res.Errors, BulkSkip{m.ID, m.Title, o.Name, err.Error()})
			}
			continue
		}
		res.Done += len(retire)
	}
	if res.Done > 0 {
		verb := "Adopted"
		if action == "replace" {
			verb = "Queued replacements for"
		}
		s.event("info", fmt.Sprintf("%s %d orphaned subtitle(s); %d skipped", verb, res.Done, len(res.Skipped)))
	}
	return res, nil
}

// bulkAdopt adopts one movie's unambiguous orphans.
func (s *Service) bulkAdopt(ctx context.Context, m movies.Movie, orphans []Orphan, langs []string, res *BulkResult, skip func(Orphan, string)) {
	type key struct{ lang, variant string }
	tags := make([]string, len(orphans))
	count := map[key]int{}
	for i, o := range orphans {
		if o.Lang == "" {
			continue
		}
		if tag, ok := sidecarLangTag(o.Lang, langs); ok {
			tags[i] = tag
			count[key{tag, o.Variant}]++
		}
	}
	for i, o := range orphans {
		tag := tags[i]
		switch {
		case o.Lang == "":
			skip(o, "its name doesn't say the language — adopt it on its own")
		case tag == "":
			skip(o, "its name doesn't say a language Arrmada knows")
		case count[key{tag, o.Variant}] > 1:
			skip(o, fmt.Sprintf("several orphaned %s subtitles — pick one", tag))
		default:
			_, err := s.adoptOne(ctx, m, o, tag, o.Variant)
			switch {
			case errors.Is(err, ErrOrphanTargetExists):
				skip(o, "the movie already has a paired "+tag+" subtitle")
			case err != nil:
				res.Errors = append(res.Errors, BulkSkip{m.ID, m.Title, o.Name, err.Error()})
			default:
				res.Done++
			}
		}
	}
}

// mergeRetire adds the orphans in more to have, once each.
func mergeRetire(have, more []orphanRetire) []orphanRetire {
	for _, r := range more {
		dup := false
		for _, h := range have {
			if h.Name == r.Name {
				dup = true
				break
			}
		}
		if !dup {
			have = append(have, r)
		}
	}
	return have
}
