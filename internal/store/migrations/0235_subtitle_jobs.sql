-- 0235_subtitle_jobs: the Subtitles queue and its recent history, kept across restarts.
--
-- The queue was two in-memory slices, so every Unraid update dropped whatever was queued
-- (and the visible history with it), and the sweep didn't come round again for up to six
-- hours. A job's id is its row id now, so it no longer restarts with the app: a reported
-- problem's subtitles fix can still find its job after a restart.
--
-- lane is 'fast' (extract, download) or 'gpu' (whisper). langs is the CSV of languages a
-- job is limited to (a gpu job carries only what the fast lane couldn't make), empty for
-- every kept language. outcomes_json is what each rung did so far ([]Outcome); audio_sec
-- is the file's running time, for the AI backlog estimate. Stage and progress ticks stay
-- in memory: only state changes are written. Finished rows are pruned to the newest 500.
--
-- AUTOINCREMENT so an id is never handed out twice, even after its row is pruned.
CREATE TABLE IF NOT EXISTS subtitle_jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    media_key     TEXT NOT NULL,
    kind          TEXT NOT NULL,
    movie_id      INTEGER,
    series_id     INTEGER,
    season        INTEGER,
    episode       INTEGER,
    title         TEXT NOT NULL,
    priority      INTEGER NOT NULL,
    lane          TEXT NOT NULL DEFAULT 'fast',
    state         TEXT NOT NULL,
    langs         TEXT NOT NULL DEFAULT '',
    redo          INTEGER NOT NULL DEFAULT 0,
    note          TEXT,
    run_id        TEXT,
    outcomes_json TEXT,
    audio_sec     INTEGER NOT NULL DEFAULT 0,
    queued_at     INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER
);
CREATE INDEX IF NOT EXISTS idx_subtitle_jobs_queue ON subtitle_jobs(state, lane, priority, queued_at);
-- One live job per file and lane, the same rule enqueue's in-memory dedupe keeps.
CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_jobs_live ON subtitle_jobs(media_key, lane) WHERE state IN ('queued', 'running');
