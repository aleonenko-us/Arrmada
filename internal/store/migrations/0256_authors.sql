-- 0256_authors: an author is a row of its own, not a string on each book.
--
-- Books used to carry only an author name, so 'J.K. Rowling' and 'J. K. Rowling' were two
-- people on the Books page, an author page had to guess who a name meant, and nobody
-- could be followed. books.author stays as the display string (and what indexer searches
-- carry); books.author_id says which person it is.
--
-- catalogue_key is the catalogue's id for the person (Open Library "OL23919A", Hardcover
-- "hc:a:123"), unique when set. monitored / monitor_new / quality_profile /
-- works_checked_at are for following an author (new books added on a schedule); checked_at
-- is when the photo and biography were last looked up.
--
-- Nothing is copied from book_authors here: two names there can share one key, which the
-- unique index refuses. The Go backfill (books.Service.BackfillAuthors) groups the names,
-- never joins two that carry different keys, and leaves an ambiguous name unlinked.
CREATE TABLE IF NOT EXISTS authors (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,
    catalogue_key    TEXT NOT NULL DEFAULT '',
    image_url        TEXT NOT NULL DEFAULT '',
    bio              TEXT NOT NULL DEFAULT '',
    monitored        INTEGER NOT NULL DEFAULT 0,
    monitor_new      INTEGER NOT NULL DEFAULT 0,
    quality_profile  TEXT NOT NULL DEFAULT '',
    added_at         TEXT NOT NULL DEFAULT (datetime('now')),
    checked_at       TEXT NOT NULL DEFAULT '',
    works_checked_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_authors_key ON authors(catalogue_key) WHERE catalogue_key != '';

ALTER TABLE books ADD COLUMN author_id INTEGER REFERENCES authors(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_books_author_id ON books(author_id);
