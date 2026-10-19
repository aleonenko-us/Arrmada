package metadata

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"
)

// MusicBrainz answers are kept for good in music_mb_cache. Every answer that is stored is
// served again until it is checked, and checking it never removes it: an answer that can't
// be fetched again (MusicBrainz down, refusing us, or saying 404 about something it used to
// know) leaves the old one in place. Lidarr's library emptied itself when its metadata
// server answered with nothing; this one keeps what it had and says it's stale.
//
// Rows are keyed by what was asked (the kind of entity, its MBID and the include set, or a
// normalised search), never by the host, so switching to or from a self-hosted mirror keeps
// the cache. Bodies are gzipped JSON exactly as MusicBrainz sent them.

// ErrMusicBrainzUnavailable means MusicBrainz couldn't be asked (down, refusing, timing
// out) and nothing was cached for the question. Callers treat it as "unknown", never as
// "empty".
var ErrMusicBrainzUnavailable = errors.New("MusicBrainz is unavailable")

func errMusicBrainzUnavailable(why string) error {
	return fmt.Errorf("%w: %s", ErrMusicBrainzUnavailable, why)
}

// How long a cached answer counts as fresh, per kind. A stale answer is still served at
// once; it is refetched in the background (and by the refresh-music-cache task).
const (
	MBFreshArtist       = 7 * 24 * time.Hour  // an artist's name, type, genres
	MBFreshReleaseGroup = 3 * 24 * time.Hour  // an artist's albums: where new releases show up
	MBFreshRelease      = 30 * 24 * time.Hour // a release's track listing, a release group's releases
	MBFreshSearch       = 24 * time.Hour      // an artist search
)

// mbKind is one kind of cached question, with its freshness and, for answers that are
// mostly a list, the list's field (an empty list never replaces a full one; see keep).
type mbKind struct {
	name  string
	fresh time.Duration
	list  string
}

var (
	mbKindArtist       = mbKind{"artist", MBFreshArtist, ""}
	mbKindReleaseGroup = mbKind{"release-group-browse", MBFreshReleaseGroup, "release-groups"}
	mbKindReleases     = mbKind{"release-browse", MBFreshRelease, "releases"}
	mbKindRelease      = mbKind{"release", MBFreshRelease, "media"}
	mbKindSearch       = mbKind{"artist-search", MBFreshSearch, ""}
)

var mbKinds = map[string]mbKind{}

func init() {
	for _, k := range []mbKind{mbKindArtist, mbKindReleaseGroup, mbKindReleases, mbKindRelease, mbKindSearch} {
		mbKinds[k.name] = k
	}
}

// mbEntry is one cached answer.
type mbEntry struct {
	body      []byte // the JSON, unzipped
	status    int    // 200, or 404 when MusicBrainz said there's no such thing
	fetchedAt time.Time
	checkedAt time.Time // the last time MusicBrainz was asked and answered
}

func (e mbEntry) fresh(k mbKind, now time.Time) bool { return now.Sub(e.checkedAt) < k.fresh }

// mbCache is the store behind music_mb_cache. A nil db caches nothing (tests).
type mbCache struct{ db *sql.DB }

func (c mbCache) get(ctx context.Context, key string) (mbEntry, bool) {
	if c.db == nil {
		return mbEntry{}, false
	}
	var gz []byte
	var e mbEntry
	var fetched, checked int64
	err := c.db.QueryRowContext(ctx,
		`SELECT body, status, fetched_at, checked_at FROM music_mb_cache WHERE key = ?`, key).
		Scan(&gz, &e.status, &fetched, &checked)
	if err != nil {
		return mbEntry{}, false
	}
	body, err := gunzip(gz)
	if err != nil {
		return mbEntry{}, false
	}
	e.body, e.fetchedAt, e.checkedAt = body, time.UnixMilli(fetched), time.UnixMilli(checked)
	return e, true
}

// put stores a fresh answer under key.
func (c mbCache) put(ctx context.Context, key string, status int, body []byte, now time.Time) error {
	if c.db == nil {
		return nil
	}
	gz, err := gzipBytes(body)
	if err != nil {
		return err
	}
	ms := now.UnixMilli()
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO music_mb_cache (key, body, status, fetched_at, checked_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET body = excluded.body, status = excluded.status,
		   fetched_at = excluded.fetched_at, checked_at = excluded.checked_at`,
		key, gz, status, ms, ms)
	return err
}

// touch says MusicBrainz was asked about key and the answer kept is still the one to use
// (it answered the same, or answered with less than we have).
func (c mbCache) touch(ctx context.Context, key string, now time.Time) {
	if c.db == nil {
		return
	}
	_, _ = c.db.ExecContext(ctx, `UPDATE music_mb_cache SET checked_at = ? WHERE key = ?`, now.UnixMilli(), key)
}

// stalest lists up to limit keys whose answers are past their freshness, longest unchecked
// first, for the refresh task.
func (c mbCache) stalest(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if c.db == nil {
		return nil, nil
	}
	// Keys start with their kind; the shortest freshness bounds the read, and each row is
	// checked against its own kind below.
	rows, err := c.db.QueryContext(ctx,
		`SELECT key, checked_at FROM music_mb_cache WHERE checked_at < ? ORDER BY checked_at LIMIT ?`,
		now.Add(-MBFreshSearch).UnixMilli(), limit*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() && len(out) < limit {
		var key string
		var checked int64
		if err := rows.Scan(&key, &checked); err != nil {
			return nil, err
		}
		k, ok := mbKindOfKey(key)
		if ok && now.Sub(time.UnixMilli(checked)) >= k.fresh {
			out = append(out, key)
		}
	}
	return out, rows.Err()
}

// MBCacheStats is what the Settings card shows about the cache.
type MBCacheStats struct {
	Entries int   `json:"entries"`
	Bytes   int64 `json:"bytes"`
	Stale   int   `json:"stale"`
}

func (c mbCache) stats(ctx context.Context, now time.Time) (MBCacheStats, error) {
	var s MBCacheStats
	if c.db == nil {
		return s, nil
	}
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(length(body)), 0) FROM music_mb_cache`).Scan(&s.Entries, &s.Bytes)
	if err != nil {
		return s, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT key, checked_at FROM music_mb_cache`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var checked int64
		if rows.Scan(&key, &checked) != nil {
			continue
		}
		if k, ok := mbKindOfKey(key); ok && now.Sub(time.UnixMilli(checked)) >= k.fresh {
			s.Stale++
		}
	}
	return s, rows.Err()
}

func mbKindOfKey(key string) (mbKind, bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			k, ok := mbKinds[key[:i]]
			return k, ok
		}
	}
	return mbKind{}, false
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
