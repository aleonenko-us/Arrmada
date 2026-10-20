-- 0341_music_refresh: Music keeps itself current (MUS-13, MUS-14).
--
-- artists.last_refreshed_at: when the scheduled refresh (or the Refresh button) last read
-- the artist's albums from MusicBrainz. NULL = never; those go first.
--
-- artists.monitor_new: whether albums MusicBrainz newly lists for the artist are wanted:
-- 'all' or 'none'. '' follows the library default (Settings, music_monitor_new).
--
-- artists.release_types: which kinds of release group are listed for the artist, as JSON
-- {"primary":["Album","EP"],"secondary":[]}. A release group is listed when its primary type
-- is allowed AND every one of its secondary types is. '' follows the library default
-- (music_release_types), whose own default is Album and EP with no secondary types:
-- exactly the list Music has always shown.
--
-- music_rg_seen + artists.rg_seen_scopes: every release group MusicBrainz has listed for an
-- artist, whether or not it passed the types. A release group not seen before is new on
-- MusicBrainz; one seen before that only now passes the types (the owner ticked Live, say)
-- is old music, listed but never wanted on its own. rg_seen_scopes says which browses have
-- been seen at least once ('main' = albums and EPs, 'other' = singles, broadcasts, other),
-- so the first browse of a scope is a first look, not a flood of "new" releases.
--
-- albums.found_at / found_as: an album a refresh added after the artist was, and why it is
-- or isn't wanted ('new', 'not_new', 'back_catalogue', 'too_many', 'undated_pending',
-- 'single_pending', 'single_asked', 'single_on_album'), for the New releases list. NULL /
-- '' for albums added with their artist (a single added with its artist can still be
-- 'single_pending').
--
-- albums.listing_tried_at: when the refresh last tried to fill a monitored album's missing
-- listing, so albums MusicBrainz has no release for yet (announced ones) go to the back of
-- the line instead of taking every slot each run.
ALTER TABLE artists ADD COLUMN last_refreshed_at TIMESTAMP;
ALTER TABLE artists ADD COLUMN monitor_new TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN release_types TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN rg_seen_scopes TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN found_at TIMESTAMP;
ALTER TABLE albums ADD COLUMN found_as TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN listing_tried_at TIMESTAMP;
-- albums.departure_checked_at: an album that left its artist's MusicBrainz list was looked
-- up by MBID and still exists there (credited elsewhere now, say). It is kept as it is and
-- looked at again only in turn, oldest first, so it never holds up new releases.
ALTER TABLE albums ADD COLUMN departure_checked_at TIMESTAMP;
-- artists.pending_add_monitor: the add dialog's choice (all, future, latest, none) when the
-- add couldn't read the artist's albums. The first refresh that can applies it exactly as
-- the add would have, instead of treating the artist as an upgraded library's first look.
ALTER TABLE artists ADD COLUMN pending_add_monitor TEXT NOT NULL DEFAULT '';

CREATE TABLE music_rg_seen (
    artist_id INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    mbid      TEXT NOT NULL,
    PRIMARY KEY (artist_id, mbid)
) WITHOUT ROWID;

CREATE INDEX idx_artists_last_refreshed ON artists(last_refreshed_at);
CREATE INDEX idx_albums_found ON albums(found_at) WHERE found_at IS NOT NULL;
