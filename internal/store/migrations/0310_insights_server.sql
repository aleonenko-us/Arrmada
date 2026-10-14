-- 0310_insights_server: which media server each recorded play, person, stream in progress
-- and bandwidth sample came from, now that Insights watches Jellyfin as well as Plex.
--
-- Everything already recorded came from Plex (or a Tautulli import of a Plex server), so
-- the default 'plex' is the truth for every existing row, and the live recorder and the
-- importer that don't name a server keep writing Plex rows.
--
-- Plays, people and streams stay in their own tables: a Jellyfin person is another row of
-- plex_users (its id a 32-hex GUID, a Plex id is a short number, so the two never meet),
-- and a Jellyfin play's rating key is the item's GUID. That also keeps 0308's viewing
-- triggers right without a change: they group by user and item, and neither can be shared
-- between the servers. The triggers don't list server, so adding it doesn't touch them.
ALTER TABLE stream_sessions ADD COLUMN server TEXT NOT NULL DEFAULT 'plex';

-- Every Insights read can be narrowed to one server over a window of time.
CREATE INDEX IF NOT EXISTS idx_stream_sessions_server_started ON stream_sessions(server, started_at);

ALTER TABLE plex_users ADD COLUMN server TEXT NOT NULL DEFAULT 'plex';

ALTER TABLE insights_live_sessions ADD COLUMN server TEXT NOT NULL DEFAULT 'plex';

-- One sample per poll per server: each server's loop records its own total.
ALTER TABLE bandwidth_samples ADD COLUMN server TEXT NOT NULL DEFAULT 'plex';
