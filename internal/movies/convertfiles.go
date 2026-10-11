package movies

import (
	"context"
	"database/sql"
	"errors"
)

// FileOwner reports which movie has path recorded as one of its files: the movie, and the
// extra version holding it (0 for the default track). ok is false when no movie does.
// Convert asks before it puts a converted file at a name another record may own.
func (s *Service) FileOwner(ctx context.Context, path string) (movieID, versionID int64, ok bool, err error) {
	if path == "" {
		return 0, 0, false, nil
	}
	err = s.repo.db.QueryRowContext(ctx,
		`SELECT id FROM movies WHERE has_file = 1 AND movie_file_path = ? LIMIT 1`, path).Scan(&movieID)
	switch {
	case err == nil:
		return movieID, 0, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, 0, false, err
	}
	err = s.repo.db.QueryRowContext(ctx,
		`SELECT movie_id, id FROM movie_versions WHERE has_file = 1 AND file_path = ? LIMIT 1`, path).Scan(&movieID, &versionID)
	switch {
	case err == nil:
		return movieID, versionID, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return 0, 0, false, nil
	}
	return 0, 0, false, err
}
