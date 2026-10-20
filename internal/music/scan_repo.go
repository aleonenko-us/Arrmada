package music

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

// The library scan's reads and writes (MUS-37). Everything it needs about what is already
// recorded is read in a handful of statements up front, and everything it learns is
// written an album (or a batch of folders) per transaction: a 100,000-track library is
// never a query per file.

// KeyScanWorkers is how many album folders the library scan reads at once.
const KeyScanWorkers = "music_scan_workers"

// Scan worker bounds: the default suits a NAS (reads are latency-bound, so a few at once
// help) without crowding a small box; more than 16 only queues on the disk.
const (
	DefaultScanWorkers = 4
	MaxScanWorkers     = 16
)

// ScanWorkers is the saved worker count, within 1..MaxScanWorkers (DefaultScanWorkers when
// unset or unreadable).
func (s *Service) ScanWorkers() int {
	v, ok := s.setting(KeyScanWorkers)
	if !ok {
		return DefaultScanWorkers
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return DefaultScanWorkers
	}
	return min(n, MaxScanWorkers)
}

// ScanKnownFile is one recorded file as the scan sees it.
type ScanKnownFile struct {
	ID, AlbumID, TrackID int64
	Path                 string
	SizeBytes, MtimeUnix int64
	FileID               string
	Missing              bool
	// MissingSince is when the verify pass marked the file missing ("" when it isn't).
	MissingSince string
}

// ScanKnownFiles lists every recorded music file, in one statement.
func (s *Service) ScanKnownFiles(ctx context.Context) ([]ScanKnownFile, error) {
	rows, err := s.repo.db.QueryContext(ctx, `
		SELECT id, album_id, COALESCE(release_track_id, 0), path, size_bytes, mtime_unix, file_id,
		       COALESCE(missing_since, '')
		  FROM music_track_files WHERE path != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanKnownFile
	for rows.Next() {
		var f ScanKnownFile
		if err := rows.Scan(&f.ID, &f.AlbumID, &f.TrackID, &f.Path, &f.SizeBytes, &f.MtimeUnix, &f.FileID, &f.MissingSince); err != nil {
			return nil, err
		}
		f.Missing = f.MissingSince != ""
		out = append(out, f)
	}
	return out, rows.Err()
}

// ScanAlbum is one library album as the scan matches folders against: its artist, title
// and release-group MBID.
type ScanAlbum struct {
	ID, ArtistID int64
	Title, MBID  string
}

// ScanAlbums lists every album, in one statement.
func (s *Service) ScanAlbums(ctx context.Context) ([]ScanAlbum, error) {
	rows, err := s.repo.db.QueryContext(ctx, `SELECT id, artist_id, title, mbid FROM albums`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanAlbum
	for rows.Next() {
		var a ScanAlbum
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.MBID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArtistByMBID is the artist with this MBID (ErrNotFound when there is none).
func (s *Service) ArtistByMBID(ctx context.Context, mbid string) (Artist, error) {
	a, err := scanArtist(s.repo.db.QueryRowContext(ctx, `SELECT `+artistCols+` FROM artists WHERE mbid = ? COLLATE NOCASE`, strings.TrimSpace(mbid)))
	if errors.Is(err, sql.ErrNoRows) {
		return Artist{}, ErrNotFound
	}
	return a, err
}

// LocalAlbumOfRelease is the library album an edition MBID is stored under (0: none),
// without asking MusicBrainz: the scan stays local.
func (s *Service) LocalAlbumOfRelease(ctx context.Context, releaseMBID string) (int64, error) {
	return s.repo.albumOfRelease(ctx, releaseMBID)
}

// ScanDir is the scan's memory of one album folder (music_scan_dirs).
type ScanDir struct {
	Path, Sig, FilesSig string
	Files               int
	State               string
}

// Scan folder states.
const (
	ScanDirDone      = "done"
	ScanDirUnmatched = "unmatched"
	// ScanDirWaiting: its album has no track listing yet; looked at again, after the
	// folders that changed.
	ScanDirWaiting = "waiting"
)

// ScanDirs reads the scan's memory of every album folder.
func (s *Service) ScanDirs(ctx context.Context) (map[string]ScanDir, error) {
	rows, err := s.repo.db.QueryContext(ctx, `SELECT path, sig, files_sig, files, state FROM music_scan_dirs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ScanDir{}
	for rows.Next() {
		var d ScanDir
		if err := rows.Scan(&d.Path, &d.Sig, &d.FilesSig, &d.Files, &d.State); err != nil {
			return nil, err
		}
		out[d.Path] = d
	}
	return out, rows.Err()
}

// SaveScanDirs remembers folders the scan judged, all in one transaction.
func (s *Service) SaveScanDirs(ctx context.Context, dirs []ScanDir) error {
	if len(dirs) == 0 {
		return nil
	}
	return store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO music_scan_dirs (path, sig, files_sig, files, state, scanned_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(path) DO UPDATE SET sig = excluded.sig, files_sig = excluded.files_sig,
			  files = excluded.files, state = excluded.state, scanned_at = excluded.scanned_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, d := range dirs {
			if _, err := stmt.ExecContext(ctx, d.Path, d.Sig, d.FilesSig, d.Files, d.State); err != nil {
				return err
			}
		}
		return nil
	})
}

