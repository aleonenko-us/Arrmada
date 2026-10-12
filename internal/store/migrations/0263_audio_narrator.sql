-- 0263_audio_narrator: who reads each audiobook. The narrator is read from the audio
-- files' tags (narrator, composer, album_artist when it isn't the author, performer),
-- and can be typed by hand per book (the standard audiobook) or per audiobook version;
-- what's typed wins.
--
-- meta_version says which probe wrote a row: rows from before narrators were read are
-- version 1, and the audiobook server's warm-up reads each of those files once more.
ALTER TABLE audio_file_meta ADD COLUMN narrator TEXT NOT NULL DEFAULT '';
ALTER TABLE audio_file_meta ADD COLUMN meta_version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE books ADD COLUMN audiobook_narrator TEXT NOT NULL DEFAULT '';
ALTER TABLE book_audio_versions ADD COLUMN narrator TEXT NOT NULL DEFAULT '';

-- A narrator typed in Arrmada is a detail apps show, so it moves meta_updated_at like
-- the others (0262_books_meta_updated, recreated here with the narrator columns).
DROP TRIGGER IF EXISTS books_meta_updated;
CREATE TRIGGER books_meta_updated
AFTER UPDATE OF title, author, year, cover_url, description, subjects_json, series_name, series_position,
                audiobook_path, ebook_path, audiobook_narrator ON books
WHEN NEW.title IS NOT OLD.title OR NEW.author IS NOT OLD.author OR NEW.year IS NOT OLD.year
  OR NEW.cover_url IS NOT OLD.cover_url OR NEW.description IS NOT OLD.description
  OR NEW.subjects_json IS NOT OLD.subjects_json OR NEW.series_name IS NOT OLD.series_name
  OR NEW.series_position IS NOT OLD.series_position OR NEW.audiobook_path IS NOT OLD.audiobook_path
  OR NEW.ebook_path IS NOT OLD.ebook_path OR NEW.audiobook_narrator IS NOT OLD.audiobook_narrator
BEGIN
    UPDATE books SET meta_updated_at = MAX(CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), meta_updated_at + 1)
     WHERE id = NEW.id;
END;

DROP TRIGGER IF EXISTS book_audio_versions_meta_updated;
CREATE TRIGGER book_audio_versions_meta_updated
AFTER UPDATE OF label, path, narrator ON book_audio_versions
WHEN NEW.label IS NOT OLD.label OR NEW.path IS NOT OLD.path OR NEW.narrator IS NOT OLD.narrator
BEGIN
    UPDATE books SET meta_updated_at = MAX(CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), meta_updated_at + 1)
     WHERE id = NEW.book_id;
END;
