-- 0254_book_release_date: the day a book comes out, from the catalogue.
--
-- Books carried only a year, so a book requested before publication was searched at
-- add time and then on the ladder, burning misses on a book that didn't exist yet, and
-- its requester read 'Searching'. With the day known, the sweep leaves an upcoming book
-- alone until it is out, searches daily for two weeks from release, and the request says
-- 'Out <date>'. YYYY-MM-DD; '' when the catalogue doesn't say (Open Library never does).
ALTER TABLE books ADD COLUMN release_date TEXT NOT NULL DEFAULT '';
