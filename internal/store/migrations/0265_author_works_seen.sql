-- 0265_author_works_seen: the works of a followed author that Arrmada has already seen.
--
-- The check-author-releases job adds new books by followed authors. "New" means not in
-- this list: the first check of an author only fills it (so following someone never
-- re-adds back catalogue the admin skipped), and a book the job added and someone later
-- deleted stays seen, so it doesn't come back. work_key is the catalogue's key for the
-- work, or "title:<normalised title>" so the same book listed under a new key is still
-- known.
--
-- authors.works_seeded says the first look has happened (works_checked_at alone can't:
-- a look that failed is stamped too, so it is retried in a day rather than at once).
-- Following an author again, or pointing them at another catalogue entry, clears it.
-- authors.new_books_found counts the books the job has added, for the author page.
CREATE TABLE IF NOT EXISTS author_works_seen (
    author_id  INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    work_key   TEXT NOT NULL,
    first_seen TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (author_id, work_key)
);

ALTER TABLE authors ADD COLUMN works_seeded INTEGER NOT NULL DEFAULT 0;
ALTER TABLE authors ADD COLUMN new_books_found INTEGER NOT NULL DEFAULT 0;
