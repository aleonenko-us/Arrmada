-- 0273_ranking_shadow: what ranking v2 would have picked while classic ranking decides.
--
-- While quality_ranking is v1 (the default), every automatic decision also runs ranking v2
-- (quality/shadow.go). A decision where the two pick different releases is one row here,
-- per profile and pair of winners however often it recurs (seen counts the repeats), with
-- the key each ranking preferred its own pick on. The day table counts the decisions that
-- had a choice to make and how many disagreed. Read with GET /api/v1/quality/ranking-shadow
-- (admin; ?format=csv to export). Kept 180 days.
CREATE TABLE ranking_shadow (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    profile    TEXT    NOT NULL,           -- the profile ref the decision ran under
    v1_winner  TEXT    NOT NULL,           -- the release classic ranking picked
    v1_key     TEXT    NOT NULL DEFAULT '', -- the step classic ranking preferred it on
    v2_winner  TEXT    NOT NULL,           -- the release v2 would have picked
    v2_key     TEXT    NOT NULL DEFAULT '', -- the key v2 preferred its pick on
    candidates INTEGER NOT NULL DEFAULT 0,  -- eligible releases in the latest such decision
    first_seen INTEGER NOT NULL,           -- unix seconds
    last_seen  INTEGER NOT NULL,
    seen       INTEGER NOT NULL DEFAULT 1,
    UNIQUE (profile, v1_winner, v2_winner)
);
CREATE INDEX idx_ranking_shadow_last_seen ON ranking_shadow (last_seen);

CREATE TABLE ranking_shadow_days (
    day           TEXT    PRIMARY KEY,      -- UTC, YYYY-MM-DD
    decisions     INTEGER NOT NULL DEFAULT 0,
    disagreements INTEGER NOT NULL DEFAULT 0
);
