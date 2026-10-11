package movies

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The needs-review list: folders a library scan couldn't confidently identify. It lives in
// library_unmatched (media-agnostic, so series can share it) rather than in memory, so a
// restart doesn't empty it and force another full scan to get it back.

// mediaMovie is this module's key in library_unmatched.
const mediaMovie = "movie"

// replaceUnmatched records a scan's unmatched folders under root. A finished scan
// (complete) replaces every row the media had — the list is what the newest scan saw — in
// one transaction; a scan that stopped early only adds what it found, so the folders it
// never reached keep their rows.
func (r *Repo) replaceUnmatched(ctx context.Context, media, root string, list []UnmatchedFolder, complete bool) error {
	root = filepath.Clean(root)
	now := time.Now().UTC().Format(time.RFC3339)
	return r.inTx(ctx, func(tx *sql.Tx, _ *Repo) error {
		if complete {
			if _, err := tx.ExecContext(ctx, `DELETE FROM library_unmatched WHERE media = ?`, media); err != nil {
				return err
			}
		}
		for _, u := range list {
			cands := u.Candidates
			if cands == nil {
				cands = []metadata.MovieResult{}
			}
			b, err := json.Marshal(cands)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_unmatched (media, root, folder, title, year, candidates_json, scanned_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(media, folder) DO UPDATE SET root = excluded.root, title = excluded.title, year = excluded.year,
					candidates_json = excluded.candidates_json, scanned_at = excluded.scanned_at`,
				media, root, u.Folder, u.Title, u.Year, string(b), now); err != nil {
				return err
			}
		}
		return nil
	})
}

// unmatched lists the media's unmatched folders under root, by folder name. Rows a scan of
// a different root left behind (the folder setting changed since) aren't shown: they can't
// be imported from the folder in use now.
func (r *Repo) unmatched(ctx context.Context, media, root string) ([]UnmatchedFolder, error) {
	rows, err := r.q().QueryContext(ctx, `SELECT folder, title, year, candidates_json FROM library_unmatched
		WHERE media = ? AND root = ? ORDER BY folder COLLATE NOCASE`, media, filepath.Clean(root))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnmatchedFolder{}
	for rows.Next() {
		var (
			u     UnmatchedFolder
			cands string
		)
		if err := rows.Scan(&u.Folder, &u.Title, &u.Year, &cands); err != nil {
			return nil, err
		}
		if cands != "" {
			_ = json.Unmarshal([]byte(cands), &u.Candidates)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// dropUnmatched removes one folder from the media's list (a manual pick settled it).
func (r *Repo) dropUnmatched(ctx context.Context, media, folder string) error {
	_, err := r.q().ExecContext(ctx, `DELETE FROM library_unmatched WHERE media = ? AND folder = ?`, media, folder)
	return err
}

// recordUnmatched saves a scan's unmatched folders. It runs after the scan's own context
// may have ended (a timeout, a cancel), so it gets a short budget of its own: the folders
// a stopped scan did look at are still worth keeping.
func (s *Service) recordUnmatched(ctx context.Context, root string, list []UnmatchedFolder, complete bool) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.repo.replaceUnmatched(wctx, mediaMovie, root, list, complete); err != nil {
		s.log.Warn("library scan: couldn't save the folders that need review", "err", err)
	}
}

// LastUnmatched returns the folders scans of the movies folder couldn't identify, each with
// the candidates offered for a manual pick. The list survives restarts.
func (s *Service) LastUnmatched(ctx context.Context) ([]UnmatchedFolder, error) {
	return s.repo.unmatched(ctx, mediaMovie, s.libRoot())
}
