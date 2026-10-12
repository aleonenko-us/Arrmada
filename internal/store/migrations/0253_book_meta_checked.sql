-- 0253_book_meta_checked: when each book's catalogue metadata was last refreshed.
--
-- Books added from an author's catalogue or 'Add missing in series' were created from
-- the list entry alone, with no description, genres or series, and nothing ever went
-- back for them: the only refresh was the manual button. The refresh-books job now
-- works through the library, never-checked books first, then any not checked in 30
-- days. '' means never.
ALTER TABLE books ADD COLUMN meta_checked_at TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_books_meta_checked ON books(meta_checked_at);
