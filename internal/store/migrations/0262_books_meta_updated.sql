-- 0262_books_meta_updated: when a book's details last changed (unix ms), so listening
-- apps see a newer updatedAt and fetch the new cover and title. Apps key their image
-- caches on updatedAt, and it used to be the day the book was added, forever.
--
-- Triggers keep it current, so every path that edits a book (a manual correction, a
-- cover pick, a refresh, a re-match, a series learned from a release, an import or a
-- removed edition) bumps it without each call site having to remember. The WHEN clause
-- skips writes that change nothing (a refresh rewriting the same description), and the
-- new value is always at least one more than the old, so two edits in the same
-- millisecond still read as two. meta_updated_at isn't in the column lists, so the
-- triggers never fire themselves.
ALTER TABLE books ADD COLUMN meta_updated_at INTEGER NOT NULL DEFAULT 0;

CREATE TRIGGER books_meta_updated
AFTER UPDATE OF title, author, year, cover_url, description, subjects_json, series_name, series_position,
                audiobook_path, ebook_path ON books
WHEN NEW.title IS NOT OLD.title OR NEW.author IS NOT OLD.author OR NEW.year IS NOT OLD.year
  OR NEW.cover_url IS NOT OLD.cover_url OR NEW.description IS NOT OLD.description
  OR NEW.subjects_json IS NOT OLD.subjects_json OR NEW.series_name IS NOT OLD.series_name
  OR NEW.series_position IS NOT OLD.series_position OR NEW.audiobook_path IS NOT OLD.audiobook_path
  OR NEW.ebook_path IS NOT OLD.ebook_path
BEGIN
    UPDATE books SET meta_updated_at = MAX(CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), meta_updated_at + 1)
     WHERE id = NEW.id;
END;

-- An audiobook version's own name or file changes what apps show for that version.
CREATE TRIGGER book_audio_versions_meta_updated
AFTER UPDATE OF label, path ON book_audio_versions
WHEN NEW.label IS NOT OLD.label OR NEW.path IS NOT OLD.path
BEGIN
    UPDATE books SET meta_updated_at = MAX(CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), meta_updated_at + 1)
     WHERE id = NEW.book_id;
END;
