package books

import (
	"context"
	"database/sql"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

// PathMove is one file a rename moved on disk.
type PathMove struct {
	From, To string
}

// RepointEdition records that a book edition's files were moved by a rename: the edition
// (versionID 0) or audiobook version now lives at path — its file, or its folder when it
// has several — and every moved file's probed audio details (audio_file_meta, keyed by
// path) follow it, so the audiobook server knows its length and chapters without probing
// it again, and keeps the file id listening apps already hold (the row's ino). One
// transaction with the timeline entry and the end of the rename's journal entry, so the
// record never describes half a rename.
//
// Listening places, bookmarks and history are keyed by the book's (and version's) id, not
// by a path, so a rename leaves them exactly as they were. meta_updated_at moves, which
// tells listening apps to re-read the item — by hand as well as by the path trigger, since
// a rename that only renames a multi-file audiobook's tracks leaves the folder path as it
// was.
func (s *Service) RepointEdition(ctx context.Context, bookID, versionID int64, kind, path string, moves []PathMove, detail string) error {
	return store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		var (
			res sql.Result
			err error
		)
		if versionID > 0 {
			res, err = tx.ExecContext(ctx, `UPDATE book_audio_versions SET path = ? WHERE id = ? AND book_id = ?`, path, versionID, bookID)
		} else {
			col := "ebook_path"
			if kind == KindAudiobook {
				col = "audiobook_path"
			}
			res, err = tx.ExecContext(ctx, `UPDATE books SET `+col+` = ? WHERE id = ?`, path, bookID)
		}
		if err := affected(res, err); err != nil {
			return err
		}
		for _, m := range moves {
			// OR REPLACE: a stale row already at the new path (a file that was there once)
			// gives way to the details of the file that's there now.
			if _, err := tx.ExecContext(ctx, `UPDATE OR REPLACE audio_file_meta SET path = ? WHERE path = ?`, m.To, m.From); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE books SET meta_updated_at = MAX(?, meta_updated_at + 1) WHERE id = ?`, time.Now().UnixMilli(), bookID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_rename_journal WHERE book_id = ? AND version_id = ? AND kind = ?`, bookID, versionID, kind); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO book_events (book_id, event, detail) VALUES (?, ?, ?)`, bookID, "renamed", detail)
		return err
	})
}

// JournalRename writes down the moves a rename is about to make, before the first one.
// RepointEdition ends the entry with the record; ClearRenameJournal ends it when the
// rename was undone. An entry still here at start-up is a rename a crash cut short.
func (s *Service) JournalRename(ctx context.Context, bookID, versionID int64, kind string, moves []PathMove) error {
	now := time.Now().UnixMilli()
	return store.WithTx(ctx, s.repo.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM book_rename_journal WHERE book_id = ? AND version_id = ? AND kind = ?`, bookID, versionID, kind); err != nil {
			return err
		}
		for i, m := range moves {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO book_rename_journal (book_id, version_id, kind, seq, from_path, to_path, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				bookID, versionID, kind, i, m.From, m.To, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearRenameJournal ends an edition's journal entry (the rename was undone).
func (s *Service) ClearRenameJournal(ctx context.Context, bookID, versionID int64, kind string) error {
	_, err := s.repo.db.ExecContext(ctx, `DELETE FROM book_rename_journal WHERE book_id = ? AND version_id = ? AND kind = ?`, bookID, versionID, kind)
	return err
}

// UnfinishedRename is one edition's rename a crash cut short: the moves it planned, in
// order. Its record still names the old place (the record and the end of the entry are
// one transaction), so the way back is to undo the moves that happened.
type UnfinishedRename struct {
	BookID, VersionID int64
	Kind              string
	Moves             []PathMove
}

// UnfinishedRenames lists the journal's entries.
func (s *Service) UnfinishedRenames(ctx context.Context) ([]UnfinishedRename, error) {
	rows, err := s.repo.db.QueryContext(ctx,
		`SELECT book_id, version_id, kind, from_path, to_path FROM book_rename_journal ORDER BY book_id, version_id, kind, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnfinishedRename
	for rows.Next() {
		var (
			bookID, versionID int64
			kind              string
			m                 PathMove
		)
		if err := rows.Scan(&bookID, &versionID, &kind, &m.From, &m.To); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].BookID != bookID || out[n-1].VersionID != versionID || out[n-1].Kind != kind {
			out = append(out, UnfinishedRename{BookID: bookID, VersionID: versionID, Kind: kind})
		}
		out[len(out)-1].Moves = append(out[len(out)-1].Moves, m)
	}
	return out, rows.Err()
}
