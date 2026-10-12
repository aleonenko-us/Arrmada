-- 0230_issues: problems requesters report with a title in the library ("episode 4 has no
-- English audio"), for staff to fix from Requests → Issues.
--
-- An issue is about one library item: a movie (library_id = movies.id) or one episode of a
-- show (library_id = series.id plus season and episode). title, year and poster_url are a
-- copy taken when it was reported, so the list reads without a join and an issue on a
-- title deleted since still says what it was about.
--
-- status: open (reported), fixing (staff started a fix — action says which), resolved,
-- dismissed. While fixing, action_file and action_size are the file that was there when
-- the fix started, so the backstop can tell a new one has landed even if the import's own
-- signal was missed; action_job is the subtitle job a subtitles fix started (0 for none).
-- told_at is when every reporter was told it was closed (0 = not yet: the notice is retried).
--
-- issue_reporters is everyone who reported it: a second person reporting the same problem
-- joins the first issue instead of opening another, and each is told when it's fixed. Each
-- keeps their own note, which only staff and that person see. Visibility for requesters is
-- this table: they see the issues they are a reporter of, never anyone else's.
CREATE TABLE IF NOT EXISTS issues (
    id               INTEGER PRIMARY KEY,
    media_type       TEXT    NOT NULL CHECK (media_type IN ('movie', 'series')),
    library_id       INTEGER NOT NULL,
    tmdb_id          INTEGER NOT NULL DEFAULT 0,
    title            TEXT    NOT NULL,
    year             INTEGER NOT NULL DEFAULT 0,
    poster_url       TEXT    NOT NULL DEFAULT '',
    season           INTEGER NOT NULL DEFAULT 0,
    episode          INTEGER NOT NULL DEFAULT 0,
    category         TEXT    NOT NULL CHECK (category IN ('video', 'audio', 'subtitles', 'wrong_file', 'other')),
    note             TEXT    NOT NULL DEFAULT '',
    reported_by      INTEGER NOT NULL,
    reported_by_name TEXT    NOT NULL DEFAULT '',
    status           TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'fixing', 'resolved', 'dismissed')),
    resolution       TEXT    NOT NULL DEFAULT '',
    action           TEXT    NOT NULL DEFAULT '',
    action_note      TEXT    NOT NULL DEFAULT '',
    action_file      TEXT    NOT NULL DEFAULT '',
    action_size      INTEGER NOT NULL DEFAULT 0,
    action_job       INTEGER NOT NULL DEFAULT 0,
    action_at        INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    resolved_at      INTEGER NOT NULL DEFAULT 0,
    resolved_by      INTEGER NOT NULL DEFAULT 0,
    resolved_by_name TEXT    NOT NULL DEFAULT '',
    told_at          INTEGER NOT NULL DEFAULT 0
);

-- One live issue per problem: a second report of the same item and category while the
-- first is open or being fixed joins it (the insert hits this index and the report is
-- attached instead), even when two people report at the same moment.
CREATE UNIQUE INDEX IF NOT EXISTS idx_issues_live
    ON issues (media_type, library_id, season, episode, category)
    WHERE status IN ('open', 'fixing');
CREATE INDEX IF NOT EXISTS idx_issues_status ON issues (status, created_at);
CREATE INDEX IF NOT EXISTS idx_issues_item ON issues (media_type, library_id, season, episode);

CREATE TABLE IF NOT EXISTS issue_reporters (
    issue_id   INTEGER NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_name  TEXT    NOT NULL DEFAULT '',
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_issue_reporters_user ON issue_reporters (user_id);
