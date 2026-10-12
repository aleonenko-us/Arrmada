-- 0222_subtitle_ledger: where every subtitle came from, and what every attempt did.
--
-- Nothing recorded a sidecar's source (embedded track, which OpenSubtitles file, which
-- whisper model), and a failed download, a rejected login or a whisper crash was only a
-- console line in a 500-line in-memory buffer, gone on the next restart. Redo, the
-- blocklist, Health, backoff and the needs-attention board all need that history.
--
-- subtitle_files: one live row per sidecar Arrmada wrote, adopted or was told to use.
-- A file replaced at the same path supersedes the old row (superseded_at set) instead of
-- deleting it. Sidecars with no row are unowned: automatic upgrades never replace them.
-- variant is 'full', 'sdh' or 'forced'.
CREATE TABLE IF NOT EXISTS subtitle_files (
    id               INTEGER PRIMARY KEY,
    media_key        TEXT NOT NULL,
    kind             TEXT NOT NULL,
    movie_id         INTEGER,
    series_id        INTEGER,
    season           INTEGER,
    episode          INTEGER,
    video_path       TEXT NOT NULL,
    path             TEXT NOT NULL,
    lang             TEXT NOT NULL,
    variant          TEXT NOT NULL DEFAULT 'full',
    source           TEXT NOT NULL,
    embedded_index   INTEGER,
    provider         TEXT,
    provider_file_id TEXT,
    release_name     TEXT,
    hash_match       INTEGER NOT NULL DEFAULT 0,
    score            INTEGER,
    model            TEXT,
    sync_offset_ms   INTEGER,
    sync_confidence  INTEGER,
    locked           INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    superseded_at    INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_files_live_path ON subtitle_files(path) WHERE superseded_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_subtitle_files_media ON subtitle_files(media_key);

-- subtitle_attempts: one row per (run, language, rung) with a typed outcome. Pruned at
-- 180 days by the auto-grab task.
CREATE TABLE IF NOT EXISTS subtitle_attempts (
    id         INTEGER PRIMARY KEY,
    run_id     TEXT NOT NULL,
    media_key  TEXT NOT NULL,
    video_path TEXT NOT NULL,
    lang       TEXT NOT NULL,
    variant    TEXT NOT NULL DEFAULT 'full',
    rung       TEXT NOT NULL,
    outcome    TEXT NOT NULL,
    detail     TEXT,
    at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_subtitle_attempts_media ON subtitle_attempts(media_key, lang, variant, at DESC);
CREATE INDEX IF NOT EXISTS idx_subtitle_attempts_at ON subtitle_attempts(at);

-- subtitle_blocklist: provider files never to download again for one video (Redo, a
-- failed sync check, or the owner's say-so).
CREATE TABLE IF NOT EXISTS subtitle_blocklist (
    provider  TEXT NOT NULL,
    file_id   TEXT NOT NULL,
    media_key TEXT NOT NULL,
    lang      TEXT NOT NULL,
    reason    TEXT NOT NULL,
    at        INTEGER NOT NULL,
    PRIMARY KEY (provider, file_id, media_key)
);
