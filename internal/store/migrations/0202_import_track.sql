-- 0202_import_track: the movie and track a recorded import was placed for (MOV-12).
--
-- A movie with extra tracks (a 4K track, a Director's Cut) used to have its download
-- routed to a track only at attach time, by the new file's resolution, after the
-- importer had already named it from the default scheme. Two tracks at the same
-- resolution rendered the same path, so a Director's Cut download replaced the
-- theatrical file. The track is now decided before the file is placed, named for that
-- track, and kept here so the attach (and every retry of it) lands on the same track.
--
-- movie_id 0 / version_id -1: the import didn't decide (rows from before this, and
-- downloads no movie matched); the attach routes those as it always has.
ALTER TABLE imports ADD COLUMN movie_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE imports ADD COLUMN version_id INTEGER NOT NULL DEFAULT -1;
