-- 0241_subtitle_profiles: which subtitles each title wants, by language AND variant.
--
-- Wanted languages used to be one flat list (the subs_languages setting), so there was no
-- way to ask for a forced subtitle on purpose, to prefer or avoid SDH, to want English
-- only when the film isn't in English, or to give one show (anime, say) its own set. A
-- profile is an ordered list of items, each {lang, variant: full|forced|both,
-- sdh: any|prefer|avoid, only_if_audio_differs}. Exactly one profile is the default; every
-- title without an override uses it.
--
-- The Default profile is NOT seeded here: it comes from the subs_languages setting, which
-- is only readable through the settings service (codes saved before the shared language
-- table need canonicalising, and an unset key means Go's default). The subtitles module
-- seeds it on first use, so an existing install keeps exactly the languages it had.
--
-- An override pins one movie or one series (all its episodes) to a profile. It lives in
-- its own table so the movies and series tables don't change; a profile's deletion takes
-- its overrides with it, and those titles go back to the default.
CREATE TABLE IF NOT EXISTS subtitle_profiles (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    items_json  TEXT NOT NULL,
    is_default  INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS subtitle_profiles_one_default
    ON subtitle_profiles (is_default) WHERE is_default = 1;

CREATE TABLE IF NOT EXISTS subtitle_profile_overrides (
    media_kind  TEXT NOT NULL CHECK (media_kind IN ('movie', 'series')),
    media_id    INTEGER NOT NULL,
    profile_id  INTEGER NOT NULL REFERENCES subtitle_profiles (id) ON DELETE CASCADE,
    PRIMARY KEY (media_kind, media_id)
);
