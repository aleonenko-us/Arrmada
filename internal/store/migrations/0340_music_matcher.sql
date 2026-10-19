-- 0340_music_matcher: the music matcher (MUS-30, MUS-31).
--
-- import_reviews.match_json: a review the matcher held keeps its explanation here (the
-- candidates it weighed, each one's distance and per-field breakdown, the files it would
-- leave), so Reviews can say in words why the download is waiting. '' for every other
-- review, and for reviews written before this.
--
-- albums.editions_checked_at: when the album's editions were last listed from MusicBrainz
-- (one browse call stores every release of the group). NULL = never: the album has only the
-- edition its first listing was read from, or the migration's legacy edition, and the next
-- time it's opened, searched or imported its editions are listed and the legacy one re-keyed.
ALTER TABLE import_reviews ADD COLUMN match_json TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN editions_checked_at TIMESTAMP;
