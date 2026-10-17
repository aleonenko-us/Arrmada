package series

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/displaytitle"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The title a show is SHOWN by (Settings → Library → "Show titles as", and the per-show
// "Display title…" override) is presentation only, as for movies (movies/display.go): the
// override and display_title — the title shown now — are columns of their own, worked out
// here. Matching (FitRelease, MatchRelease, SearchFit) and naming (folders, episode files,
// renames) never read either: they keep using Title.

// ErrBadOverride is a display-title override that is too long or has control characters.
var ErrBadOverride = errors.New("a display title is at most 200 characters, on one line")

// SetDisplayPolicy installs the "Show titles as" setting, read on every use. Call it at
// startup; without it every show shows its canonical title (or its override).
func (s *Service) SetDisplayPolicy(fn func() displaytitle.Policy) { s.displayPolicy = fn }

// policy is the display setting in effect now.
func (s *Service) policy() displaytitle.Policy {
	if s.displayPolicy == nil {
		return displaytitle.Policy{Mode: displaytitle.ModeEnglish}
	}
	return s.displayPolicy()
}

// shownTitle is the stored display_title, or the canonical title when it's '' (the
// default, and rows not worked out yet).
func shownTitle(display, title string) string {
	if strings.TrimSpace(display) != "" {
		return display
	}
	return title
}

// Shown is the title the app shows for sr: DisplayTitle, or Title for a show built in
// memory rather than read from the database. Never for matching or naming.
func (sr Series) Shown() string { return shownTitle(sr.DisplayTitle, sr.Title) }

// displayFor works out the title sr shows under p. A show's stored original title is
// already in its own script, so it shows even before the display names are read.
func displayFor(p displaytitle.Policy, sr Series) string {
	t := displaytitle.Title{Canonical: sr.Title, Override: sr.TitleOverride}
	if sr.Extra != nil {
		t.OriginalTitle = sr.Extra.OriginalTitle
		t.OriginalLanguage = sr.Extra.OriginalLanguage
		t.Names = sr.Extra.DisplayNames
	}
	return p.Resolve(t)
}

// storedDisplay is what goes in display_title for a shown title: '' when it is the
// canonical title, so the default setting stores nothing.
func storedDisplay(shown, title string) string {
	if shown == title {
		return ""
	}
	return shown
}

// syncDisplay works out one show's shown title again and stores it. Best effort: a
// failure leaves the previous title shown until the next start (SyncDisplayTitles).
func (s *Service) syncDisplay(ctx context.Context, id int64) {
	sr, err := s.repo.Get(ctx, id)
	if err != nil {
		return
	}
	want := storedDisplay(displayFor(s.policy(), sr), sr.Title)
	if want == storedDisplay(sr.DisplayTitle, sr.Title) {
		return
	}
	if _, err := s.repo.db.ExecContext(ctx, `UPDATE series SET display_title = ? WHERE id = ?`, want, id); err != nil {
		s.log.Warn("series: couldn't store the display title", "series", sr.Title, "err", err)
	}
}

