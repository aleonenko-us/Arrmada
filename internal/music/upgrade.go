package music

import (
	"context"
	"database/sql"
	"errors"
)

// The music upgrade sweep's backoff (MUS-16), kept per album apart from the missing-album
// search's: albums.last_upgrade_search_at and upgrade_misses.

// UpgradeState is the upgrade sweep's record of one album: when it last looked for a
// better copy ("" = never) and how many looks in a row found nothing better.
type UpgradeState struct {
	LastAt string
	Misses int
}

// UpgradeStates reads every album's upgrade backoff, keyed by album id. Albums never
// searched for an upgrade are present with a zero state.
func (r *Repo) UpgradeStates(ctx context.Context) (map[int64]UpgradeState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, last_upgrade_search_at, upgrade_misses FROM albums`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UpgradeState{}
	for rows.Next() {
		var id int64
		var last sql.NullString
		var st UpgradeState
		if err := rows.Scan(&id, &last, &st.Misses); err != nil {
			return nil, err
		}
		st.LastAt = last.String
		out[id] = st
	}
	return out, rows.Err()
}

// UpgradeStateOf reads one album's upgrade backoff.
func (r *Repo) UpgradeStateOf(ctx context.Context, albumID int64) (UpgradeState, error) {
	var last sql.NullString
	var st UpgradeState
	err := r.db.QueryRowContext(ctx, `SELECT last_upgrade_search_at, upgrade_misses FROM albums WHERE id = ?`, albumID).
		Scan(&last, &st.Misses)
	if errors.Is(err, sql.ErrNoRows) {
		return st, ErrNotFound
	}
	st.LastAt = last.String
	return st, err
}

// RecordUpgradeMiss stamps the upgrade search and counts one more that found nothing better.
func (r *Repo) RecordUpgradeMiss(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE albums SET last_upgrade_search_at = CURRENT_TIMESTAMP, upgrade_misses = upgrade_misses + 1 WHERE id = ?`, id)
	return err
}

// ResetUpgradeMisses stamps the upgrade search and clears its backoff (an upgrade was
// grabbed: the next look comes soon, for the tracks it may not have covered).
func (r *Repo) ResetUpgradeMisses(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE albums SET last_upgrade_search_at = CURRENT_TIMESTAMP, upgrade_misses = 0 WHERE id = ?`, id)
	return err
}

// TouchUpgradeSearch stamps the upgrade search without counting a miss: it failed for a
// reason of our own, and the album goes to the back of the line.
func (r *Repo) TouchUpgradeSearch(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE albums SET last_upgrade_search_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// UpgradeStates reads every album's upgrade backoff (see Repo.UpgradeStates).
func (s *Service) UpgradeStates(ctx context.Context) (map[int64]UpgradeState, error) {
	return s.repo.UpgradeStates(ctx)
}

// UpgradeStateOf reads one album's upgrade backoff.
func (s *Service) UpgradeStateOf(ctx context.Context, albumID int64) (UpgradeState, error) {
	return s.repo.UpgradeStateOf(ctx, albumID)
}

// RecordUpgradeMiss counts an upgrade search that found nothing better.
func (s *Service) RecordUpgradeMiss(ctx context.Context, albumID int64) error {
	return s.repo.RecordUpgradeMiss(ctx, albumID)
}

// ResetUpgradeMisses clears an album's upgrade backoff after an upgrade grab.
func (s *Service) ResetUpgradeMisses(ctx context.Context, albumID int64) error {
	return s.repo.ResetUpgradeMisses(ctx, albumID)
}

// TouchUpgradeSearch stamps an upgrade search that failed on our side.
func (s *Service) TouchUpgradeSearch(ctx context.Context, albumID int64) error {
	return s.repo.TouchUpgradeSearch(ctx, albumID)
}
