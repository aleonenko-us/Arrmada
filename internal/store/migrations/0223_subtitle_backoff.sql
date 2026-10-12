-- 0223_subtitle_backoff: when to look again for a subtitle that couldn't be made.
--
-- The six-hourly sweep decided "missing" from the sidecars on disk alone, so a file with
-- no OpenSubtitles match, an impossible AI plan, a rejected login or a missing whisper
-- model was queued again every six hours forever, spending a search call each time. One
-- row per (file, language, variant) still missing after a run says why and when to try
-- again: next_retry_at is the earliest time-based retry (NULL = only an event clears it),
-- clear_on the events that clear it early (creds, model, langs), reasons_json each
-- rung's outcome. A row only holds for the video_path it was written for, so an upgraded
-- file starts fresh. Import and manual runs ignore it; a success deletes it.
CREATE TABLE IF NOT EXISTS subtitle_backoff (
    media_key      TEXT NOT NULL,
    lang           TEXT NOT NULL,
    variant        TEXT NOT NULL DEFAULT 'full',
    video_path     TEXT NOT NULL,
    primary_reason TEXT NOT NULL,
    reasons_json   TEXT NOT NULL,
    detail         TEXT,
    count          INTEGER NOT NULL DEFAULT 1,
    next_retry_at  INTEGER,
    clear_on       TEXT NOT NULL DEFAULT '',
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (media_key, lang, variant)
);
