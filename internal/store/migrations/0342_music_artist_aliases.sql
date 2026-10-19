-- 0342_music_artist_aliases (MUS-38): an artist's other names, from MusicBrainz.
--
-- A release named by an alias ("Utada Hikaru - First Love" for 宇多田ヒカル, an old band
-- name, a legal name) never matched, because search, release matching and the library scan
-- only knew the primary name. MusicBrainz lists the aliases with the artist, with the locale
-- each is for, its sort name and its type (Artist name, Legal name, Search hint).
--
--   folded  music.NormKey(name): how matching compares names (accents, "&"/"and", case)
--
-- An alias widens matching only for the artist's own albums, and only as the release's
-- whole artist part; one that folds to another artist's name is never used (music.NameIndex).
CREATE TABLE music_artist_aliases (
    artist_id  INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    sort_name  TEXT    NOT NULL DEFAULT '',
    locale     TEXT    NOT NULL DEFAULT '',
    alias_type TEXT    NOT NULL DEFAULT '',
    is_primary INTEGER NOT NULL DEFAULT 0,
    folded     TEXT    NOT NULL,
    PRIMARY KEY (artist_id, name)
) WITHOUT ROWID;

CREATE INDEX idx_music_artist_aliases_folded ON music_artist_aliases(folded);

-- The artist lookup now asks MusicBrainz for aliases too (inc=aliases+genres), which is a
-- different cache key. The answers cached under the old key move to the new one, marked
-- due, so nothing is lost and nothing is left behind for the stale-refresh task to keep
-- refetching under a key no lookup reads any more. Their bodies carry no aliases until
-- they are refetched; that happens as they come due (the artist refresh, a page open).
UPDATE OR IGNORE music_mb_cache
   SET key = substr(key, 1, length(key) - length('?inc=genres')) || '?inc=aliases+genres',
       checked_at = 0
 WHERE key LIKE 'artist:/artist/%?inc=genres';
