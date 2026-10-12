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

// RestoreConvertedFile puts every episode whose file is at outPath (Convert's converted
// file — several for a multi-episode file) back on srcPath after Convert's Revert moved the
// original back, with the original's size and release. Unless keepBaseline (the original
// was itself a conversion), the pre-conversion baseline is cleared and the release restored
// is that baseline's; otherwise release (the ledger's) is used. Returns how many episodes
// moved.
func (s *Service) RestoreConvertedFile(ctx context.Context, seriesID int64, outPath, srcPath string, size int64, release string, keepBaseline bool) (int64, error) {
	keep := 0
	if keepBaseline {
		keep = 1
	}
	res, err := s.repo.db.ExecContext(ctx,
		`UPDATE episodes SET has_file = 1, file_path = ?, size_bytes = ?,
		   source_release = CASE
		     WHEN ? = 0 AND converted_from_release <> '' THEN converted_from_release
		     WHEN ? <> '' THEN ?
		     ELSE source_release END,
		   converted_from_release = CASE WHEN ? = 0 THEN '' ELSE converted_from_release END,
		   converted_from_size = CASE WHEN ? = 0 THEN 0 ELSE converted_from_size END
		 WHERE series_id = ? AND has_file = 1 AND file_path = ?`,
		srcPath, size, keep, release, release, keep, keep, seriesID, outPath)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 && s.log != nil {
		s.log.Info("series: conversion reverted — the episodes point at the original again", "series_id", seriesID, "path", srcPath, "episodes", n)
	}
	return n, nil
}
