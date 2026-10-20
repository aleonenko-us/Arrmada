package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// First-run setup. A fresh install has no admin, and the wizard that configures it runs
// before one is made (the account is the wizard's last step). Its calls are authorised by
// a setup session: a random token whose hash lives in setup_sessions.
//
// Everything that matters is decided in SQL, in one statement, so two browsers racing at
// first run cannot both win:
//   - a setup session is only ever inserted while no admin exists;
//   - a setup session only validates while no admin exists (checked on every request, not
//     just when it was issued) and before its expiry;
//   - the first admin is only ever inserted while no admin exists.
//
// Creating the admin also sweeps the table, though validation would already refuse them.

// SetupSessionTTL is how long a setup session lasts. The wizard is a few minutes of work;
// two hours leaves room for a slow Plex sign-in or a phone call, and no more.
const SetupSessionTTL = 2 * time.Hour

// setupSessionMax caps live setup sessions so a loop on the LAN can't grow the table: the
// oldest goes first.
const setupSessionMax = 20

// ErrSetupClosed: an admin already exists, so first-run setup is over.
var ErrSetupClosed = errors.New("setup already complete")

// CreateSetupSession issues a setup session and returns its raw token (shown once; only
// the hash is stored) and expiry. ErrSetupClosed when an admin exists.
func (s *Service) CreateSetupSession(ctx context.Context) (string, time.Time, error) {
	raw, err := randToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	exp := now.Add(SetupSessionTTL)
	// Old and surplus rows first; best effort (the insert below is what must be right).
	_, _ = s.db.ExecContext(ctx, `DELETE FROM setup_sessions WHERE expires_at <= ?`, sqlTime(now))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM setup_sessions WHERE token_hash IN (
		SELECT token_hash FROM setup_sessions ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, setupSessionMax-1)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO setup_sessions (token_hash, created_at, expires_at)
		 SELECT ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM users WHERE role = ?)`,
		hashToken(raw), sqlTime(now), sqlTime(exp), string(RoleAdmin))
	if err != nil {
		return "", time.Time{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", time.Time{}, ErrSetupClosed
	}
	return raw, exp, nil
}

// SetupSessionValid reports whether raw is a live setup session: known, unexpired, and no
// admin exists. A database error counts as "not valid" (fail closed).
func (s *Service) SetupSessionValid(ctx context.Context, raw string) bool {
	if raw == "" {
		return false
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM setup_sessions
		 WHERE token_hash = ? AND expires_at > ?
		   AND NOT EXISTS (SELECT 1 FROM users WHERE role = ?)`,
		hashToken(raw), sqlTime(s.now()), string(RoleAdmin)).Scan(&n)
	return err == nil && n > 0
}

// DeleteSetupSessions removes every setup session.
func (s *Service) DeleteSetupSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM setup_sessions`)
	return err
}

// CreateFirstAdmin makes the first admin with a password, only if no admin exists (one
// statement, so a second caller at the same moment gets ErrSetupClosed). It leaves every
// setup session dead.
func (s *Service) CreateFirstAdmin(ctx context.Context, username, password string) (*User, error) {
	u, err := s.createUser(ctx, username, password, RoleAdmin, true, true)
	if err != nil {
		return nil, err
	}
	_ = s.DeleteSetupSessions(ctx)
	return u, nil
}

// ClaimFirstAdminViaLink makes the first admin out of a Plex or Jellyfin sign-in the caller
// has already verified as the media server's owner: a passwordless account linked to that
// server user, only if no admin exists. kind is "plex" or "jellyfin". A server user who
// already has an account here is refused with ErrUserExists, never promoted.
func (s *Service) ClaimFirstAdminViaLink(ctx context.Context, kind, extID, extName string) (*User, error) {
	var k linkKind
	switch kind {
	case "plex":
		k = plexLink
	case "jellyfin":
		k = jellyfinLink
	default:
		return nil, errors.New("unknown media server")
	}
	extID, extName = strings.TrimSpace(extID), strings.TrimSpace(extName)
	if extID == "" {
		return nil, errors.New("missing " + strings.ToLower(k.server) + " id")
	}
	if _, err := s.userWhere(ctx, k.idCol+" = ?", extID); err == nil {
		return nil, ErrUserExists
	} else if !errors.Is(err, ErrInvalidCredentials) {
		return nil, err
	}
	pw := make([]byte, 24) // unusable password: this account signs in through the server
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword(pw, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	username := s.uniqueUsername(ctx, extName, k.fallbackName)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, auto_approve, `+autoApproveCols+`, `+k.idCol+`, `+k.nameCol+`, password_login)
		 SELECT ?, ?, ?, 1, 1, 1, 1, ?, ?, 0 WHERE NOT EXISTS (SELECT 1 FROM users WHERE role = ?)`,
		username, string(hash), string(RoleAdmin), extID, extName, string(RoleAdmin))
	if isUniqueViolation(err) {
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrSetupClosed
	}
	id, _ := res.LastInsertId()
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = s.DeleteSetupSessions(ctx)
	return u, nil
}
