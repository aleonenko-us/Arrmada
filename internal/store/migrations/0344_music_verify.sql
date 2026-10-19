-- 0344_music_verify (MUS-15): the verify pass over recorded music files.
--
-- music_track_files.missing_seen_at: the first verify pass that couldn't find the file.
-- It is not missing yet: a file only becomes missing (missing_since, set to this time) when
-- a later pass, at least a grace period on, still can't find it. One miss — a share that
-- was briefly away, a folder mid-rename — never changes what the library has. Cleared the
-- moment the file is seen again.
--
-- music_track_files.changed_at: when a verify pass found the file changed outside Arrmada
-- (its size or modification time no longer what was recorded). The file was measured again
-- and its house-tags state rechecked; the album page says so. Cleared when Arrmada next
-- writes or places the file.
ALTER TABLE music_track_files ADD COLUMN missing_seen_at TIMESTAMP;
ALTER TABLE music_track_files ADD COLUMN changed_at TIMESTAMP;
