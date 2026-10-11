-- 0193_event_feed_indexes: History reads the four per-title event tables newest first
-- across every title (GET /api/v1/history, the Dashboard's Recent activity). Each table
-- was only indexed per title, so that read sorted the whole table; with these each branch
-- of the feed walks its own index from the newest row and stops at the page size.

CREATE INDEX IF NOT EXISTS idx_movie_events_created  ON movie_events(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_series_events_created ON series_events(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_book_events_created   ON book_events(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_artist_events_created ON artist_events(created_at DESC, id DESC);
