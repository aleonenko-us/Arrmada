-- 0328_request_release_previews: the release Arrmada would grab for a request still
-- waiting for approval, looked up in advance so whoever approves it knows what they'll
-- get (or that nothing is out yet).
--
-- One row per request. A preview is a dry run: it never grabs, never counts a search miss
-- and never writes search_attempts, so the title's own search state is untouched.
--   state       'queued' (waiting its turn in the paced queue), 'done', or 'busy' (the
--               title was being searched for real; tried again later)
--   queued_at   unix seconds the row wants checking from; 0 when nothing is queued
--   checked_at  unix seconds of the last finished check; 0 = never
--   profile     the quality profile the result was ranked under
--   seasons     series: the seasons the check covered, as a JSON array ('' = whole show)
--   result      the display facts (automation.Preview as JSON): what would be grabbed and
--               why, or why nothing would. Never a download link.
--   tokens      the server-side release tokens behind the result (JSON array), spent only
--               by "Approve & grab this". In-memory handles: useless after a restart, and
--               never sent to any browser.
--
-- No foreign key, as with request_subscribers: requests.Repo.Delete removes the row.
CREATE TABLE IF NOT EXISTS request_previews (
    request_id INTEGER PRIMARY KEY,
    state      TEXT    NOT NULL DEFAULT 'queued',
    queued_at  INTEGER NOT NULL DEFAULT 0,
    checked_at INTEGER NOT NULL DEFAULT 0,
    profile    TEXT    NOT NULL DEFAULT '',
    seasons    TEXT    NOT NULL DEFAULT '',
    result     TEXT    NOT NULL DEFAULT '',
    tokens     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_previews_queue ON request_previews(state, queued_at);
