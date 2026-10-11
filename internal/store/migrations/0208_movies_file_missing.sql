-- 0208_movies_file_missing: when the daily missing-file check (or the detail page) found a
-- movie's recorded default file gone from disk. NULL means present as far as Arrmada knows.
--
-- It's only a flag: nothing is cleared, deleted or searched because of it — the grid shows
-- "File missing" and the owner decides on the detail page. A new file recorded for the
-- movie, or the record cleared, resets it (movies.Repo SetFile / ClearFile). The check
-- refuses to set it while the movies folder looks unmounted.
ALTER TABLE movies ADD COLUMN file_missing_since TEXT;
