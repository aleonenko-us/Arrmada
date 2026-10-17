-- 0333_display_titles: the title the app SHOWS for a film or show can differ from its
-- canonical (TMDB English) title, which matching, naming and every script keep using.
--   title_override  the per-title "Display title…" pick on the movie or series page;
--                   '' follows Settings → Library → "Show titles as". A column of its
--                   own, not extra metadata, so no metadata refresh can ever drop it.
--   display_title   the title shown now, worked out from the setting, the override and
--                   the names stored with the title; '' means the canonical title. Kept
--                   up to date whenever any of those change, so lists, the calendar and
--                   alerts read it straight from the row without asking TMDB.
ALTER TABLE movies ADD COLUMN title_override TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN display_title TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN title_override TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN display_title TEXT NOT NULL DEFAULT '';
