package books

import (
	"context"
	"database/sql"

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
// it again. One transaction with the timeline entry, so the record never describes half a
// rename.
//
// Listening places, bookmarks and history are keyed by the book's (and version's) id, not
// by a path, so a rename leaves them exactly as they were; the path change moves the
// book's meta_updated_at (trigger), which tells listening apps to re-read the item.
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
		_, err = tx.ExecContext(ctx, `INSERT INTO book_events (book_id, event, detail) VALUES (?, ?, ?)`, bookID, "renamed", detail)
		return err
	})
}
