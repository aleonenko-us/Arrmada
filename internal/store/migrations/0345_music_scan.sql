-- 0345_music_scan (MUS-37): the library scan remembers what it has looked at.
--
-- music_scan_dirs: one row per album folder (a disc folder's parent) the scan has judged.
--   sig        the folder's (and its disc folders') modification time, size and inode, and
--              how many audio files it holds: unchanged, the folder isn't opened again
--   files_sig  the audio files' sizes and modification times, for a folder sent to Reviews
--              (a retag in place doesn't touch the folder itself)
--   files      how many of its files were on record when it was judged: fewer now (an
--              artist removed, say) and it is looked at again
--   state      done (its files are recorded, or there was nothing new) | unmatched (in Reviews)
--              | waiting (its album has no track listing yet: looked at again, after the rest)
-- It's a cache: losing a row only costs the scan a second look at that folder.
--
-- music_scan_runs: each scan's summary (counts and timings), for the results drawer. The
-- last twenty are kept.
--
-- music_track_files.file_id is indexed so a renamed folder's files are found by inode.
CREATE TABLE music_scan_dirs (
    path       TEXT    PRIMARY KEY,
    sig        TEXT    NOT NULL,
    files_sig  TEXT    NOT NULL DEFAULT '',
    files      INTEGER NOT NULL DEFAULT 0,
    state      TEXT    NOT NULL,
    scanned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) WITHOUT ROWID;

CREATE TABLE music_scan_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at   TIMESTAMP NOT NULL,
    finished_at  TIMESTAMP NOT NULL,
    summary_json TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_music_track_files_file_id ON music_track_files(file_id) WHERE file_id != '';