// ForgetScanDir drops the scan's memory of a folder, so the next scan looks at it afresh.
func (s *Service) ForgetScanDir(ctx context.Context, path string) error {
	_, err := s.repo.db.ExecContext(ctx, `DELETE FROM music_scan_dirs WHERE path = ?`, path)
	return err
}

// ScannedTrack is one file the scan found for one release track.
type ScannedTrack struct {
	TrackID int64
	Rec     FileRecord
}

// RecordScanned records the files the scan matched to an album's tracks, in one
// transaction. A track that already has a file on disk keeps it (the scan never re-points a
// recorded track), and a path another track holds is left alone; both are skipped, not
// errors. It returns how many were recorded.
func (s *Service) RecordScanned(ctx context.Context, files []ScannedTrack) (int, error) {
	n := 0
	err := store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		n = 0
		for _, f := range files {
			var held int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM music_track_files
				 WHERE release_track_id = ? AND missing_since IS NULL AND path != ?`, f.TrackID, f.Rec.Path).Scan(&held); err != nil {
				return err
			}
			if held > 0 {
				continue
			}
			err := recordTrackFileTx(ctx, tx, f.TrackID, f.Rec)
			if errors.Is(err, ErrPathTaken) || errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// FollowMovedFile records that a recorded file is now at another path (the scan found it
// there by its inode, with the size and modification time recorded for it, and the old
// path is gone): the row keeps everything else, and a missing mark ends. False when the
// row changed meanwhile, its recorded size or time isn't the file's, or another row holds
// the path.
func (s *Service) FollowMovedFile(ctx context.Context, id int64, from, to string, size, mtime int64, fileID string) (bool, error) {
	res, err := s.repo.db.ExecContext(ctx, `
		UPDATE music_track_files SET path = ?, file_id = ?, missing_since = NULL, missing_seen_at = NULL
		 WHERE id = ? AND path = ? AND size_bytes = ? AND mtime_unix = ? AND size_bytes > 0 AND mtime_unix > 0
		   AND NOT EXISTS (SELECT 1 FROM music_track_files o WHERE o.path = ?)`,
		to, fileID, id, from, size, mtime, to)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// scanRunsKept is how many scan summaries are kept for the results drawer.
const scanRunsKept = 20

// SaveScanRun keeps one scan's summary (JSON) and prunes the oldest beyond scanRunsKept.
func (s *Service) SaveScanRun(ctx context.Context, started, finished time.Time, summary string) error {
	return store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO music_scan_runs (started_at, finished_at, summary_json) VALUES (?, ?, ?)`,
			started.UTC().Format(time.RFC3339), finished.UTC().Format(time.RFC3339), summary); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM music_scan_runs WHERE id NOT IN (SELECT id FROM music_scan_runs ORDER BY id DESC LIMIT ?)`, scanRunsKept)
		return err
	})
}

// LastScanRun is the most recent scan's summary JSON and when it ran (ok false: none yet).
func (s *Service) LastScanRun(ctx context.Context) (summary, started, finished string, ok bool, err error) {
	err = s.repo.db.QueryRowContext(ctx, `SELECT summary_json, started_at, finished_at FROM music_scan_runs ORDER BY id DESC LIMIT 1`).
		Scan(&summary, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	return summary, started, finished, err == nil, err
}
