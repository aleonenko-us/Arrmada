-- 0350_file_checks: what the file check (internal/filecheck) found inside library files.
--
-- file_checks: the last check of each movie or episode file — its path, size and
-- modification time as checked — so the nightly pass only looks again at a file that
-- changed. kind is movie | episode; media_id is movies.id or episodes.id; version_id a
-- movie's extra version (0 = the default track, and always 0 for an episode).
--
-- file_check_findings: each problem found in a file (no sound, silence, a language nobody
-- here watches in, a probable cam, an unreadable file), from the first check that saw it.
-- dismissed is the owner's "Ignore". A file that changes is checked afresh: its old
-- findings, dismissed or not, go with the old file.
--
-- file_check_replacements: every time a bad copy was replaced — by hand ("Find a better
-- copy") or automatically — so automatic replacement can stop after one a day and three in
-- all for a title, rather than loop on a film with no good copy yet.
CREATE TABLE file_checks (
    kind       TEXT NOT NULL,
    media_id   INTEGER NOT NULL,
    version_id INTEGER NOT NULL DEFAULT 0,
    path       TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    mtime      INTEGER NOT NULL DEFAULT 0,
    checked_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (kind, media_id, version_id)
);

CREATE TABLE file_check_findings (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    media_id   INTEGER NOT NULL,
    version_id INTEGER NOT NULL DEFAULT 0,
    owner_id   INTEGER NOT NULL DEFAULT 0, -- the movie, or the episode's show
    season     INTEGER NOT NULL DEFAULT 0,
    episode    INTEGER NOT NULL DEFAULT 0,
    code       TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',   -- "The Matrix (1999)", "Severance S01E02"
    detail     TEXT NOT NULL DEFAULT '',   -- "the file has no sound"
    release    TEXT NOT NULL DEFAULT '',   -- the release the file came from, when known
    path       TEXT NOT NULL DEFAULT '',
    first_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dismissed  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (kind, media_id, version_id, code)
);
CREATE INDEX idx_file_check_findings_owner ON file_check_findings(kind, owner_id);

CREATE TABLE file_check_replacements (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    kind     TEXT NOT NULL,
    media_id INTEGER NOT NULL,
    code     TEXT NOT NULL DEFAULT '',
    release  TEXT NOT NULL DEFAULT '',
    auto     INTEGER NOT NULL DEFAULT 0,
    at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_file_check_replacements_media ON file_check_replacements(kind, media_id, at);