// SyncDisplayTitles works out every show's shown title again — at start and whenever the
// setting changes — and returns how many changed. It only reads the database.
func (s *Service) SyncDisplayTitles(ctx context.Context) (int, error) {
	all, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	p := s.policy()
	type change struct {
		id   int64
		want string
	}
	var changes []change
	for _, sr := range all {
		want := storedDisplay(displayFor(p, sr), sr.Title)
		if want != storedDisplay(sr.DisplayTitle, sr.Title) {
			changes = append(changes, change{sr.ID, want})
		}
	}
	if len(changes) == 0 {
		return 0, nil
	}
	err = store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		for _, c := range changes {
			if _, err := tx.ExecContext(ctx, `UPDATE series SET display_title = ? WHERE id = ?`, c.want, c.id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(changes), nil
}

// SetTitleOverride sets (or, with "", clears) a show's display-title override and returns
// the show as it now shows. A refresh never touches it: it is a column of its own.
func (s *Service) SetTitleOverride(ctx context.Context, id int64, override string) (Series, error) {
	clean, ok := displaytitle.CleanOverride(override)
	if !ok {
		return Series{}, ErrBadOverride
	}
	if _, err := s.repo.Get(ctx, id); err != nil {
		return Series{}, err
	}
	if _, err := s.repo.db.ExecContext(ctx, `UPDATE series SET title_override = ? WHERE id = ?`, clean, id); err != nil {
		return Series{}, err
	}
	s.syncDisplay(ctx, id)
	return s.Get(ctx, id)
}

// TitleChoices lists the names a show may be shown by, for the "Display title…" picker:
// its English and original titles and every translation TMDB has (through the titles
// cache; the stored names alone when TMDB can't be reached).
func (s *Service) TitleChoices(ctx context.Context, id int64) ([]displaytitle.Choice, error) {
	sr, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	names := &displaytitle.Names{}
	if sr.Extra != nil {
		if sr.Extra.DisplayNames != nil {
			names = sr.Extra.DisplayNames
		} else {
			names = &displaytitle.Names{Original: sr.Extra.OriginalTitle, OriginalLanguage: sr.Extra.OriginalLanguage}
		}
	}
	var all *metadata.Titles
	if s.titles != nil && sr.TMDBID > 0 {
		if t, err := s.titles.Titles(ctx, metadata.TitlesSeries, sr.TMDBID); err == nil {
			all = t
		} else {
			s.log.Debug("series: couldn't read every title for the picker; offering the stored ones", "series", sr.Title, "err", err)
		}
	}
	return displaytitle.Choices(sr.Title, names, all), nil
}

// needsDisplayNames reports whether sr's display names haven't been read for what p
// shows. The default setting shows only the canonical title, so it reads nothing.
func needsDisplayNames(sr Series, p displaytitle.Policy) bool {
	if p.Default() || sr.TMDBID <= 0 {
		return false
	}
	return sr.Extra == nil || !sr.Extra.DisplayNames.Covers(p)
}

// refreshDisplayNames reads and stores a show's display names for the setting now, on add
// and refresh, and works out its shown title again either way (the title itself may have
// changed). When the names can't be read the stored ones stay, and the titles backfill
// tries again.
func (s *Service) refreshDisplayNames(ctx context.Context, id int64, tmdbID int, title string) {
	defer s.syncDisplay(ctx, id)
	p := s.policy()
	if p.Default() || s.titles == nil || tmdbID <= 0 {
		return
	}
	t, err := s.titles.Titles(ctx, metadata.TitlesSeries, tmdbID)
	if err != nil {
		s.log.Debug("series: couldn't read display titles; the titles backfill tries again", "series", title, "err", err)
		return
	}
	if err := s.repo.SetDisplayNames(ctx, id, displaytitle.FromTitles(t, p.TranslationLangs())); err != nil {
		s.log.Warn("series: couldn't store display titles", "series", title, "err", err)
	}
}

// backfillDisplayOnce reads the display names of every show not yet read for the setting
// now, one show per every so a large library never bursts TMDB, and returns how many it
// filled. It stops when TMDB isn't configured or ctx ends; a show TMDB can't answer right
// now is left for the next run.
func (s *Service) backfillDisplayOnce(ctx context.Context, every time.Duration) (int, error) {
	p := s.policy()
	if p.Default() || s.titles == nil {
		return 0, nil
	}
	all, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	filled := 0
	for _, sr := range all {
		if !needsDisplayNames(sr, p) {
			continue
		}
		t := time.NewTimer(every)
		select {
		case <-ctx.Done():
			t.Stop()
			return filled, ctx.Err()
		case <-t.C:
		}
		got, err := s.titles.Titles(ctx, metadata.TitlesSeries, sr.TMDBID)
		var names *displaytitle.Names
		switch {
		case errors.Is(err, metadata.ErrNotConfigured):
			return filled, nil
		case errors.Is(err, metadata.ErrNotFound):
			names = displaytitle.FromTitles(nil, p.TranslationLangs()) // read, nothing to show
		case err != nil:
			if ctx.Err() != nil {
				return filled, ctx.Err()
			}
			s.log.Debug("series: couldn't read display titles; trying again next run", "series", sr.Title, "err", err)
			continue
		default:
			names = displaytitle.FromTitles(got, p.TranslationLangs())
		}
		if err := s.repo.SetDisplayNames(ctx, sr.ID, names); err != nil {
			return filled, err
		}
		s.syncDisplay(ctx, sr.ID)
		filled++
	}
	return filled, nil
}

// SetDisplayNames stores a show's display names, leaving the rest of its extra metadata as
// it is (json_set, as SetLocalTitles).
func (r *Repo) SetDisplayNames(ctx context.Context, id int64, n *displaytitle.Names) error {
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE series SET extra_json = json_set(
			CASE WHEN json_valid(extra_json) AND extra_json <> '' THEN extra_json ELSE '{}' END,
			'$.display_names', json(?))
		WHERE id = ?`, string(b), id)
	return err
}
