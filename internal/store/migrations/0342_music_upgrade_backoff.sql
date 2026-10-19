-- 0342_music_upgrade_backoff: the music upgrade sweep's own backoff (MUS-16).
--
-- albums.last_upgrade_search_at: when the upgrade sweep last looked for a better copy of a
-- complete album. NULL = never; those go first, a capped few a run, so the first sweep
-- after an update doesn't search a whole library at once.
--
-- albums.upgrade_misses: upgrade searches in a row that found nothing better, which
-- lengthens the wait before the next (30 min up to 12 h, then weekly, as for missing
-- albums). Kept apart from search_misses: a complete album's upgrade backoff must never
-- slow down the search for one that's missing tracks, or the other way round.
ALTER TABLE albums ADD COLUMN last_upgrade_search_at TIMESTAMP;
ALTER TABLE albums ADD COLUMN upgrade_misses INTEGER NOT NULL DEFAULT 0;
