-- 0216_convert_library_version: Convert's library index covers every track of a movie,
-- not just the default file.
--
-- A 4K remux kept as an extra version (movie_versions) is often the biggest file in the
-- library, and it never appeared in Convert's lists, stats or reclaimable figure because
-- the index held one row per movie: its default file. Each indexed movie file now records
-- which track it belongs to. version_id 0 is the default track (the movie row), so every
-- existing row already means what it did; version_label is the track's name, for the
-- Library's version chip and the job's title.
--
-- Item keys follow: 'movie:ID' stays the default track, 'movie:ID:v:VID' is a version, so
-- requests, skips and failures recorded under the old keys keep their meaning.
ALTER TABLE convert_library ADD COLUMN version_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE convert_library ADD COLUMN version_label TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_convert_library_movie ON convert_library(movie_id, version_id);
