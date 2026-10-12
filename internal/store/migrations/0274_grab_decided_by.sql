-- 0274_grab_decided_by: every automatic pick records what decided it.
--
-- decided_by is the ranking key the grabbed release won on (quality.RankKey: resolution,
-- preferences, custom, source, proper, group, bitrate, health, size, avoid, or "only" for
-- the one eligible release, "tie" for an equal match); decided_mode is how it was decided:
-- "v1" (classic ranking), "v2" (ordered keys) or "upgrade" (an upgrade over the file on
-- disk, where the key is the kind of gain). Both are '' for a grab no ranking chose — a
-- manual pick, an uploaded torrent, a book or album, and every grab made before this.
ALTER TABLE grabs ADD COLUMN decided_by TEXT NOT NULL DEFAULT '';
ALTER TABLE grabs ADD COLUMN decided_mode TEXT NOT NULL DEFAULT '';
