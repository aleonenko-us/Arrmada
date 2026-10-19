-- 0335_music_discography_grabs: a discography is grabbed for an ARTIST, and its grab and
-- blocklist rows hold the artist's id in movie_id. They were written with media_type
-- 'music', the album grabs' type, so the stall check read the artist id as an album id and
-- judged the discography by whichever album shared the number: failed within minutes when
-- there was no such album, 'imported' (and seed-cleaned mid-import) when that album was
-- complete, and stall blocks landed on the unrelated album.
--
-- They get a type of their own, 'music_artist'. Which existing rows are discographies is
-- read off the release name, with the same words the import routes them by
-- (music.ParseRelease: discography, complete collection, all albums, box set, anthology).
-- An album grab never carries those words: the album search refuses such releases.
UPDATE grabs SET media_type = 'music_artist'
 WHERE media_type = 'music'
   AND (lower(title) LIKE '%discography%' OR lower(title) LIKE '%complete collection%'
        OR lower(title) LIKE '%all albums%' OR lower(title) LIKE '%box set%'
        OR lower(title) LIKE '%boxset%' OR lower(title) LIKE '%anthology%');

UPDATE blocklist SET media_type = 'music_artist'
 WHERE media_type = 'music'
   AND (lower(title) LIKE '%discography%' OR lower(title) LIKE '%complete collection%'
        OR lower(title) LIKE '%all albums%' OR lower(title) LIKE '%box set%'
        OR lower(title) LIKE '%boxset%' OR lower(title) LIKE '%anthology%');
