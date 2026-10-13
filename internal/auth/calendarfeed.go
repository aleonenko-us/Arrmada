package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Calendar feed links. Each person may have one private link to their Calendar as an
// iCal feed, which Google or Apple Calendar fetches without signing in. The link carries
// 32 random bytes; only their SHA-256 is stored, so the token can't be read back from the
// database, a backup or a support bundle — the person sees it once, when it's made.
// Making a new link replaces the old one, which stops working straight away; turning it
// off deletes it. The token is good for nothing but the feed: it isn't a session or an
// API key and no other route reads it.

// calendarTokenBytes is the token's entropy: 256 bits.
const calendarTokenBytes = 32

// CalendarFeed is whether a person has a feed link, and since when.
type CalendarFeed struct {
	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at,omitempty"`
}

func hashCalendarToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CalendarFeedFor says whether userID has a feed link.
func (s *Service) CalendarFeedFor(ctx context.Context, userID int64) (CalendarFeed, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM calendar_feeds WHERE user_id = ?`, userID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return CalendarFeed{}, nil
	}
	if err != nil {
		return CalendarFeed{}, err
	}
	return CalendarFeed{Enabled: true, CreatedAt: at}, nil
}

// RotateCalendarFeed makes a new feed token for userID, replacing any earlier one, and
// returns it. This is the only time the token exists outside the caller's link.
func (s *Service) RotateCalendarFeed(ctx context.Context, userID int64) (string, CalendarFeed, error) {
	b := make([]byte, calendarTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", CalendarFeed{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := s.now().Unix()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO calendar_feeds (user_id, token_hash, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET token_hash = excluded.token_hash, created_at = excluded.created_at`,
		userID, hashCalendarToken(token), now); err != nil {
		return "", CalendarFeed{}, err
	}
	return token, CalendarFeed{Enabled: true, CreatedAt: now}, nil
}

// RevokeCalendarFeed turns userID's feed link off.
func (s *Service) RevokeCalendarFeed(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM calendar_feeds WHERE user_id = ?`, userID)
	return err
}

// UserByCalendarToken is the account a feed token belongs to. An unknown or turned-off
// token and a disabled account are all ErrNotFound alike, so the answer never says
// which accounts exist.
func (s *Service) UserByCalendarToken(ctx context.Context, token string) (*User, error) {
	if len(token) < 32 || len(token) > 128 {
		return nil, ErrNotFound
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM calendar_feeds WHERE token_hash = ?`, hashCalendarToken(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u, err := s.UserByID(ctx, id)
	if errors.Is(err, ErrInvalidCredentials) || (err == nil && (u == nil || u.Disabled)) {
		return nil, ErrNotFound
	}
	return u, err
}
