-- 0339_music_model (MUS-28): music's own shape, with Arrmada's ids. 0070 had artists ->
-- albums -> tracks with the file stored on the track row, which can't hold editions, more
-- than one artist on a record, a file's real properties or any id beyond one MBID a row.
--
-- artists and albums are KEPT, same ids, and widened: every grab, blocklist row, search
-- attempt, review and artist event that stores one of their ids still resolves. albums
-- now means a release group. Below them:
--
--   albums -< music_releases (editions) -< music_media (discs) -< music_release_tracks
--   music_release_tracks >- music_recordings;  music_track_files: 0..1 per release track
--   artists -< music_artist_credit_names >- music_artist_credits (albums, releases, ...)
--   music_external_ids: every other identifier;  music_migration_notes: see below
--
-- Then the data is copied, set-based (one statement per table, no row-by-row work):
--   - each artist gets a one-name credit (credit_key = its MBID), each album that credit;
--   - each album's tracks rows become ONE legacy edition (legacy = 1, no MBID), its discs
--     from the distinct disc numbers, one release track per tracks row and one recording
--     per tracks row. Release tracks and recordings keep the old tracks.id as their own id
--     (and in legacy_track_id), so any id something held still names the same track;
--   - every row with a file (has_file != 0) becomes a music_track_files row with its path, size, stored
--     tier, bitrate and source_release. A file row the migration can't place one-to-one
--     (two tracks claiming one path, a file flag with no path, a track of an album that's
--     gone) is written to music_migration_notes, never dropped.
--
-- tracks.mbid was never a recording MBID: the MusicBrainz client has always stored the
-- release track's id (media[].tracks[].id) there. So it goes to music_release_tracks.mbid,
-- where it lets MUS-30 find the very release a legacy edition was read from, and each
-- recording starts without an MBID rather than with a wrong one.
--
-- The migration checks itself before it commits (the block at the end): any count that
-- disagrees fails it, nothing is applied, and the automatic pre-migration snapshot stays
-- the way back. The old tracks table is frozen, not dropped: the copy's source and its
-- audit trail. Nothing outside migrations reads or writes it any more (archtest).

-- ---------------------------------------------------------------------------------------
-- Artists: same rows, widened.
ALTER TABLE artists ADD COLUMN in_library     INTEGER NOT NULL DEFAULT 1; -- 0 = only credited (featured, Various Artists): hidden from the library list
ALTER TABLE artists ADD COLUMN disambiguation TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN artist_type    TEXT NOT NULL DEFAULT '';   -- Person, Group, Orchestra...
ALTER TABLE artists ADD COLUMN country        TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN stale_since    TIMESTAMP;                  -- MusicBrainz stopped answering for it; data kept

