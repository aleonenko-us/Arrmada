package movies

import (
	"context"
	"time"
)

// datesRereadEvery is how often the release dates of a film still settling are read again:
// TMDB often learns the digital date only weeks into the cinema run, and a grab is judged
// by what was read.
const datesRereadEvery = 3 * 24 * time.Hour

// Release dates stop being read again once settled: the home release more than settledFor
// ago, or — with none listed — a cinema release over a year ago (an older film whose home
// date TMDB never recorded won't get one now).
const (
	settledFor       = 30 * 24 * time.Hour
	noHomeSettledFor = 365 * 24 * time.Hour
)

// ReleaseDay parses a stored YYYY-MM-DD day (HomeReleaseDate, TheatricalDate); the zero
// time when it's empty or unreadable.
func ReleaseDay(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// DatesDue reports whether a movie's release dates should be read from TMDB: never read
// yet, or read more than datesRereadEvery ago while they may still change (see settledFor).
func DatesDue(m Movie, now time.Time) bool {
	if m.TMDBID <= 0 {
		return false
	}
	if m.Extra == nil || !m.Extra.DatesRead {
		return true
	}
	if read := ReleaseDay(m.Extra.DatesReadAt); !read.IsZero() && now.Sub(read) < datesRereadEvery {
		return false
	}
	if home := ReleaseDay(m.Extra.HomeReleaseDate); !home.IsZero() {
		return now.Sub(home) < settledFor
	}
	if th := ReleaseDay(m.Extra.TheatricalDate); !th.IsZero() {
		return now.Sub(th) < noHomeSettledFor
	}
	// Nothing listed at all: only a film from this year or last is worth asking again.
	return m.Year == 0 || m.Year >= now.Year()-1
}

// SetReleaseDates stores a movie's home and theatrical release days and marks them read
// today, leaving the rest of its extra metadata as it is (json_set, as SetTitles).
func (r *Repo) SetReleaseDates(ctx context.Context, id int64, home, theatrical string, now time.Time) error {
	_, err := r.q().ExecContext(ctx, `UPDATE movies SET extra_json = json_set(
			CASE WHEN json_valid(extra_json) AND extra_json <> '' THEN extra_json ELSE '{}' END,
			'$.home_release_date', ?, '$.theatrical_date', ?, '$.dates_read', json('true'), '$.dates_read_at', ?)
		WHERE id = ?`, home, theatrical, now.UTC().Format(time.DateOnly), id)
	return err
}
