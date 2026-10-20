-- 0351_discover_not_interested: Discover's "Not interested" and the Plex watchlist's
-- automatic requests.
--
-- discover_not_interested: titles a person has hidden from their own Discover. They never
-- show again in a row, a browse grid or search results for that person, until restored
-- (Discover → Hidden titles). The title, year and poster are kept so the hidden list can
-- show what each one was without asking TMDB.
--
-- plex_watchlist_requests: every watchlist title "Request everything on my Plex watchlist
-- automatically" has dealt with, and how, so each is asked for once: a title taken off
-- and put back on the watchlist isn't asked for again, nor one the owner declined.
-- outcome is requested | joined | in_library | declined | unavailable | failed.
CREATE TABLE discover_not_interested (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_type TEXT    NOT NULL,
    tmdb_id    INTEGER NOT NULL,
    title      TEXT    NOT NULL DEFAULT '',
    year       INTEGER NOT NULL DEFAULT 0,
    poster_url TEXT    NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, media_type, tmdb_id)
);

CREATE TABLE plex_watchlist_requests (
    media_type TEXT    NOT NULL,
    tmdb_id    INTEGER NOT NULL,
    user_id    INTEGER NOT NULL DEFAULT 0,
    title      TEXT    NOT NULL DEFAULT '',
    outcome    TEXT    NOT NULL DEFAULT '',
    at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (media_type, tmdb_id)
);
