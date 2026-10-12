-- 0278_series_upgrade_cursor: let the upgrade sweep reach every season, a few per sweep.
--
-- The upgrade sweep asked one title query per show, which returns one capped page of
-- mostly recent releases, so a long or finished show's older seasons (and their BluRay
-- season packs) were never searched for upgrades at all. It now also asks for a few
-- seasons by number each sweep, resuming where the last sweep stopped — the season number
-- the next sweep starts at, like search_season_cursor does for the missing sweep.
ALTER TABLE series ADD COLUMN search_upgrade_cursor INTEGER NOT NULL DEFAULT 0;
