package auth

import (
	"context"
	"database/sql"
	"errors"
)

// Send to Kindle: each person's own Kindle address. Only that person reads or changes it
// (GET/PUT /api/v1/me/kindle); it's never listed with other accounts.

// KindleEmail is the account's Send-to-Kindle address, "" when none is set.
func (s *Service) KindleEmail(ctx context.Context, id int64) (string, error) {
	var addr string
	err := s.db.QueryRowContext(ctx, `SELECT kindle_email FROM users WHERE id = ?`, id).Scan(&addr)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return addr, err
}

// SetKindleEmail saves the account's Send-to-Kindle address ("" clears it). Checking the
// address is the caller's job.
func (s *Service) SetKindleEmail(ctx context.Context, id int64, addr string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET kindle_email = ? WHERE id = ?`, addr, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
