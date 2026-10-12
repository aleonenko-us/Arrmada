package books

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The search ladder: how long the missing-books sweep leaves a book alone after it
// came up empty. Books used to get two tries and then never again, so anything uploaded
// a week after the request only arrived if someone pressed Search. A wanted book is now
// never dropped; it is just asked about less often the longer nothing turns up, which
// keeps the load on private trackers bounded.
const (
	// MonthlyAfter is the miss count past which a book is checked once a month.
	MonthlyAfter = 10
)

// SearchWait is how long to wait after a book's last search, given how many searches
// in a row found nothing: straight away, a day, three days, weekly, then monthly.
func SearchWait(misses int) time.Duration {
	switch {
	case misses <= 0:
		return 0
	case misses == 1:
		return 24 * time.Hour
	case misses == 2:
		return 72 * time.Hour
	case misses <= MonthlyAfter:
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

// NextSearchAt is when the sweep may next search a book last searched at lastAt (as
// stored, UTC) after misses empty searches. The zero time means now: never searched, or
// no misses to wait out.
func NextSearchAt(lastAt string, misses int) time.Time {
	wait := SearchWait(misses)
	last := parseStamp(lastAt)
	if wait == 0 || last.IsZero() {
		return time.Time{}
	}
	return last.Add(wait)
}

// Release day. A book requested before it is published used to be searched at add time
// and then on the ladder, so its misses piled up against a book that didn't exist yet
// and it was on a weekly or monthly check by the time it came out. With the day known:
// no sweep search before it; on the first sweep from release day the misses are reset
// and it is searched; for ReleaseWindowDays after that it waits at most a day between
// searches; then the ordinary ladder takes over.

// ReleaseWindowDays is how long after release a book is searched at least daily.
const ReleaseWindowDays = 14

// releaseLayout is how release dates are stored.
const releaseLayout = "2006-01-02"

// cleanReleaseDate keeps a well-formed YYYY-MM-DD and drops anything else.
func cleanReleaseDate(s string) string {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(releaseLayout, s); err != nil {
		return ""
	}
	return s
}

// ReleaseDay is the start of a release date in the server's time zone (the owner's day,
// when TZ is set), or the zero time for "" or anything unreadable.
func ReleaseDay(date string) time.Time {
	t, err := time.ParseInLocation(releaseLayout, strings.TrimSpace(date), time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Released reports whether a book with this release date is out at now. A book with no
// known date counts as out.
func Released(date string, now time.Time) bool {
	day := ReleaseDay(date)
	return day.IsZero() || !now.Before(day)
}

// ReleasedSinceLastSearch reports whether the book came out after its last search (or
// was never searched) — the sweep's cue to reset its misses and look straight away.
func ReleasedSinceLastSearch(lastAt, date string, now time.Time) bool {
	day := ReleaseDay(date)
	if day.IsZero() || now.Before(day) {
		return false
	}
	last := parseStamp(lastAt)
	return last.IsZero() || last.Before(day)
}

// NextSearchAtFor is NextSearchAt that knows the release date: the release day itself
// for an upcoming book, now (the zero time) once it is out and hasn't been searched
// since, at most a day after the last search during the release window, and the
// ordinary ladder otherwise. The sweep, the book page and request tracking all read it,
// so they agree on when the next look is.
func NextSearchAtFor(lastAt string, misses int, date string, now time.Time) time.Time {
	day := ReleaseDay(date)
	if day.IsZero() {
		return NextSearchAt(lastAt, misses)
	}
	if now.Before(day) {
		return day
	}
	if ReleasedSinceLastSearch(lastAt, date, now) {
		return time.Time{}
	}
	next := NextSearchAt(lastAt, misses)
	if now.Before(day.AddDate(0, 0, ReleaseWindowDays)) {
		if daily := parseStamp(lastAt).Add(24 * time.Hour); !next.IsZero() && next.After(daily) {
			return daily
		}
	}
	return next
}

// ResetForRelease clears a book's misses when it comes out, so the searches that found
// nothing before it existed don't slow the searches that matter. Unlike
// ResetSearchMisses it doesn't stamp a search: one hasn't happened yet.
func (r *Repo) ResetForRelease(ctx context.Context, bookID int64) {
	_, _ = r.db.ExecContext(ctx, `UPDATE books SET search_misses = 0 WHERE id = ?`, bookID)
}

// ResetForRelease is the repo's ResetForRelease, for the sweep.
func (s *Service) ResetForRelease(ctx context.Context, bookID int64) {
	s.repo.ResetForRelease(ctx, bookID)
}

// SetReleaseDate stores a book's release date when the catalogue gives one; a lookup
// that knows none leaves the stored date alone.
func (r *Repo) SetReleaseDate(ctx context.Context, bookID int64, date string) error {
	date = cleanReleaseDate(date)
	if date == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE books SET release_date = ? WHERE id = ?`, date, bookID)
	return err
}

// parseStamp reads a stored SQLite timestamp; an empty or unreadable one is the zero time.
func parseStamp(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// SearchState is where one book stands on the ladder.
type SearchState struct {
	LastAt string // when the sweep last searched it ("" = never)
	Misses int    // searches in a row that found nothing
}

// SearchStates returns every book's search state in one query, for the sweep.
func (r *Repo) SearchStates(ctx context.Context) (map[int64]SearchState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, last_search_at, search_misses FROM books`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]SearchState{}
	for rows.Next() {
		var id int64
		var last sql.NullString
		var misses int
		if err := rows.Scan(&id, &last, &misses); err != nil {
			return nil, err
		}
		out[id] = SearchState{LastAt: last.String, Misses: misses}
	}
	return out, rows.Err()
}

// SearchStates is the repo's SearchStates, for the sweep.
func (s *Service) SearchStates(ctx context.Context) (map[int64]SearchState, error) {
	return s.repo.SearchStates(ctx)
}
