-- 0250_listen_item_redirects: where an audiobook item went when its book was merged into
-- another (BOOK-10/11).
--
-- Listening places, bookmarks, sessions and timelines are keyed by item ("b12",
-- "b12v3"). A merge moves them to the kept book's keys, but listening apps cache item
-- ids, and an offline upload or a progress update for the old id can arrive days later.
-- The audiobook server follows this table once for an id it no longer knows, so that
-- report lands on the kept book instead of failing.
--
-- There is deliberately no user column: a redirect says where an item went, never who
-- listened to it. Chains are collapsed when written (old_key always points at a key that
-- exists now), so one lookup is enough. Book and version ids are never reused
-- (AUTOINCREMENT), so a redirected key can't come back as a different item.
CREATE TABLE IF NOT EXISTS listen_item_redirects (
    old_key    TEXT    PRIMARY KEY,
    new_key    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_listen_item_redirects_new ON listen_item_redirects(new_key);
