package auth

import (
	"context"
	"time"
)

// "Trust this device for 30 days": after a right code, the browser can be remembered so
// the next sign-ins there ask for the password only. It's a random token in an HttpOnly
// cookie, stored hashed like a session token and tied to one account, so it can't skip
// the code for anyone else and is useless without that account's password. It never
// slides: thirty days after the code, the code is asked for again.
//
// Turning two-factor off (or an admin's reset), changing or resetting the password,
// disabling the account and signing out everywhere forget an account's trusted devices.

// TrustedDeviceTTL is how long a trusted device skips the code.
const TrustedDeviceTTL = 30 * 24 * time.Hour

// TrustDevice remembers a browser for userID and returns the raw token for its cookie.
func (s *Service) TrustDevice(ctx context.Context, userID int64) (string, time.Time, error) {
	raw, err := randToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	exp := now.Add(TrustedDeviceTTL)
	// Expired rows go as new ones arrive; nothing else needs to sweep them.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM user_trusted_devices WHERE expires_at <= ?`, sqlTime(now))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO user_trusted_devices (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(raw), userID, sqlTime(exp)); err != nil {
		return "", time.Time{}, err
	}
	return raw, exp, nil
}

// DeviceTrusted reports whether raw is a live trusted-device token for userID.
func (s *Service) DeviceTrusted(ctx context.Context, userID int64, raw string) bool {
	if raw == "" {
		return false
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_trusted_devices WHERE token_hash = ? AND user_id = ? AND expires_at > ?`,
		hashToken(raw), userID, sqlTime(s.now())).Scan(&n)
	return err == nil && n > 0
}

// ForgetTrustedDevices forgets userID's trusted devices except keepRaw's (the browser
// asking; "" keeps none) and says how many were forgotten.
func (s *Service) ForgetTrustedDevices(ctx context.Context, userID int64, keepRaw string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_trusted_devices WHERE user_id = ? AND token_hash <> ?`, userID, keepHash(keepRaw))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
