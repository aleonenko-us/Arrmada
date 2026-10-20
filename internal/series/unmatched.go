package series

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The needs-you list of the series scan lives in library_unmatched, shared with movies, so a
// restart doesn't empty it and a folder the scan skipped leaves a trace with its reason.

// mediaSeries is this module's key in library_unmatched.
const mediaSeries = "series"

// replaceUnmatched records a scan's unmatched folders. replaced are the TV folders the scan
// read to the end: every row the media had under them is dropped first, since the list is
// what the newest scan saw there. nil replaced (a scan that stopped early) only adds.
func (r *Repo) replaceUnmatched(ctx context.Context, list []UnmatchedFolder, replaced []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return store.WithTx(ctx, r.db, func(tx *sql.Tx) error {
		if len(replaced) > 0 {
			args := []any{mediaSeries}
			marks := make([]string, 0, len(replaced))
			for _, k := range replaced {
				marks = append(marks, "?")
				args = append(args, filepath.Clean(k))
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM library_unmatched WHERE media = ? AND root IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
				return err
			}
		}
		for _, u := range list {
			cands := u.Candidates
			if cands == nil {
				cands = []metadata.SeriesResult{}
			}
			b, err := json.Marshal(cands)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_unmatched (media, root, folder, title, year, candidates_json, scanned_at, reason, detail, series_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(media, root, folder) DO UPDATE SET title = excluded.title, year = excluded.year,
					candidates_json = excluded.candidates_json, scanned_at = excluded.scanned_at,
					reason = excluded.reason, detail = excluded.detail, series_id = excluded.series_id`,
				mediaSeries, filepath.Clean(u.Root), u.Folder, u.Title, u.Year, string(b), now, u.Reason, u.Detail, u.SeriesID); err != nil {
				return err
			}
		}
		return nil
	})
}

// unmatched lists the series needs-you folders under any of roots, by folder name.
func (r *Repo) unmatched(ctx context.Context, roots []string) ([]UnmatchedFolder, error) {
	out := []UnmatchedFolder{}
	if len(roots) == 0 {
		return out, nil
	}
	args := []any{mediaSeries}
	marks := make([]string, 0, len(roots))
	for _, root := range roots {
		args = append(args, filepath.Clean(root))
		marks = append(marks, "?")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT folder, root, title, year, candidates_json, reason, detail, series_id FROM library_unmatched
		WHERE media = ? AND root IN (`+strings.Join(marks, ",")+`) ORDER BY folder COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			u     UnmatchedFolder
			cands string
		)
		if err := rows.Scan(&u.Folder, &u.Root, &u.Title, &u.Year, &cands, &u.Reason, &u.Detail, &u.SeriesID); err != nil {
			return nil, err
		}
		if cands != "" {
			_ = json.Unmarshal([]byte(cands), &u.Candidates)
		}
		if u.Candidates == nil {
			u.Candidates = []metadata.SeriesResult{}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) dropUnmatched(ctx context.Context, root, folder string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM library_unmatched WHERE media = ? AND root = ? AND folder = ?`, mediaSeries, filepath.Clean(root), folder)
	return err
}

// recordUnmatched saves a scan's list; it runs after the scan's context may have ended, so
// it has a short budget of its own.
func (s *Service) recordUnmatched(ctx context.Context, list []UnmatchedFolder, replaced []string) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.repo.replaceUnmatched(wctx, list, replaced); err != nil {
		s.log.Warn("series scan: couldn't save the folders that need you", "err", err)
	}
}

// LastUnmatched returns the folders scans of the TV folders left needing you, each with
// why and the candidates offered. It survives restarts.
func (s *Service) LastUnmatched(ctx context.Context) ([]UnmatchedFolder, error) {
	return s.repo.unmatched(ctx, s.libRoots())
}

// dropUnmatched takes one folder of one TV folder (root) off the list.
func (s *Service) dropUnmatched(ctx context.Context, root, folder string) {
	if err := s.repo.dropUnmatched(ctx, root, folder); err != nil {
		s.log.Warn("series scan: couldn't clear a settled folder from the list", "folder", folder, "err", err)
	}
}
