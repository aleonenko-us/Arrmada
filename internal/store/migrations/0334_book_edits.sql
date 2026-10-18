-- 0334_book_edits: a book's details can be edited by hand on its page, and the details the
-- catalogue (Open Library / Hardcover) matched stay underneath, per field, so any edit
-- can be reset and a refresh or re-match never undoes one.
--
-- The book's own columns hold what is SHOWN and used everywhere (pages, search, the
-- audiobook server, OPDS, file naming, series grouping): the edit when there is one,
-- else the catalogue's value. That way every reader of a book, Go or SQL, sees the edit
-- without knowing about it.
--
--   edits_json  the fields edited by hand, each with the catalogue's value underneath:
--               {"title": "Dune", "series_position": 1}. A field not in it is not edited.
--   edited_at   when the last hand edit was saved (unix ms, only ever moves forward).
--               An edit writes it; the triggers below tell an edit from any other write
--               by it.
--   subtitle, language (a canonical code from internal/langs), isbn (digits, checksum
--   checked) and publisher are new details; nothing fills them but an edit for now.
ALTER TABLE books ADD COLUMN subtitle TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN language TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN isbn TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN publisher TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN edits_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE books ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0;

-- A narrator already typed by hand was always an edit: what's underneath is the audio
-- files' own tags, which the audiobook server reads, so the value kept for it is ''.
UPDATE books SET edits_json = json_object('narrator', '') WHERE audiobook_narrator != '';

-- The guarantee: a write to an edited field that isn't an edit (a refresh, a re-match, a
-- series learned from a release, a merge, or any code path added later) lands
-- underneath instead. The new value becomes the catalogue's value kept in edits_json and
-- the column goes back to the edit. The Go code already writes catalogue values to
-- edits_json itself (books.Repo.setMatched); this catches anything that doesn't.
-- Triggers don't fire themselves (recursive_triggers is off), so the put-back is final.
CREATE TRIGGER books_edit_keeps_title
AFTER UPDATE OF title ON books
WHEN NEW.title IS NOT OLD.title AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.title') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.title', NEW.title), title = OLD.title WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_subtitle
AFTER UPDATE OF subtitle ON books
WHEN NEW.subtitle IS NOT OLD.subtitle AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.subtitle') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.subtitle', NEW.subtitle), subtitle = OLD.subtitle WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_author
AFTER UPDATE OF author ON books
WHEN NEW.author IS NOT OLD.author AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.author') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.author', NEW.author), author = OLD.author WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_series_name
AFTER UPDATE OF series_name ON books
WHEN NEW.series_name IS NOT OLD.series_name AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.series_name') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.series_name', NEW.series_name), series_name = OLD.series_name WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_series_position
AFTER UPDATE OF series_position ON books
WHEN NEW.series_position IS NOT OLD.series_position AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.series_position') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.series_position', NEW.series_position), series_position = OLD.series_position WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_description
AFTER UPDATE OF description ON books
WHEN NEW.description IS NOT OLD.description AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.description') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.description', NEW.description), description = OLD.description WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_year
AFTER UPDATE OF year ON books
WHEN NEW.year IS NOT OLD.year AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.year') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.year', NEW.year), year = OLD.year WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_language
AFTER UPDATE OF language ON books
WHEN NEW.language IS NOT OLD.language AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.language') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.language', NEW.language), language = OLD.language WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_isbn
AFTER UPDATE OF isbn ON books
WHEN NEW.isbn IS NOT OLD.isbn AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.isbn') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.isbn', NEW.isbn), isbn = OLD.isbn WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_narrator
AFTER UPDATE OF audiobook_narrator ON books
WHEN NEW.audiobook_narrator IS NOT OLD.audiobook_narrator AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.narrator') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.narrator', NEW.audiobook_narrator), audiobook_narrator = OLD.audiobook_narrator WHERE id = NEW.id;
END;

CREATE TRIGGER books_edit_keeps_publisher
AFTER UPDATE OF publisher ON books
WHEN NEW.publisher IS NOT OLD.publisher AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.publisher') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.publisher', NEW.publisher), publisher = OLD.publisher WHERE id = NEW.id;
END;

-- Subjects are a JSON list in their column, so they go underneath as a list.
CREATE TRIGGER books_edit_keeps_subjects
AFTER UPDATE OF subjects_json ON books
WHEN NEW.subjects_json IS NOT OLD.subjects_json AND NEW.edited_at IS OLD.edited_at
 AND json_type(OLD.edits_json, '$.subjects') IS NOT NULL
BEGIN
    UPDATE books SET edits_json = json_set(edits_json, '$.subjects',
                         CASE WHEN NEW.subjects_json = '' THEN json('[]') ELSE json(NEW.subjects_json) END),
                     subjects_json = OLD.subjects_json
     WHERE id = NEW.id;
END;

-- Listening apps show the subtitle, language, ISBN and publisher too, so a change to them
-- moves meta_updated_at like the others (0263_audio_narrator, recreated with them).
-- edits_json isn't listed: a catalogue value changing underneath an edit changes nothing
-- an app shows.
DROP TRIGGER IF EXISTS books_meta_updated;
CREATE TRIGGER books_meta_updated
AFTER UPDATE OF title, author, year, cover_url, description, subjects_json, series_name, series_position,
                audiobook_path, ebook_path, audiobook_narrator, subtitle, language, isbn, publisher ON books
WHEN NEW.title IS NOT OLD.title OR NEW.author IS NOT OLD.author OR NEW.year IS NOT OLD.year
  OR NEW.cover_url IS NOT OLD.cover_url OR NEW.description IS NOT OLD.description
  OR NEW.subjects_json IS NOT OLD.subjects_json OR NEW.series_name IS NOT OLD.series_name
  OR NEW.series_position IS NOT OLD.series_position OR NEW.audiobook_path IS NOT OLD.audiobook_path
  OR NEW.ebook_path IS NOT OLD.ebook_path OR NEW.audiobook_narrator IS NOT OLD.audiobook_narrator
  OR NEW.subtitle IS NOT OLD.subtitle OR NEW.language IS NOT OLD.language
  OR NEW.isbn IS NOT OLD.isbn OR NEW.publisher IS NOT OLD.publisher
BEGIN
    UPDATE books SET meta_updated_at = MAX(CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER), meta_updated_at + 1)
     WHERE id = NEW.id;
END;
