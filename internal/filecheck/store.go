package filecheck

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

// sqlTime is how this package writes a time: UTC, the same text SQLite's CURRENT_TIMESTAMP
// writes, so datetime() comparisons and the defaults agree.
const sqlTime = "2006-01-02 15:04:05"

func toSQL(t time.Time) string { return t.UTC().Format(sqlTime) }

// fromSQL reads a stored time whichever way the driver hands it back (the driver turns a
// TIMESTAMP column into a time.Time; an aggregate comes back as text). Zero when unset.
func fromSQL(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x.UTC()
	case string:
		return parseSQLTime(x)
	case []byte:
		return parseSQLTime(string(x))
	}
	return time.Time{}
}

func parseSQLTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{sqlTime, time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05.999999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// fileKey names one checked file.
type fileKey struct {
	Kind      string
	MediaID   int64
	VersionID int64
}

// identity is what a check recorded about the file it read.
type identity struct {
	Path  string
	Size  int64
	Mtime int64
}

type repo struct{ db *sql.DB }

func (r repo) identity(ctx context.Context, k fileKey) (identity, bool, error) {
	var id identity
	err := r.db.QueryRowContext(ctx, `SELECT path, size_bytes, mtime FROM file_checks WHERE kind = ? AND media_id = ? AND version_id = ?`,
		k.Kind, k.MediaID, k.VersionID).Scan(&id.Path, &id.Size, &id.Mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return identity{}, false, nil
	}
	return id, err == nil, err
}

// record stores one check: the file as read and its findings. A file that changed since
// the last check starts afresh (its old findings, dismissed or not, were about the old
// file); otherwise a finding seen again keeps its first-seen time and its dismissal, and
// one no longer found is dropped.
func (r repo) record(ctx context.Context, f File, id identity, found []Finding, now time.Time) error {
	return store.WithTx(ctx, r.db, func(tx *sql.Tx) error { return recordTx(ctx, tx, f, id, found, now) })
}

func recordTx(ctx context.Context, tx *sql.Tx, f File, id identity, found []Finding, now time.Time) error {
	k := f.key()
	var prev identity
	err := tx.QueryRowContext(ctx, `SELECT path, size_bytes, mtime FROM file_checks WHERE kind = ? AND media_id = ? AND version_id = ?`,
		k.Kind, k.MediaID, k.VersionID).Scan(&prev.Path, &prev.Size, &prev.Mtime)
	changed := errors.Is(err, sql.ErrNoRows) || prev != id
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO file_checks (kind, media_id, version_id, path, size_bytes, mtime, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, media_id, version_id) DO UPDATE SET path = excluded.path, size_bytes = excluded.size_bytes,
			mtime = excluded.mtime, checked_at = excluded.checked_at`,
		k.Kind, k.MediaID, k.VersionID, id.Path, id.Size, id.Mtime, toSQL(now)); err != nil {
		return err
	}
	if changed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM file_check_findings WHERE kind = ? AND media_id = ? AND version_id = ?`,
			k.Kind, k.MediaID, k.VersionID); err != nil {
			return err
		}
	}
	codes := make([]any, 0, len(found)+3)
	codes = append(codes, k.Kind, k.MediaID, k.VersionID)
	for _, fd := range found {
		codes = append(codes, fd.Code)
		if _, err := tx.ExecContext(ctx, `INSERT INTO file_check_findings
			(kind, media_id, version_id, owner_id, season, episode, code, title, detail, release, path, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(kind, media_id, version_id, code) DO UPDATE SET owner_id = excluded.owner_id,
				season = excluded.season, episode = excluded.episode, title = excluded.title,
				detail = excluded.detail, release = excluded.release, path = excluded.path`,
			k.Kind, k.MediaID, k.VersionID, f.OwnerID, f.Season, f.Episode, fd.Code, f.Title, fd.Detail, f.Release, f.Path, toSQL(now)); err != nil {
			return err
		}
	}
	q := `DELETE FROM file_check_findings WHERE kind = ? AND media_id = ? AND version_id = ?`
	if len(found) > 0 {
		q += ` AND code NOT IN (?` + strings.Repeat(", ?", len(found)-1) + `)`
	}
	_, err = tx.ExecContext(ctx, q, codes...)
	return err
}

