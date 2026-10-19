package music

import (
	"context"
	"database/sql"
	"time"
)

// Music's numbers for the Dashboard and the homepage widget (MUS-24). Both pages are read
// often (the Dashboard refreshes on every import), so these are grouped statements that
// return only what is counted, never every album with its counts.

// IncompleteWanted lists the albums WantedAlbums would list that aren't Complete: only
// their dates, which is all a count of wanted and released albums needs. One grouped
// statement over the target editions, with WantedAlbums' filters and Album.Complete's rule
// (a track counts when it is wanted; inside a monitored album that is its own flag).
func (s *Service) IncompleteWanted(ctx context.Context) ([]Album, error) {
	return s.repo.IncompleteWanted(ctx)
}

// IncompleteWanted is Service.IncompleteWanted.
func (r *Repo) IncompleteWanted(ctx context.Context) ([]Album, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT al.id, al.release_date, al.year
		  FROM albums al
		  JOIN artists ar ON ar.id = al.artist_id
		  LEFT JOIN music_media m ON m.release_id = al.target_release_id
		  LEFT JOIN music_release_tracks rt ON rt.medium_id = m.id
		  LEFT JOIN music_track_files f ON f.release_track_id = rt.id AND f.missing_since IS NULL
		 WHERE al.monitored = 1 AND ar.monitored = 1 AND al.stale_since IS NULL
		   AND NOT EXISTS (SELECT 1 FROM music_migration_notes n WHERE n.album_id = al.id AND `+openNote+`)
		 GROUP BY al.id
		HAVING NOT (COUNT(rt.id) > 0
		            AND COUNT(CASE WHEN rt.monitored = 1 AND f.id IS NOT NULL THEN 1 END)
		                >= COUNT(CASE WHEN rt.monitored = 1 THEN 1 END))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Album
	for rows.Next() {
		var al Album
		if err := rows.Scan(&al.ID, &al.ReleaseDate, &al.Year); err != nil {
			return nil, err
		}
		out = append(out, al)
	}
	return out, rows.Err()
}

// RecentRelease is one row of the New releases list as the Dashboard shows it.
type RecentRelease struct {
	ID          int64
	Title       string
	ArtistName  string
	AlbumType   string
	ReleaseDate string
	Monitored   bool
	FoundAs     string
}

// RecentReleases is the newest limit albums of the New releases list (found by a refresh
// in the last days) and how many the list holds in all.
func (s *Service) RecentReleases(ctx context.Context, days, limit int) ([]RecentRelease, int, error) {
	if days <= 0 {
		days = 30
	}
	return s.repo.RecentReleases(ctx, s.clock().Add(-time.Duration(days)*24*time.Hour), limit)
}

// RecentReleases is Service.RecentReleases.
func (r *Repo) RecentReleases(ctx context.Context, since time.Time, limit int) ([]RecentRelease, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM albums WHERE found_at IS NOT NULL AND found_at >= ?`,
		dbTime(since)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT al.id, al.title, COALESCE(ar.name, ''), al.album_type, al.release_date, al.monitored, al.found_as
		  FROM albums al LEFT JOIN artists ar ON ar.id = al.artist_id
		 WHERE al.found_at IS NOT NULL AND al.found_at >= ?
		 ORDER BY al.found_at DESC, al.id DESC
		 LIMIT ?`, dbTime(since), limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RecentRelease{}
	for rows.Next() {
		var (
			rr  RecentRelease
			mon int
		)
		if err := rows.Scan(&rr.ID, &rr.Title, &rr.ArtistName, &rr.AlbumType, &rr.ReleaseDate, &mon, &rr.FoundAs); err != nil {
			return nil, 0, err
		}
		rr.Monitored = mon != 0
		out = append(out, rr)
	}
	return out, total, rows.Err()
}

// rowsWith scans a row into an album (scanAlbum's columns) and then extra columns, for a
// query that selects a few more after the album's own.
type rowsWith struct {
	rows  *sql.Rows
	extra []any
}

func (w rowsWith) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.extra...)...) }
