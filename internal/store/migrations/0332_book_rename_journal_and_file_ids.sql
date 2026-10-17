-- 0332_book_rename_journal_and_file_ids: two things the book rename (issue #25) needs.
--
-- audio_file_meta.ino is the id listening apps know an audiobook file by. It used to be
-- worked out from the file's name on every listing, so renaming a file changed it under a
-- phone's downloads and an open play session. Now it's stored the first time the file is
-- listed — the same value it had always been given — and follows the file through a
-- rename with the rest of its row. '' means not stored yet.
ALTER TABLE audio_file_meta ADD COLUMN ino TEXT NOT NULL DEFAULT '';

-- A rename's planned moves, written before the first file moves and removed in the same
-- transaction that records the new place (or once the moves are undone). An entry still
-- here at start-up is a rename a crash cut short; it is rolled back then.
CREATE TABLE IF NOT EXISTS book_rename_journal (
    book_id    INTEGER NOT NULL,
    version_id INTEGER NOT NULL DEFAULT 0,
    kind       TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    from_path  TEXT    NOT NULL,
    to_path    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (book_id, version_id, kind, seq)
);
