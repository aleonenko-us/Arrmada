-- 0295_session_grandparent: the show's poster and rating key on every recorded episode.
--
-- An episode's own thumb is its landscape still, so Most watched TV and the History list
-- squeezed stills into portrait slots; the show's poster (grandparentThumb) is what those
-- want. The show's rating key lets the Library tab and the People page group and match
-- shows by Plex identity instead of by name. '' = not recorded: every play before this,
-- movies and music, and Tautulli imports that don't carry them.
ALTER TABLE stream_sessions ADD COLUMN grandparent_thumb TEXT NOT NULL DEFAULT '';
ALTER TABLE stream_sessions ADD COLUMN grandparent_rating_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_stream_sessions_grandparent_rk ON stream_sessions(grandparent_rating_key);
