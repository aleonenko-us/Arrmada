-- 0308_insights_viewing_start: mark the plays that start a viewing, so History's "Group
-- resumed plays" can list and count viewings without a pass over every play.
--
-- A viewing is a person's sittings of one item with no gap of more than three hours
-- between one sitting's stop and the next one's start (insights.resumeGap). Working that
-- out with a window function over 100,000 plays took about half a second per page; with
-- the starting plays marked, a page reads its 50 starts and their sittings from indexes.
--
-- The item is the play's rating key, or for a play without one its type, title, show,
-- season and episode (insights.itemKey — the expression below must stay identical to it,
-- or SQLite won't use the index for it; TestViewingStartIndexMatchesItemKey checks).
-- viewing_start = 1 when the play has no earlier sitting of the item by that person, or
-- the one just before it (by start, then id) stopped more than 10800 seconds before it
-- began. Only grouping over every play, or one person's, reads it: other filters pick
-- sittings that change where viewings break, so they still group with the window.
--
-- Triggers keep it right on every write — the live recorder, Tautulli imports and their
-- key repair, undoing an import, removing double counts, folding one user into another —
-- without each call site having to remember. A write only changes the mark of the play
-- itself and of the one after it (its old and new place, for an update), so that is all
-- the triggers touch. viewing_start isn't in the UPDATE trigger's column list, so the
-- triggers never fire themselves. A future migration that rebuilds stream_sessions must
-- create these triggers again.
ALTER TABLE stream_sessions ADD COLUMN viewing_start INTEGER NOT NULL DEFAULT 1;

-- A person's sittings of one item, in order: the triggers' "the play before / after"
-- lookups and the page's sum of each viewing's sittings.
CREATE INDEX IF NOT EXISTS idx_stream_sessions_item ON stream_sessions(user_id,
    (CASE WHEN rating_key <> '' THEN rating_key
     ELSE media_type || '|' || title || '|' || grandparent_title || '|' || parent_index || '|' || media_index END),
    started_at);

UPDATE stream_sessions SET viewing_start = 0 WHERE id IN (
    SELECT id FROM (
        SELECT id, started_at, LAG(stopped_at) OVER (PARTITION BY user_id,
            CASE WHEN rating_key <> '' THEN rating_key
            ELSE media_type || '|' || title || '|' || grandparent_title || '|' || parent_index || '|' || media_index END
            ORDER BY started_at, id) AS prev_stop
        FROM stream_sessions)
    WHERE started_at - prev_stop <= 10800);

-- The viewings, newest first, for paging and counting them.
CREATE INDEX IF NOT EXISTS idx_stream_sessions_viewings ON stream_sessions(started_at) WHERE viewing_start = 1;
CREATE INDEX IF NOT EXISTS idx_stream_sessions_user_viewings ON stream_sessions(user_id, started_at) WHERE viewing_start = 1;

-- Each trigger re-marks a list of plays: viewing_start from the play just before each one.
CREATE TRIGGER stream_sessions_viewing_insert AFTER INSERT ON stream_sessions
BEGIN
    UPDATE stream_sessions SET viewing_start = COALESCE((
        SELECT stream_sessions.started_at - p.stopped_at > 10800 FROM stream_sessions p
         WHERE p.user_id = stream_sessions.user_id
           AND (CASE WHEN p.rating_key <> '' THEN p.rating_key
                ELSE p.media_type || '|' || p.title || '|' || p.grandparent_title || '|' || p.parent_index || '|' || p.media_index END)
             = (CASE WHEN stream_sessions.rating_key <> '' THEN stream_sessions.rating_key
                ELSE stream_sessions.media_type || '|' || stream_sessions.title || '|' || stream_sessions.grandparent_title || '|' || stream_sessions.parent_index || '|' || stream_sessions.media_index END)
           AND (p.started_at, p.id) < (stream_sessions.started_at, stream_sessions.id)
         ORDER BY p.started_at DESC, p.id DESC LIMIT 1), 1)
     WHERE id = NEW.id OR id = (
        SELECT n.id FROM stream_sessions n
         WHERE n.user_id = NEW.user_id
           AND (CASE WHEN n.rating_key <> '' THEN n.rating_key
                ELSE n.media_type || '|' || n.title || '|' || n.grandparent_title || '|' || n.parent_index || '|' || n.media_index END)
             = (CASE WHEN NEW.rating_key <> '' THEN NEW.rating_key
                ELSE NEW.media_type || '|' || NEW.title || '|' || NEW.grandparent_title || '|' || NEW.parent_index || '|' || NEW.media_index END)
           AND (n.started_at, n.id) > (NEW.started_at, NEW.id)
         ORDER BY n.started_at, n.id LIMIT 1);
END;

CREATE TRIGGER stream_sessions_viewing_delete AFTER DELETE ON stream_sessions
BEGIN
    UPDATE stream_sessions SET viewing_start = COALESCE((
        SELECT stream_sessions.started_at - p.stopped_at > 10800 FROM stream_sessions p
         WHERE p.user_id = stream_sessions.user_id
           AND (CASE WHEN p.rating_key <> '' THEN p.rating_key
                ELSE p.media_type || '|' || p.title || '|' || p.grandparent_title || '|' || p.parent_index || '|' || p.media_index END)
             = (CASE WHEN stream_sessions.rating_key <> '' THEN stream_sessions.rating_key
                ELSE stream_sessions.media_type || '|' || stream_sessions.title || '|' || stream_sessions.grandparent_title || '|' || stream_sessions.parent_index || '|' || stream_sessions.media_index END)
           AND (p.started_at, p.id) < (stream_sessions.started_at, stream_sessions.id)
         ORDER BY p.started_at DESC, p.id DESC LIMIT 1), 1)
     WHERE id = (
        SELECT n.id FROM stream_sessions n
         WHERE n.user_id = OLD.user_id
           AND (CASE WHEN n.rating_key <> '' THEN n.rating_key
                ELSE n.media_type || '|' || n.title || '|' || n.grandparent_title || '|' || n.parent_index || '|' || n.media_index END)
             = (CASE WHEN OLD.rating_key <> '' THEN OLD.rating_key
                ELSE OLD.media_type || '|' || OLD.title || '|' || OLD.grandparent_title || '|' || OLD.parent_index || '|' || OLD.media_index END)
           AND (n.started_at, n.id) > (OLD.started_at, OLD.id)
         ORDER BY n.started_at, n.id LIMIT 1);
END;

CREATE TRIGGER stream_sessions_viewing_update
AFTER UPDATE OF user_id, rating_key, media_type, title, grandparent_title, parent_index, media_index, started_at, stopped_at
ON stream_sessions
WHEN NEW.user_id IS NOT OLD.user_id OR NEW.rating_key IS NOT OLD.rating_key OR NEW.media_type IS NOT OLD.media_type
  OR NEW.title IS NOT OLD.title OR NEW.grandparent_title IS NOT OLD.grandparent_title
  OR NEW.parent_index IS NOT OLD.parent_index OR NEW.media_index IS NOT OLD.media_index
  OR NEW.started_at IS NOT OLD.started_at OR NEW.stopped_at IS NOT OLD.stopped_at
BEGIN
    UPDATE stream_sessions SET viewing_start = COALESCE((
        SELECT stream_sessions.started_at - p.stopped_at > 10800 FROM stream_sessions p
         WHERE p.user_id = stream_sessions.user_id
           AND (CASE WHEN p.rating_key <> '' THEN p.rating_key
                ELSE p.media_type || '|' || p.title || '|' || p.grandparent_title || '|' || p.parent_index || '|' || p.media_index END)
             = (CASE WHEN stream_sessions.rating_key <> '' THEN stream_sessions.rating_key
                ELSE stream_sessions.media_type || '|' || stream_sessions.title || '|' || stream_sessions.grandparent_title || '|' || stream_sessions.parent_index || '|' || stream_sessions.media_index END)
           AND (p.started_at, p.id) < (stream_sessions.started_at, stream_sessions.id)
         ORDER BY p.started_at DESC, p.id DESC LIMIT 1), 1)
     WHERE id = NEW.id OR id = (
        SELECT n.id FROM stream_sessions n
         WHERE n.user_id = NEW.user_id
           AND (CASE WHEN n.rating_key <> '' THEN n.rating_key
                ELSE n.media_type || '|' || n.title || '|' || n.grandparent_title || '|' || n.parent_index || '|' || n.media_index END)
             = (CASE WHEN NEW.rating_key <> '' THEN NEW.rating_key
                ELSE NEW.media_type || '|' || NEW.title || '|' || NEW.grandparent_title || '|' || NEW.parent_index || '|' || NEW.media_index END)
           AND (n.started_at, n.id) > (NEW.started_at, NEW.id)
         ORDER BY n.started_at, n.id LIMIT 1)
     OR id = (
        SELECT n.id FROM stream_sessions n
         WHERE n.user_id = OLD.user_id
           AND (CASE WHEN n.rating_key <> '' THEN n.rating_key
                ELSE n.media_type || '|' || n.title || '|' || n.grandparent_title || '|' || n.parent_index || '|' || n.media_index END)
             = (CASE WHEN OLD.rating_key <> '' THEN OLD.rating_key
                ELSE OLD.media_type || '|' || OLD.title || '|' || OLD.grandparent_title || '|' || OLD.parent_index || '|' || OLD.media_index END)
           AND (n.started_at, n.id) > (OLD.started_at, OLD.id)
         ORDER BY n.started_at, n.id LIMIT 1);
END;
