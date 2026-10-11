package series

import (
	"context"
	"database/sql"
	"errors"
)

// EpisodeFileOwner reports which show has path recorded as an episode's file (ok false
// when none does). Convert asks before it puts a converted file at a name another record
// may own.
func (s *Service) EpisodeFileOwner(ctx context.Context, path string) (seriesID int64, ok bool, err error) {
	if path == "" {
		return 0, false, nil
	}
	err = s.repo.db.QueryRowContext(ctx,
		`SELECT series_id FROM episodes WHERE has_file = 1 AND file_path = ? LIMIT 1`, path).Scan(&seriesID)
	switch {
	case err == nil:
		return seriesID, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	}
	return 0, false, err
}
