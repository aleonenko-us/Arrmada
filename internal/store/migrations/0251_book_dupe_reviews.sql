-- 0251_book_dupe_reviews: pairs of library books that may be one book (BOOK-12).
--
-- Most possible duplicates are worked out when the review opens (same title and
-- author). This table keeps what can't be: pairs the Hardcover re-match flagged
-- (status open, with the reason), and pairs a manager said are different books
-- (status ignored), which never come back — across reloads and restarts.
--
-- A pair is stored once, smaller id first. Deleting either book drops its pairs.
CREATE TABLE IF NOT EXISTS book_dupe_reviews (
    a_id       INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    b_id       INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    reason     TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'ignored')),
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (a_id, b_id),
    CHECK (a_id < b_id)
);
CREATE INDEX IF NOT EXISTS idx_book_dupe_reviews_b ON book_dupe_reviews(b_id);

-- Pairs the re-match flagged before this table existed are on both books' timelines
-- ("… the same book as “X” (book 12). Not merged; …"): bring them into the review.
INSERT OR IGNORE INTO book_dupe_reviews (a_id, b_id, reason, status)
SELECT MIN(e.book_id, o.id), MAX(e.book_id, o.id), 'Hardcover lists both as the same book', 'open'
  FROM book_events e
  JOIN books o ON o.id = CAST(substr(e.detail, instr(e.detail, '” (book ') + length('” (book ')) AS INTEGER)
 WHERE e.event = 'possible_duplicate'
   AND instr(e.detail, '” (book ') > 0
   AND o.id != e.book_id;
