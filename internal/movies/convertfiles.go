package movies

import (
	"context"
	"database/sql"
	"errors"

	"github.com/tristenlammi/arrmada/internal/outbox"
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

// RestoreConvertedFile puts a movie's record back on its original after Convert's Revert
// moved the original back from its hold: every track whose file is at outPath (the
// converted file; the same path for an MKV source) points at srcPath again, with the
// original's media info and release. Unless keepBaseline (the original was itself a
// conversion), the pre-conversion baseline is cleared — the file IS the original again —
// and the release restored is that baseline's; otherwise release (the ledger's) is used.
// The change goes out like a rename, so Convert's index, Subtitles and Plex follow it.
func (s *Service) RestoreConvertedFile(ctx context.Context, movieID int64, outPath, srcPath, release string, keepBaseline bool) (int, error) {
	versions, err := s.VersionRows(ctx, movieID)
	if err != nil {
		return 0, err
	}
	var info *MovieFile
	n := 0
	var first int64 = -1
	for _, v := range versions {
		if !v.HasFile || v.FilePath != outPath {
			continue
		}
		if info == nil {
			info = s.probeTrack(srcPath) // the original's own facts, read once
		}
		rel := v.ConvertedFromRelease
		if keepBaseline || rel == "" {
			rel = release
		}
		if rel == "" {
			rel = v.SourceRelease
		}
		if v.IsDefault {
			if err := s.setDefaultFileWith(ctx, movieID, srcPath, info); err != nil {
				return n, err
			}
			if err := s.repo.SetSourceRelease(ctx, movieID, rel); err != nil {
				return n, err
			}
		} else {
			size := int64(0)
			if info != nil && !info.Missing {
				size = info.SizeBytes
			}
			if err := s.repo.SetVersionFile(ctx, v.ID, srcPath, size, mediaJSONOf(info)); err != nil {
				return n, err
			}
			if err := s.repo.SetVersionSourceRelease(ctx, v.ID, rel); err != nil {
				return n, err
			}
		}
		if !keepBaseline {
			if err := s.repo.ClearConvertedFrom(ctx, movieID, v.ID); err != nil {
				return n, err
			}
		}
		if first < 0 {
			first = v.ID
		}
		n++
	}
	if n > 0 {
		change := outbox.ChangeRenamed
		if outPath == srcPath {
			change = outbox.ChangeDetected // same name, different file
		}
		s.enqueueChange(ctx, outbox.MovieChanged{MovieID: movieID, VersionID: first, Change: change, OldPath: outPath, Path: srcPath})
		s.log.Info("movie: conversion reverted — the record points at the original again", "movie_id", movieID, "path", srcPath, "records", n)
	}
	return n, nil
}