// forget drops every check of files of the given kind that aren't in keep (gone from the
// library), or — with owner set — of that one movie's or show's files not in keep.
func (r repo) forget(ctx context.Context, kind string, owner int64, keep map[fileKey]bool) error {
	q := `SELECT f.kind, f.media_id, f.version_id FROM file_checks f WHERE f.kind = ?`
	args := []any{kind}
	if owner > 0 {
		if kind == KindMovie {
			q += ` AND f.media_id = ?`
		} else {
			q += ` AND f.media_id IN (SELECT id FROM episodes WHERE series_id = ?)`
		}
		args = append(args, owner)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	var gone []fileKey
	for rows.Next() {
		var k fileKey
		if err := rows.Scan(&k.Kind, &k.MediaID, &k.VersionID); err != nil {
			rows.Close()
			return err
		}
		if !keep[k] {
			gone = append(gone, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range gone {
		for _, t := range []string{"file_checks", "file_check_findings"} {
			if _, err := r.db.ExecContext(ctx, `DELETE FROM `+t+` WHERE kind = ? AND media_id = ? AND version_id = ?`,
				k.Kind, k.MediaID, k.VersionID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Stored is one finding as kept.
type Stored struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	MediaID   int64     `json:"media_id"`
	VersionID int64     `json:"version_id,omitempty"`
	OwnerID   int64     `json:"owner_id"`
	Season    int       `json:"season,omitempty"`
	Episode   int       `json:"episode,omitempty"`
	Code      string    `json:"code"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Release   string    `json:"release,omitempty"`
	Path      string    `json:"path,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	Dismissed bool      `json:"dismissed,omitempty"`
}

const storedCols = `id, kind, media_id, version_id, owner_id, season, episode, code, title, detail, release, path, first_seen, dismissed`

func scanStored(sc interface{ Scan(...any) error }) (Stored, error) {
	var s Stored
	var first any
	var dismissed int
	err := sc.Scan(&s.ID, &s.Kind, &s.MediaID, &s.VersionID, &s.OwnerID, &s.Season, &s.Episode, &s.Code, &s.Title,
		&s.Detail, &s.Release, &s.Path, &first, &dismissed)
	s.FirstSeen, s.Dismissed = fromSQL(first), dismissed != 0
	return s, err
}

// findings lists findings: all of them, or only those not dismissed; optionally only one
// movie's or show's (kind and owner set).
func (r repo) findings(ctx context.Context, activeOnly bool, kind string, owner int64) ([]Stored, error) {
	q := `SELECT ` + storedCols + ` FROM file_check_findings WHERE 1 = 1`
	var args []any
	if activeOnly {
		q += ` AND dismissed = 0`
	}
	if kind != "" {
		q += ` AND kind = ? AND owner_id = ?`
		args = append(args, kind, owner)
	}
	rows, err := r.db.QueryContext(ctx, q+` ORDER BY first_seen, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		s, err := scanStored(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r repo) finding(ctx context.Context, id int64) (Stored, error) {
	s, err := scanStored(r.db.QueryRowContext(ctx, `SELECT `+storedCols+` FROM file_check_findings WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	return s, err
}

func (r repo) dismiss(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `UPDATE file_check_findings SET dismissed = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// replacement is one recorded replace.
type replacement struct {
	At   time.Time
	Auto bool
}

func (r repo) replacements(ctx context.Context, kind string, mediaID int64) ([]replacement, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT at, auto FROM file_check_replacements WHERE kind = ? AND media_id = ? ORDER BY at, id`, kind, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []replacement
	for rows.Next() {
		var at any
		var auto int
		if err := rows.Scan(&at, &auto); err != nil {
			return nil, err
		}
		out = append(out, replacement{At: fromSQL(at), Auto: auto != 0})
	}
	return out, rows.Err()
}

func (r repo) addReplacement(ctx context.Context, kind string, mediaID int64, code, release string, auto bool, now time.Time) error {
	a := 0
	if auto {
		a = 1
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO file_check_replacements (kind, media_id, code, release, auto, at) VALUES (?, ?, ?, ?, ?, ?)`,
		kind, mediaID, code, release, a, toSQL(now))
	return err
}

// grabbedAt is when a movie's release was grabbed (the earliest grab of that name for the
// track), zero when Arrmada has no grab of it.
func (r repo) grabbedAt(ctx context.Context, movieID, versionID int64, release string) time.Time {
	if release == "" {
		return time.Time{}
	}
	var v any
	if err := r.db.QueryRowContext(ctx, `SELECT MIN(grabbed_at) FROM grabs WHERE movie_id = ? AND version_id = ? AND title = ? COLLATE NOCASE`,
		movieID, versionID, release).Scan(&v); err != nil {
		return time.Time{}
	}
	return fromSQL(v)
}
