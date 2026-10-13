-- 0299_calendar_feeds: each person's private calendar feed link (APP-18).
--
-- The link carries a random token that Google or Apple Calendar fetches with. Only its
-- SHA-256 is kept, so the database (or a backup of it) can't be used to read anyone's
-- feed. One per account: making a new link replaces the row, and the old link stops
-- working at once. It goes with the account.
CREATE TABLE calendar_feeds (
    user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);
