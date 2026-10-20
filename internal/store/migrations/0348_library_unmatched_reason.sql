-- 0348_library_unmatched_reason: the series scan's needs-you list is kept here too, with why.
--
-- Movies have kept their unmatched folders in library_unmatched since 0207; the series scan
-- kept its own in memory, so a restart emptied it and a folder it skipped left no trace. It
-- now shares the table. A row says why the folder needs you (reason, a short code the web UI
-- keys its fix action on; detail, the same in plain words) and, for a show that was added
-- but got no episodes from its folder, which show that was (series_id). Rows already here
-- (movies) keep the empty defaults.
ALTER TABLE library_unmatched ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE library_unmatched ADD COLUMN detail TEXT NOT NULL DEFAULT '';
ALTER TABLE library_unmatched ADD COLUMN series_id INTEGER NOT NULL DEFAULT 0;
