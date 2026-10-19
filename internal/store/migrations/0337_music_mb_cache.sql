-- 0337_music_mb_cache: every MusicBrainz answer, kept for good. MusicBrainz allows one
-- request a second, so without a cache every page open, listing and refresh paid for its
-- calls again (a first load of a big library is hours), and an outage left nothing to show.
-- metadata_cache (0080) can't be reused: its rows are pruned by age, and these must never
-- be deleted just because they're old or MusicBrainz is unreachable.
--
--   key         what was asked: "<kind>:<mbid or query>?<include set>", never the host, so a
--               switch to or from a self-hosted mirror keeps the cache
--   body        the JSON answer, gzipped
--   status      200, or 404 when MusicBrainz said there's no such thing
--   fetched_at  when this body arrived (unix ms)
--   checked_at  when MusicBrainz last answered for it (unix ms); freshness runs from here
CREATE TABLE IF NOT EXISTS music_mb_cache (
    key        TEXT    PRIMARY KEY,
    body       BLOB    NOT NULL,
    status     INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    checked_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_music_mb_cache_checked ON music_mb_cache(checked_at);