-- Artist credits: many-to-many with join phrases ("Daft Punk feat. Pharrell Williams").
-- An identical credit is stored once, keyed by credit_key ("mbid", or
-- "mbid|join|mbid|join..." for several names; music.CreditKey builds it).
CREATE TABLE music_artist_credits (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    display    TEXT NOT NULL,
    credit_key TEXT NOT NULL UNIQUE
);
CREATE TABLE music_artist_credit_names (
    credit_id     INTEGER NOT NULL REFERENCES music_artist_credits(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,                            -- from 0, in credit order
    artist_id     INTEGER NOT NULL REFERENCES artists(id),     -- no cascade: an artist leaving the library keeps its credits (in_library = 0)
    credited_name TEXT NOT NULL,
    join_phrase   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (credit_id, position)
);
CREATE INDEX idx_music_credit_names_artist ON music_artist_credit_names(artist_id);

-- Editions. mbid is '' for the migrated legacy edition (and later for gap-fill releases);
-- MUS-30 re-keys a legacy edition to a real release by moving file ROWS, never files.
CREATE TABLE music_releases (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id         INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    mbid             TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL,
    artist_credit_id INTEGER REFERENCES music_artist_credits(id),
    status           TEXT NOT NULL DEFAULT '',   -- Official, Promotion, Bootleg, Pseudo-Release
    release_date     TEXT NOT NULL DEFAULT '',   -- YYYY, YYYY-MM or YYYY-MM-DD
    country          TEXT NOT NULL DEFAULT '',
    label            TEXT NOT NULL DEFAULT '',
    catalog_number   TEXT NOT NULL DEFAULT '',
    barcode          TEXT NOT NULL DEFAULT '',
    packaging        TEXT NOT NULL DEFAULT '',
    disambiguation   TEXT NOT NULL DEFAULT '',
    media_summary    TEXT NOT NULL DEFAULT '',
    track_count      INTEGER NOT NULL DEFAULT 0,
    disc_count       INTEGER NOT NULL DEFAULT 0,
    source           TEXT NOT NULL DEFAULT 'musicbrainz', -- musicbrainz | deezer | discogs
    legacy           INTEGER NOT NULL DEFAULT 0, -- 1 = built by this migration from the old tracks table
    tracks_fetched   INTEGER NOT NULL DEFAULT 0, -- 1 = the full track list is stored
    fetched_at       TIMESTAMP,
    stale_since      TIMESTAMP
);
CREATE UNIQUE INDEX ux_music_releases_mbid ON music_releases(mbid) WHERE mbid != '';
CREATE INDEX idx_music_releases_album ON music_releases(album_id);

CREATE TABLE music_media (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id  INTEGER NOT NULL REFERENCES music_releases(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,                -- disc number, from 1
    format      TEXT NOT NULL DEFAULT '',        -- CD, Digital Media, Vinyl...
    title       TEXT NOT NULL DEFAULT '',        -- disc subtitle
    track_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(release_id, position)
);

-- Recordings: the distinct audio, one recording on many releases.
CREATE TABLE music_recordings (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    mbid             TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL,
    artist_credit_id INTEGER REFERENCES music_artist_credits(id), -- NULL: the release's credit
    length_ms        INTEGER NOT NULL DEFAULT 0,
    disambiguation   TEXT NOT NULL DEFAULT '',
    legacy_track_id  INTEGER                      -- migration only: the tracks.id it was made from
);
CREATE UNIQUE INDEX ux_music_recordings_mbid ON music_recordings(mbid) WHERE mbid != '';

-- Tracks: a recording at a position on one disc of one edition.
CREATE TABLE music_release_tracks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    medium_id        INTEGER NOT NULL REFERENCES music_media(id) ON DELETE CASCADE,
    recording_id     INTEGER NOT NULL REFERENCES music_recordings(id),
    mbid             TEXT NOT NULL DEFAULT '',    -- release-track MBID
    position         INTEGER NOT NULL,            -- on its disc
    number           TEXT NOT NULL DEFAULT '',    -- as printed: "3", "A1"
    title            TEXT NOT NULL,
    artist_credit_id INTEGER REFERENCES music_artist_credits(id), -- NULL: the release's credit
    length_ms        INTEGER NOT NULL DEFAULT 0,
    monitored        INTEGER NOT NULL DEFAULT 1,
    legacy_track_id  INTEGER UNIQUE,              -- migration only: the tracks.id it was copied from
    UNIQUE(medium_id, position)
);
CREATE INDEX idx_music_release_tracks_recording ON music_release_tracks(recording_id);

-- Files: what is on disk, measured. One file per release track at most; a file of a track
-- not on the target edition stays as an album extra (release_track_id NULL), never deleted.
-- Deleting a row never deletes a file.
CREATE TABLE music_track_files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id         INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    release_track_id INTEGER REFERENCES music_release_tracks(id) ON DELETE SET NULL,
    path             TEXT NOT NULL UNIQUE,
    size_bytes       INTEGER NOT NULL DEFAULT 0,
    mtime_unix       INTEGER NOT NULL DEFAULT 0,
    file_id          TEXT NOT NULL DEFAULT '',    -- device+inode, so a scan sees a rename, not a delete
    container        TEXT NOT NULL DEFAULT '',    -- flac, mp3, m4a...
    codec            TEXT NOT NULL DEFAULT '',    -- from ffprobe
    lossless         INTEGER NOT NULL DEFAULT 0,
    bitrate_kbps     INTEGER NOT NULL DEFAULT 0,
    bitrate_mode     TEXT NOT NULL DEFAULT '',    -- cbr | vbr | ''
    sample_rate_hz   INTEGER NOT NULL DEFAULT 0,
    bit_depth        INTEGER NOT NULL DEFAULT 0,
    channels         INTEGER NOT NULL DEFAULT 0,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    quality          TEXT NOT NULL DEFAULT '',    -- measured tier, or an old scan's extension tag (music.HeldTier reads both)
    probed_at        TIMESTAMP,                   -- NULL: not measured with every fact yet (the scan's measuring pass)
    tags_json        TEXT NOT NULL DEFAULT '',
    tag_state        TEXT NOT NULL DEFAULT 'as_found', -- as_found | house | failed
    tagged_at        TIMESTAMP,
    acoustid         TEXT NOT NULL DEFAULT '',
    source_release   TEXT NOT NULL DEFAULT '',
    download_hash    TEXT NOT NULL DEFAULT '',
    missing_since    TIMESTAMP,
    added_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_music_track_files_track ON music_track_files(release_track_id) WHERE release_track_id IS NOT NULL;
CREATE INDEX idx_music_track_files_album ON music_track_files(album_id);
CREATE INDEX idx_music_track_files_unprobed ON music_track_files(id) WHERE probed_at IS NULL;

-- Every other identifier, for bridging sources and following MusicBrainz merges.
CREATE TABLE music_external_ids (
    entity_type TEXT NOT NULL,   -- artist | album | release | recording | release_track
    entity_id   INTEGER NOT NULL,
    source      TEXT NOT NULL,   -- musicbrainz_old | isrc | upc | deezer | discogs_master | discogs_release | acoustid | wikidata
    value       TEXT NOT NULL,
    PRIMARY KEY (entity_type, entity_id, source, value)
);
CREATE INDEX idx_music_external_ids_lookup ON music_external_ids(source, value);

-- What the migration couldn't place one-to-one, kept rather than dropped. album_id has no
-- foreign key on purpose: the note outlives the album, like an audit line should.
CREATE TABLE music_migration_notes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    legacy_track_id INTEGER,
    album_id        INTEGER,
    path            TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT '',  -- shared_path | no_path | orphan | stale_path
    note            TEXT NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- A person looked at it (fixed the files, or accepted it) on the album page. Until
    -- then an album with an open shared_path / no_path note is left out of the search
    -- sweep: the old table said those tracks had a file, so they aren't wanted on a guess.
    resolved_at     TIMESTAMP
);
CREATE INDEX idx_music_migration_notes_track ON music_migration_notes(legacy_track_id);
CREATE INDEX idx_music_migration_notes_album ON music_migration_notes(album_id);

-- Albums (release groups): same rows, widened. After music_releases, which one references.
ALTER TABLE albums ADD COLUMN artist_credit_id  INTEGER REFERENCES music_artist_credits(id);
ALTER TABLE albums ADD COLUMN primary_type      TEXT NOT NULL DEFAULT '';  -- Album, EP, Single, Broadcast, Other
ALTER TABLE albums ADD COLUMN secondary_types   TEXT NOT NULL DEFAULT '';  -- "Compilation,Live"
ALTER TABLE albums ADD COLUMN disambiguation    TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN target_release_id INTEGER REFERENCES music_releases(id) ON DELETE SET NULL;
ALTER TABLE albums ADD COLUMN edition_pinned    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE albums ADD COLUMN source            TEXT NOT NULL DEFAULT 'musicbrainz';
ALTER TABLE albums ADD COLUMN stale_since       TIMESTAMP;

-- "Appears on" and an artist's removal look albums up by the credits an artist is in.
CREATE INDEX idx_albums_credit ON albums(artist_credit_id);
CREATE INDEX idx_music_releases_credit ON music_releases(artist_credit_id);
CREATE INDEX idx_music_release_tracks_credit ON music_release_tracks(artist_credit_id) WHERE artist_credit_id IS NOT NULL;
CREATE INDEX idx_music_recordings_credit ON music_recordings(artist_credit_id) WHERE artist_credit_id IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- The copy.

-- One-name credit per artist, and each album filed under it takes that credit.
INSERT INTO music_artist_credits (display, credit_key)
SELECT name, mbid FROM artists ORDER BY id;

INSERT INTO music_artist_credit_names (credit_id, position, artist_id, credited_name, join_phrase)
SELECT c.id, 0, a.id, a.name, ''
  FROM artists a JOIN music_artist_credits c ON c.credit_key = a.mbid;

UPDATE albums SET
    artist_credit_id = (SELECT c.id FROM artists a JOIN music_artist_credits c ON c.credit_key = a.mbid
                         WHERE a.id = albums.artist_id),
    -- album_type stays the display label. MusicBrainz's primary types map straight across;
    -- the secondary ones (only ever stored as the whole type) are an Album with that
    -- secondary type, as MusicBrainz itself files them.
    primary_type = CASE
        WHEN album_type IN ('Live', 'Compilation', 'Soundtrack', 'Remix') THEN 'Album'
        WHEN album_type = '' THEN 'Album'
        ELSE album_type END,
    secondary_types = CASE
        WHEN album_type IN ('Live', 'Compilation', 'Soundtrack', 'Remix') THEN album_type
        ELSE '' END;

-- One legacy edition per album that has a listing. Albums with no tracks rows (listing
-- never fetched) get none: the first listing fetch (and MUS-30) fills it.
INSERT INTO music_releases (album_id, mbid, title, artist_credit_id, track_count, disc_count,
                            source, legacy, tracks_fetched)
SELECT al.id, '', al.title, al.artist_credit_id, COUNT(*), COUNT(DISTINCT t.disc_number),
       'musicbrainz', 1, 1
  FROM albums al JOIN tracks t ON t.album_id = al.id
 GROUP BY al.id
 ORDER BY al.id;

-- Its discs, from the distinct disc numbers.
INSERT INTO music_media (release_id, position, track_count)
SELECT r.id, t.disc_number, COUNT(*)
  FROM music_releases r JOIN tracks t ON t.album_id = r.album_id
 WHERE r.legacy = 1
 GROUP BY r.id, t.disc_number
 ORDER BY r.id, t.disc_number;

-- One recording per tracks row (see the top: no recording MBIDs were ever stored).
INSERT INTO music_recordings (id, mbid, title, length_ms, legacy_track_id)
SELECT t.id, '', t.title, MAX(t.duration_sec, 0) * 1000, t.id
  FROM tracks t JOIN albums al ON al.id = t.album_id
 ORDER BY t.id;

-- One release track per tracks row, same id, monitored copied. The old table's
-- UNIQUE(album_id, disc_number, track_number) is what guarantees (disc, position) is free.
INSERT INTO music_release_tracks (id, medium_id, recording_id, mbid, position, number, title,
                                  length_ms, monitored, legacy_track_id)
SELECT t.id, m.id, t.id, t.mbid, t.track_number, CAST(t.track_number AS TEXT), t.title,
       MAX(t.duration_sec, 0) * 1000, t.monitored, t.id
  FROM tracks t
  JOIN music_releases r ON r.album_id = t.album_id AND r.legacy = 1
  JOIN music_media m ON m.release_id = r.id AND m.position = t.disc_number
 ORDER BY t.id;

UPDATE albums SET target_release_id =
    (SELECT r.id FROM music_releases r WHERE r.album_id = albums.id AND r.legacy = 1);

-- The rows that claim a file. When several claim one path, the first one (lowest id, of an
-- album that still exists) keeps it; claimant names that row for the others' notes.
CREATE TEMP TABLE music_model_files AS
SELECT x.*,
       ROW_NUMBER()  OVER (PARTITION BY x.path ORDER BY x.has_album DESC, x.track_id) AS claim,
       FIRST_VALUE(x.track_id) OVER (PARTITION BY x.path ORDER BY x.has_album DESC, x.track_id) AS claimant
  FROM (SELECT t.id AS track_id, t.album_id, t.file_path AS path, t.format, t.bitrate_kbps,
               t.size_bytes, t.source_release,
               EXISTS (SELECT 1 FROM albums al WHERE al.id = t.album_id) AS has_album,
               -- The extension after the last dot ("flac"), '' when the name has none.
               CASE WHEN instr(t.file_path, '.') = 0 THEN ''
                    ELSE lower(replace(t.file_path, rtrim(t.file_path, replace(t.file_path, '.', '')), ''))
               END AS ext
          FROM tracks t
         WHERE t.has_file != 0) x; -- the old code only wrote 0 and 1; anything else non-zero is "has a file" too

INSERT INTO music_track_files (album_id, release_track_id, path, size_bytes, container, lossless,
                               bitrate_kbps, quality, tag_state, source_release)
SELECT f.album_id, f.track_id, f.path, f.size_bytes,
       CASE WHEN instr(f.ext, '/') > 0 OR instr(f.ext, '\') > 0 THEN '' ELSE f.ext END,
       CASE WHEN upper(f.format) IN ('FLAC', 'FLAC-24', 'ALAC', 'WAV', 'AIFF', 'APE', 'WV') THEN 1 ELSE 0 END,
       f.bitrate_kbps, f.format, 'as_found', f.source_release
  FROM music_model_files f
 WHERE f.has_album = 1 AND f.path != '' AND f.claim = 1
 ORDER BY f.track_id;

INSERT INTO music_migration_notes (legacy_track_id, album_id, path, kind, note)
SELECT f.track_id, f.album_id, f.path,
       CASE WHEN f.has_album = 0 THEN 'orphan' WHEN f.path = '' THEN 'no_path' ELSE 'shared_path' END,
       CASE WHEN f.has_album = 0
                 THEN 'Track ' || f.track_id || ' had a file but its album ' || f.album_id || ' no longer exists; the file was left where it is.'
            WHEN f.path = ''
                 THEN 'Track ' || f.track_id || ' was marked as having a file but no path was recorded.'
            ELSE 'Track ' || f.track_id || ' recorded the same file as track ' || f.claimant ||
                 ', which keeps it; this track is listed without a file.'
       END
  FROM music_model_files f
 WHERE NOT (f.has_album = 1 AND f.path != '' AND f.claim = 1)
 ORDER BY f.track_id;

-- A path left on a row marked without a file isn't a file the library had, but it's a path
-- the old table held, so it's kept here too.
INSERT INTO music_migration_notes (legacy_track_id, album_id, path, kind, note)
SELECT t.id, t.album_id, t.file_path, 'stale_path',
       'Track ' || t.id || ' was marked without a file but still named this path.'
  FROM tracks t
 WHERE t.has_file = 0 AND t.file_path != ''
 ORDER BY t.id;

DROP TABLE music_model_files;

-- ---------------------------------------------------------------------------------------
-- @self-check: the migration checks its own copy. Each column counts the rows a check
-- finds wrong; any non-zero fails its CHECK, which fails the migration with that check's
-- name, rolls everything back and leaves the snapshot as the way back.
CREATE TEMP TABLE music_model_self_check (
    edition_tracks INTEGER
        CONSTRAINT "music_model self-check: an album's legacy edition doesn't have one release track per old track row"
        CHECK (edition_tracks = 0),
    target_edition INTEGER
        CONSTRAINT "music_model self-check: an album with a listing has no target edition"
        CHECK (target_edition = 0),
    album_files INTEGER
        CONSTRAINT "music_model self-check: an album's mapped files plus migration notes don't equal its old files"
        CHECK (album_files = 0),
    file_paths INTEGER
        CONSTRAINT "music_model self-check: an old file path is neither mapped to its track nor noted"
        CHECK (file_paths = 0),
    track_values INTEGER
        CONSTRAINT "music_model self-check: a release track's title, disc, position, monitored flag, MBID or album differs from its old row"
        CHECK (track_values = 0),
    file_values INTEGER
        CONSTRAINT "music_model self-check: a mapped file's path, tier, bitrate, size or source differs from its old row"
        CHECK (file_values = 0),
    credits INTEGER
        CONSTRAINT "music_model self-check: an artist or album has no artist credit"
        CHECK (credits = 0)
);
INSERT INTO music_model_self_check VALUES (
    -- Per album: release tracks of the legacy edition = old tracks rows.
    (SELECT COUNT(*)
       FROM (SELECT t.album_id, COUNT(*) AS n FROM tracks t
               JOIN albums al ON al.id = t.album_id GROUP BY t.album_id) o
       LEFT JOIN (SELECT r.album_id, COUNT(rt.id) AS n FROM music_releases r
                    JOIN music_media m ON m.release_id = r.id
                    JOIN music_release_tracks rt ON rt.medium_id = m.id
                   WHERE r.legacy = 1 GROUP BY r.album_id) e ON e.album_id = o.album_id
      WHERE e.n IS NULL OR e.n != o.n),
    (SELECT COUNT(*) FROM albums al
      WHERE EXISTS (SELECT 1 FROM tracks t WHERE t.album_id = al.id)
        AND NOT EXISTS (SELECT 1 FROM music_releases r
                         WHERE r.id = al.target_release_id AND r.album_id = al.id AND r.legacy = 1)),
    -- Per album: mapped files + notes = old has_file rows.
    (SELECT COUNT(*)
       FROM (SELECT album_id, COUNT(*) AS n FROM tracks WHERE has_file != 0 GROUP BY album_id) h
       LEFT JOIN (SELECT album_id, COUNT(*) AS n FROM music_track_files GROUP BY album_id) f
              ON f.album_id = h.album_id
       LEFT JOIN (SELECT album_id, COUNT(*) AS n FROM music_migration_notes
                   WHERE kind IN ('shared_path', 'no_path', 'orphan') GROUP BY album_id) x
              ON x.album_id = h.album_id
      WHERE COALESCE(f.n, 0) + COALESCE(x.n, 0) != h.n),
    -- Every old file row is either its own track's file, with its path, or noted.
    (SELECT COUNT(*) FROM tracks t
      WHERE t.has_file != 0
        AND NOT EXISTS (SELECT 1 FROM music_track_files f
                         WHERE f.release_track_id = t.id AND f.path = t.file_path)
        AND NOT EXISTS (SELECT 1 FROM music_migration_notes n WHERE n.legacy_track_id = t.id)),
    (SELECT COUNT(*) FROM music_release_tracks rt
       JOIN tracks t ON t.id = rt.legacy_track_id
       JOIN music_media m ON m.id = rt.medium_id
       JOIN music_releases r ON r.id = m.release_id
      WHERE rt.title != t.title OR rt.position != t.track_number OR rt.monitored != t.monitored
         OR rt.mbid != t.mbid OR m.position != t.disc_number OR r.album_id != t.album_id),
    (SELECT COUNT(*) FROM music_track_files f JOIN tracks t ON t.id = f.release_track_id
      WHERE f.path != t.file_path OR f.quality != t.format OR f.bitrate_kbps != t.bitrate_kbps
         OR f.size_bytes != t.size_bytes OR f.source_release != t.source_release
         OR f.album_id != t.album_id),
    (SELECT COUNT(*) FROM artists a
      WHERE NOT EXISTS (SELECT 1 FROM music_artist_credits c
                          JOIN music_artist_credit_names n ON n.credit_id = c.id
                         WHERE c.credit_key = a.mbid AND n.artist_id = a.id))
    + (SELECT COUNT(*) FROM albums al
        WHERE al.artist_credit_id IS NULL AND EXISTS (SELECT 1 FROM artists a WHERE a.id = al.artist_id))
);
DROP TABLE music_model_self_check;
