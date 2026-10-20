// Package notinterested keeps the titles each person has hidden from their own Discover
// ("Not interested"). A hidden title never shows again in that person's rows, browse
// grids or search results until they restore it; nobody else's Discover changes.
package notinterested

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Title is one hidden title, with enough to show it in the hidden list.
type Title struct {
	MediaType string `json:"media_type"`
	TMDBID    int    `json:"tmdb_id"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	At        string `json:"at,omitempty"`
}

// Key is how a title is looked up: "movie:603", "series:1396".
func Key(media string, tmdbID int) string { return media + ":" + strconv.Itoa(tmdbID) }

// ErrBadTitle is a title that isn't a movie or series with a TMDB id.
var ErrBadTitle = errors.New("not a movie or series")

// maxPerUser bounds one person's list (a script can't grow it without end).
const maxPerUser = 5000

// ErrFull is a person's list at maxPerUser.
var ErrFull = errors.New("too many hidden titles")

// Store reads and writes the hidden titles.
type Store struct{ db *sql.DB }

// New is a store over the app database.
func New(db *sql.DB) *Store { return &Store{db: db} }

func valid(media string, tmdbID int) bool {
	return (media == "movie" || media == "series") && tmdbID > 0
}

// Hide hides a title for a person. Hiding it again just refreshes what's kept about it.
func (s *Store) Hide(ctx context.Context, userID int64, t Title) error {
	if !valid(t.MediaType, t.TMDBID) || userID <= 0 {
		return ErrBadTitle
	}
	t.Title = clip(strings.TrimSpace(t.Title), 300)
	if !strings.HasPrefix(t.PosterURL, "https://image.tmdb.org/") {
		t.PosterURL = "" // only TMDB's own images are shown back
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM discover_not_interested WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return err
	}
	if n >= maxPerUser {
		return ErrFull
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO discover_not_interested (user_id, media_type, tmdb_id, title, year, poster_url)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, media_type, tmdb_id) DO UPDATE SET title = excluded.title, year = excluded.year, poster_url = excluded.poster_url`,
		userID, t.MediaType, t.TMDBID, t.Title, max(t.Year, 0), t.PosterURL)
	return err
}

// Restore shows a hidden title again. Restoring one that isn't hidden is fine.
func (s *Store) Restore(ctx context.Context, userID int64, media string, tmdbID int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM discover_not_interested WHERE user_id = ? AND media_type = ? AND tmdb_id = ?`, userID, media, tmdbID)
	return err
}

// List is a person's hidden titles, the most recently hidden first.
func (s *Store) List(ctx context.Context, userID int64) ([]Title, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_type, tmdb_id, title, year, poster_url, created_at
		FROM discover_not_interested WHERE user_id = ? ORDER BY created_at DESC, rowid DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Title{}
	for rows.Next() {
		var t Title
		if err := rows.Scan(&t.MediaType, &t.TMDBID, &t.Title, &t.Year, &t.PosterURL, &t.At); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Keys is the set of a person's hidden titles (Key), for filtering cards.
func (s *Store) Keys(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_type, tmdb_id FROM discover_not_interested WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var media string
		var id int
		if err := rows.Scan(&media, &id); err != nil {
			return nil, err
		}
		out[Key(media, id)] = true
	}
	return out, rows.Err()
}

// clip cuts s to at most n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
