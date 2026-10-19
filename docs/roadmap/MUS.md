# MUS — Music

_Part of the [Arrmada roadmap](../../ROADMAP.md). 50 tasks. After each task: priority, size, and the phase it ships in. Reworked 2026-10-19 into the rebuild plan after [MUS-03](#mus-03) decided **Rebuild**; the decision and its reasons are in [docs/decisions/music.md](../decisions/music.md)._

**Goal.** Rebuild Music as a first-class module that is better than Lidarr, not a copy of it. It talks to MusicBrainz directly and keeps a permanent local cache, so no upstream outage can block it or delete anything. It stores every edition of an album and lets any acceptable edition satisfy it. It imports in tiers rather than pass/fail: confident matches go straight in, doubtful ones go to Reviews with a per-field explanation, and the tracks that do match are imported even when others don't. It measures files instead of guessing from names. It writes one house tag style in a staging copy, so Plex, Jellyfin and Navidrome all read the library correctly and seeding torrents are never touched. Then it lets the household ask for albums and songs, and goes beyond Lidarr with gap-fill sources, lyrics, soundtracks for the films and shows already in the library, and suggestions.

**Why.** Music scored 3/10 in the 2026-10-08 audit. Containment shipped in Phase 0 (MUS-01, MUS-02): Music is off by default, labelled Preview, stops when switched off, and backs off its searches. What's left is structural:

1. **It still grabs and files the wrong things** (music-2..6): ASCII-only matching, substring quality detection, misparsed numeric titles, partial albums that seed forever and can be downgraded, and discography grabs stored under the wrong id. These are Phase 1.
2. **Its schema can't describe music** (from the 2026-10-19 research). Artists → albums → tracks with the file stored on the track row cannot hold editions, two artists on one album, featured artists, Various Artists compilations, or the real properties of a file. MusicBrainz is called on every page open with nothing cached. No tags are read or written, because no tag library exists in go.mod. These are Phase 2.
3. **It doesn't run itself or reach the household** (music-7..10, 13..15): no scheduled refresh, no upgrade sweep, no artwork, no music in Discover or Requests, no Plex scan after imports. These are Phases 3 and 4.

The research's conclusion is that Lidarr's worst failures (its single metadata proxy, one edition per album picked by size, hard-coded all-or-nothing import gates, flattened artist tags, retagging seeding files) are design choices, not hard problems. This plan doesn't make them.

**Depends on:**
- **Shipped and relied on:** BE's migration runner and pre-migration snapshot, the job runner (a.submit / useJob), the outbox and the scheduler's labelled tasks; SAFE's per-root recycle bins (library.RemoveToBin / CheckBin, refuse on bin failure) and hidden Arrmada folders (library.PrepareHiddenDir, SkipScanDir); ACQ-16 (music searches write search_attempts), ACQ-18 (cross-media Wanted, albums included, with Search now), ACQ-21 (parser.FoldTitle / TitleWords as the one normaliser), the Reviews reason codes; SEC-07 release tokens; INT-23 audio categories from caps; the Connections hub and apikeys Hidden entries for keys; toolrun for every ffmpeg/ffprobe/fpcalc call; plexscan partial scans; Requests 2.0 (CreateOptions, announceCreated, shapeRequests); the Discover features/discover/* structure; the FE kit and LibraryGrid.
- **Owner sign-off:** the tag library licence ([MUS-27](#mus-27)) and the open questions at the end of this file.
- **Migrations:** every task that needs one takes the next free number at commit time and never renumbers a shipped file. Additive by default; no DROP anywhere. The old `tracks` table is frozen, not dropped.

#### Design

## Target state

Music is a module the owner can leave running. It shows every edition of an album, imports the right files from any acceptable edition, writes tags that every player reads, explains every doubtful import in Reviews, keeps itself current, and lets family members request an album or a single song from Discover. Plex is the player; the library also works as-is for Jellyfin and Navidrome.

### Principles

| # | Principle | In practice |
|---|---|---|
| 1 | Never let an upstream service block work or delete files | Imports work from embedded MBIDs and the local cache. A failed or empty lookup marks data stale, never gone. Deletes and replaces go to the recycle bin |
| 2 | Model editions explicitly | Every release of an album is stored; a target edition is picked by a stated policy; editions with identical track lists are interchangeable; a correct download of any acceptable edition satisfies the album |
| 3 | Import in tiers, not pass/fail | Confident and unambiguous → automatic. Doubtful → Reviews with a per-field breakdown. Tracks that match are imported; the rest stay listed as missing |
| 4 | Measure files, don't guess from names | ffprobe gives codec, bit depth, sample rate and bitrate. The release name only decides what to grab |
| 5 | One house tag style, written safely | Every MBID, multi-value artists, compilation flag, original date, cover. Written in staging, then one move. A seeding file is copied, never retagged |
| 6 | Let people want what they want | A single album without following the artist, a single song, a compilation without "Various Artists", soundtracks of films already in the library |

### Phases

Every task ships to main behind the Music module toggle. The module keeps its Preview label until [MUS-12](#mus-12) (after R1 and R2 soak).

| Phase | Goal | Tasks | Estimate (rough, not a schedule) |
|---|---|---|---|
| **R1 Safety** | No wrong files, no wasted calls, the foundations proven | [MUS-04](#mus-04)..[MUS-08](#mus-08), [MUS-11](#mus-11), [MUS-26](#mus-26), [MUS-27](#mus-27) (4 S, 4 M) | ≈ 14–18 working days, 3–3.5 weeks |
| **R2 Core model** | Editions, files and credits that match reality; the matcher and the tagger | [MUS-28](#mus-28)..[MUS-36](#mus-36), [MUS-12](#mus-12) (4 L, 4 M, 2 S) | ≈ 35–45 working days, 7–9 weeks, plus a 14-day soak |
| **R3 Stay current** | It runs itself | [MUS-13](#mus-13), [MUS-14](#mus-14), [MUS-09](#mus-09), [MUS-15](#mus-15), [MUS-16](#mus-16), [MUS-17](#mus-17), [MUS-21](#mus-21), [MUS-37](#mus-37), [MUS-38](#mus-38), [MUS-24](#mus-24) (2 L, 4 M, 4 S) | ≈ 25–30 working days, 5–6 weeks |
| **R4 Household** | Family can ask for music and find it in Plex | [MUS-39](#mus-39)..[MUS-41](#mus-41), [MUS-22](#mus-22), [MUS-23](#mus-23) (1 L, 4 M) | ≈ 15–20 working days, 3–4 weeks |
| **R5 Beyond Lidarr** | Features Lidarr never had | [MUS-42](#mus-42)..[MUS-50](#mus-50) (7 M, 2 S) | ≈ 18–24 working days, 4–5 weeks; can be split and reordered |

All estimates are judgement, not measurement: about 22–28 weeks of focused sessions in total, roughly five to seven months for one owner and an AI agent. R2 must land before R4: otherwise every requested compilation or deluxe edition hits the one-edition, one-artist walls.

Superseded by the new model: [MUS-10](#mus-10) → [MUS-29](#mus-29), [MUS-18](#mus-18) → ACQ-18 (shipped) + [MUS-09](#mus-09), [MUS-19](#mus-19) → [MUS-30](#mus-30), [MUS-20](#mus-20) → [MUS-37](#mus-37). Won't do: [MUS-25](#mus-25) (the Cut path).

### Data safety rules (every task)

These apply to every task below; each task's **Data safety** line says how.

- **Never delete or overwrite the user's files without the recycle bin.** Replaced or removed files go through library.RemoveToBin / CheckBin on their own drive, and the operation refuses (leaving everything in place) when the bin fails. There is never an os.Remove fallback. Deleting a DB row never deletes a file.
- **Staging, then one move.** Anything that writes into an audio file (tags, cover, split) works on a copy in `<root>/.arrmada-staging/<job>/` (hidden, on the same drive, skipped by scanners, `.plexignore` inside), is checked, and then lands in the library with one rename per file. A crash leaves the staging job to be finished or rolled back on restart; a file whose only copy is in staging is never deleted.
- **Copy instead of hardlink when retagging a seeding file.** If the download client still holds the torrent (or the file has other hard links), the file is copied into staging and the copy is tagged. The seeding file is never written. Files that won't be retagged (extras, or tag writing switched off) are hardlinked as today.
- **Upstream failures change nothing.** A failed, empty or 503 answer from MusicBrainz or any other source never removes, unmonitors, renames or relists anything.
- **Tests never touch real libraries.** Temp dirs and synthetic media only (byte blobs, or ffmpeg lavfi-generated audio in env-gated tests run in the `arrmada:sycl` image with `--cpus=3` and an `arrmada-tests-` container name).

### Metadata: MusicBrainz client ([MUS-26](#mus-26))

```
every caller (add artist, refresh, import, scan, Discover, requests, art)
  → metadata.MusicBrainz (ONE per process, built in main; archtest forbids a second)
      → music_mb_cache: fresh → answer; stale → answer now + queue a background refresh;
                        missing → fetch
      → limiter: musicbrainz.org = 1 request / 1.1 s, hard ceiling; a mirror = its own rate
                 two lanes: interactive (someone is waiting) ahead of background
      → User-Agent: Arrmada/<build version> ( https://github.com/tristenlammi/Arrmada[; contact] )
      → 503 / timeout / empty: serve the cached answer; nothing is ever deleted on it
```

Base URL is a setting (Settings → Connections → MusicBrainz): `https://musicbrainz.org` by default, or a self-hosted mirror. Arrmada never runs or depends on a central metadata proxy.

### Data model ([MUS-28](#mus-28))

MusicBrainz's own shape, with Arrmada's ids. The existing `artists` and `albums` tables are kept and widened, so every id that grabs, blocklist, search_attempts, reviews, requests and artist_events already store stays valid. `albums` keeps its name and means **release group** (what people call an album).

```
artists ─< music_artist_credit_names >─ music_artist_credits
                                              │ (used by albums, releases, recordings, tracks)
albums (release group) ─< music_releases (edition) ─< music_media (disc) ─< music_release_tracks
                                                                                 │        │
                                                              music_recordings ──┘        │
                                                              music_track_files ──────────┘ (0..1 file per release track)
music_external_ids: (entity_type, entity_id) → MBID history, ISRC, UPC, Deezer, Discogs, AcoustID, Wikidata
```

```sql
-- artists (existing; ids, monitored, quality_profile and search state kept)
ALTER TABLE artists ADD COLUMN in_library     INTEGER NOT NULL DEFAULT 1; -- 0 = only credited (featured, Various Artists): hidden from the library list
ALTER TABLE artists ADD COLUMN disambiguation TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN artist_type    TEXT NOT NULL DEFAULT '';   -- Person, Group, Orchestra…
ALTER TABLE artists ADD COLUMN country        TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN stale_since    TIMESTAMP;                  -- MusicBrainz stopped answering for it; data kept

-- Artist credits: many-to-many with join phrases ("Daft Punk feat. Pharrell Williams & Nile Rodgers")
CREATE TABLE music_artist_credits (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    display    TEXT NOT NULL,                 -- rendered from the names and join phrases
    credit_key TEXT NOT NULL UNIQUE           -- "mbid|join|mbid|join…": an identical credit is stored once
);
CREATE TABLE music_artist_credit_names (
    credit_id     INTEGER NOT NULL REFERENCES music_artist_credits(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    artist_id     INTEGER NOT NULL REFERENCES artists(id),  -- no cascade: removing an artist from the library sets in_library=0
    credited_name TEXT NOT NULL,              -- the name as credited here (may differ from the artist's name)
    join_phrase   TEXT NOT NULL DEFAULT '',   -- " feat. ", " & ", ", " …
    PRIMARY KEY (credit_id, position)
);
CREATE INDEX idx_music_credit_names_artist ON music_artist_credit_names(artist_id);

-- Editions
CREATE TABLE music_releases (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id         INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    mbid             TEXT NOT NULL DEFAULT '',   -- '' for the migrated legacy edition and for gap-fill releases
    title            TEXT NOT NULL,
    artist_credit_id INTEGER REFERENCES music_artist_credits(id),
    status           TEXT NOT NULL DEFAULT '',   -- Official, Promotion, Bootleg, Pseudo-Release
    release_date     TEXT NOT NULL DEFAULT '',   -- YYYY, YYYY-MM or YYYY-MM-DD
    country          TEXT NOT NULL DEFAULT '',
    label            TEXT NOT NULL DEFAULT '',
    catalog_number   TEXT NOT NULL DEFAULT '',
    barcode          TEXT NOT NULL DEFAULT '',
    packaging        TEXT NOT NULL DEFAULT '',
    disambiguation   TEXT NOT NULL DEFAULT '',   -- "deluxe edition", "2011 remaster"
    media_summary    TEXT NOT NULL DEFAULT '',   -- "2×CD", "Digital Media", "12\" Vinyl"
    track_count      INTEGER NOT NULL DEFAULT 0,
    disc_count       INTEGER NOT NULL DEFAULT 0,
    source           TEXT NOT NULL DEFAULT 'musicbrainz', -- musicbrainz | deezer | discogs
    legacy           INTEGER NOT NULL DEFAULT 0, -- 1 = built by the migration from the old tracks table
    tracks_fetched   INTEGER NOT NULL DEFAULT 0, -- 1 = the full track list is stored (summaries come from one browse call)
    fetched_at       TIMESTAMP,
    stale_since      TIMESTAMP
);
CREATE UNIQUE INDEX ux_music_releases_mbid ON music_releases(mbid) WHERE mbid != '';
CREATE INDEX idx_music_releases_album ON music_releases(album_id);

CREATE TABLE music_media (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id  INTEGER NOT NULL REFERENCES music_releases(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,                -- disc number, from 1
    format      TEXT NOT NULL DEFAULT '',        -- CD, Digital Media, Vinyl, SACD…
    title       TEXT NOT NULL DEFAULT '',        -- disc subtitle
    track_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(release_id, position)
);

-- Recordings (the distinct audio; one recording appears on many releases)
CREATE TABLE music_recordings (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    mbid             TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL,
    artist_credit_id INTEGER REFERENCES music_artist_credits(id),
    length_ms        INTEGER NOT NULL DEFAULT 0,
    disambiguation   TEXT NOT NULL DEFAULT '',
    legacy_track_id  INTEGER                      -- migration only: the old tracks.id of a recording with no MBID
);
CREATE UNIQUE INDEX ux_music_recordings_mbid ON music_recordings(mbid) WHERE mbid != '';

-- Tracks: a recording at a position on one disc of one edition
CREATE TABLE music_release_tracks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    medium_id        INTEGER NOT NULL REFERENCES music_media(id) ON DELETE CASCADE,
    recording_id     INTEGER NOT NULL REFERENCES music_recordings(id),
    mbid             TEXT NOT NULL DEFAULT '',    -- release-track MBID
    position         INTEGER NOT NULL,            -- on its disc
    number           TEXT NOT NULL DEFAULT '',    -- as printed: "3", "A1"
    title            TEXT NOT NULL,               -- this release's title for it
    artist_credit_id INTEGER REFERENCES music_artist_credits(id),
    length_ms        INTEGER NOT NULL DEFAULT 0,
    monitored        INTEGER NOT NULL DEFAULT 1,  -- per-track wants within an album
    legacy_track_id  INTEGER UNIQUE,              -- migration only: the tracks.id it was copied from
    UNIQUE(medium_id, position)
);
CREATE INDEX idx_music_release_tracks_recording ON music_release_tracks(recording_id);

-- Files: what is actually on disk, measured
CREATE TABLE music_track_files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id         INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE, -- row only; the file stays on disk
    release_track_id INTEGER REFERENCES music_release_tracks(id) ON DELETE SET NULL, -- NULL = an extra file not on the target edition
    path             TEXT NOT NULL UNIQUE,
    size_bytes       INTEGER NOT NULL DEFAULT 0,
    mtime_unix       INTEGER NOT NULL DEFAULT 0,
    file_id          TEXT NOT NULL DEFAULT '',    -- device+inode, so the scan notices a rename instead of a delete
    container        TEXT NOT NULL DEFAULT '',    -- flac, mp3, m4a, ogg, opus, wav…
    codec            TEXT NOT NULL DEFAULT '',    -- from ffprobe: flac, alac, mp3, aac, opus, vorbis, pcm_s16le…
    lossless         INTEGER NOT NULL DEFAULT 0,
    bitrate_kbps     INTEGER NOT NULL DEFAULT 0,
    bitrate_mode     TEXT NOT NULL DEFAULT '',    -- cbr | vbr | ''
    sample_rate_hz   INTEGER NOT NULL DEFAULT 0,
    bit_depth        INTEGER NOT NULL DEFAULT 0,
    channels         INTEGER NOT NULL DEFAULT 0,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    quality          TEXT NOT NULL DEFAULT '',    -- measured tier (TierFromProbe): FLAC-24, FLAC, MP3-V0…
    probed_at        TIMESTAMP,
    tags_json        TEXT NOT NULL DEFAULT '',    -- tags as found at import: audit trail, and what a retag replaced
    tag_state        TEXT NOT NULL DEFAULT 'as_found', -- as_found | house | failed
    tagged_at        TIMESTAMP,
    acoustid         TEXT NOT NULL DEFAULT '',
    source_release   TEXT NOT NULL DEFAULT '',    -- release name it came from
    download_hash    TEXT NOT NULL DEFAULT '',
    missing_since    TIMESTAMP,                   -- the file vanished (MUS-15); the row is kept so a remount re-links it
    added_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_music_track_files_track ON music_track_files(release_track_id) WHERE release_track_id IS NOT NULL;
CREATE INDEX idx_music_track_files_album ON music_track_files(album_id);

-- Every other identifier, for bridging sources and following MusicBrainz merges
CREATE TABLE music_external_ids (
    entity_type TEXT NOT NULL,   -- artist | album | release | recording | release_track
    entity_id   INTEGER NOT NULL,
    source      TEXT NOT NULL,   -- musicbrainz_old | isrc | upc | deezer | discogs_master | discogs_release | acoustid | wikidata
    value       TEXT NOT NULL,
    PRIMARY KEY (entity_type, entity_id, source, value)
);
CREATE INDEX idx_music_external_ids_lookup ON music_external_ids(source, value);

-- Anything the migration could not place one-to-one is written here, never dropped
CREATE TABLE music_migration_notes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    legacy_track_id INTEGER, path TEXT NOT NULL DEFAULT '', note TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- albums = release groups (existing; ids, monitored, search backoff, cover kept). music_releases is created first.
ALTER TABLE albums ADD COLUMN artist_credit_id  INTEGER REFERENCES music_artist_credits(id);
ALTER TABLE albums ADD COLUMN primary_type      TEXT NOT NULL DEFAULT '';  -- Album, EP, Single, Broadcast, Other
ALTER TABLE albums ADD COLUMN secondary_types   TEXT NOT NULL DEFAULT '';  -- "Compilation,Live" …
ALTER TABLE albums ADD COLUMN disambiguation    TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN target_release_id INTEGER REFERENCES music_releases(id) ON DELETE SET NULL;
ALTER TABLE albums ADD COLUMN edition_pinned    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE albums ADD COLUMN source            TEXT NOT NULL DEFAULT 'musicbrainz';
ALTER TABLE albums ADD COLUMN stale_since       TIMESTAMP;
-- albums.artist_id stays: the library artist an album is filed under (Various Artists for a compilation,
-- added with in_library = 0). albums.album_type stays as the display label.
```

Notes:
- **One file per release track.** Upgrades replace (old file to the bin); two qualities of one track aren't kept side by side. Two *editions'* files are different release tracks, so both can exist, and a file of a recording that isn't on the target edition stays as an album "extra" (release_track_id NULL), listed, never deleted.
- **MBIDs live on the rows.** music_external_ids keeps everything else: merged-away MBIDs (`musicbrainz_old`, so tags written with an old id still match), ISRCs on recordings, UPCs on releases, and Deezer/Discogs/AcoustID/Wikidata ids.
- **Per-song wants** ([MUS-40](#mus-40)) add one table later, `music_song_wants(recording_id, requested_by, …)`.
- **Not-on-MusicBrainz albums** ([MUS-42](#mus-42)) keep `albums.mbid` NOT NULL UNIQUE by storing a prefixed source id (`deezer:123`) until relinked; it is never written to a MUSICBRAINZ_* tag.

**Migration from today's four tables (no user data lost).** One migration ([MUS-28](#mus-28)), run by BE's runner after its automatic snapshot:

| Today | Becomes | How |
|---|---|---|
| `artists` row | same row, same id | widened; `monitored`, `quality_profile`, `monitor_new`/`release_types` (if present), dates untouched. Each artist gets a one-name credit (`credit_key` = its MBID) |
| `albums` row | same row (release group), same id | widened; `primary_type`/`secondary_types` derived from `album_type` (Album/EP/Single → primary; Live/Compilation/Soundtrack/Remix → primary Album + that secondary); `artist_credit_id` = its artist's credit; `monitored`, `last_search_at`, `search_misses`, cover, dates untouched |
| the album's `tracks` rows | one **legacy edition** (`music_releases.legacy = 1`, no MBID), its discs from the distinct `disc_number`s, one release track per `tracks` row (`legacy_track_id` = the old id, `monitored` copied), and its recordings (one per distinct non-empty MBID; one per row where the MBID is blank) | `albums.target_release_id` points at it. Albums with no `tracks` rows (listing never fetched) get no edition; MUS-30 fills it on first refresh |
| `tracks` rows with `has_file = 1` | `music_track_files` | `path` = file_path, size, `quality` = format, bitrate, `source_release` copied; `tag_state = 'as_found'`; `probed_at` NULL so MUS-11's backfill re-measures. Two rows claiming one path → the first is mapped, the other written to `music_migration_notes` |
| `artist_events` | unchanged | same table, same ids |
| `grabs`, `blocklist`, `search_attempts`, reviews, imports | unchanged | they store album ids, which didn't change |
| `tracks` | **frozen, not dropped** | kept as the migration's source and an audit trail; an archtest forbids writes to it outside migrations once MUS-29 lands |

The migration checks itself before committing: per album, the legacy edition has exactly as many release tracks as the album had `tracks` rows, and mapped files + migration notes equal the old `has_file` count. A mismatch fails the migration (the snapshot stays the way back). After it, the legacy edition is re-keyed to a real MusicBrainz release by [MUS-30](#mus-30) without ever dropping a row that has a file. Rolling back the app means restoring the pre-migration snapshot (update.sh); files imported after the snapshot are still on disk, and a library scan finds them.

### Matcher ([MUS-31](#mus-31))

A beets-style autotagger with Arrmada's tiers. It decides, for a group of audio files, which edition they are and which file is which track.

**1. Group.** One group per download folder; sibling disc folders (`CD1`, `Disc 2`, `CD 2 - Title`) merge into one group. A folder splits into several groups only when its tags clearly name different albums (an album or album-artist tag counts as common when at most 25% of values differ and the most common covers at least 75%; Lidarr's rule). Discography downloads are one group per album folder.

**2. Read.** Per file: tags (through [MUS-27](#mus-27)'s reader): artist(s), album artist, album, title, track/disc number and totals, date, every MBID, ISRC, barcode, label, catalog number, country, media. Probe facts ([MUS-11](#mus-11)): duration, codec. The filename's number and title ([MUS-06](#mus-06)), the folder name (ParseRelease), and the grab context (the album the download was grabbed for, from its hash).

**3. Candidates**, in order, up to 10 editions:
1. The release MBID in the tags, when at least 75% of files agree, plus its equivalent editions.
2. Every acceptable edition of the album the download was grabbed for.
3. Library albums whose artist and title fold-match the tag consensus.
4. A MusicBrainz release search (cached, interactive lane): by artist + album, by barcode, by recording MBIDs.
5. Second pass, AcoustID ([MUS-32](#mus-32)), when the best is weak (below).
6. Phase 5: Deezer and Discogs candidates, with a source-mismatch penalty ([MUS-42](#mus-42)).

**4. Distance.** As beets: each component adds `penalty × weight`; the distance is the sum divided by the largest it could be, from 0.0 (perfect) to 1.0. Strings are compared after foldMusic ([MUS-04](#mus-04)) as 1 − Levenshtein similarity; a bracketed suffix ("(2011 Remaster)", "[Bonus Track]") that only one side has costs a fixed 0.1, not the whole string.

| Component | Arrmada weight | beets | Lidarr | What it measures |
|---|---|---|---|---|
| album_id | 5.0 | 5.0 | 5.0 | release MBID in the tags differs from the candidate's (0 when absent) |
| artist | 3.0 | 3.0 | 3.0 | album-artist credit vs the tag consensus; skipped for Various Artists |
| album | 3.0 | 3.0 | 3.0 | release title vs the album tag (else the folder name) |
| tracks | 2.0 | 2.0 | 2.0 | each assigned file↔track pair's track distance |
| missing_tracks | **0.9** | 0.9 | **0.6** | per candidate track with no file |
| unmatched_tracks | **0.6** | 0.6 | **0.9** | per file with no candidate track |
| data_source | 2.0 | 2.0 | 2.0 (unused) | 0.5 penalty when the candidate isn't from MusicBrainz (Phase 5) |
| media_count | 1.0 | 1.0 | 1.0 | disc-count tag vs the candidate's discs |
| media_format | 1.0 | 1.0 | 1.0 | position in the preferred-media list; 1.0 for an ignored medium |
| year | 1.0 | 1.0 | 1.0 | date tag vs the release or original year, scaled by how old the album is |
| country | 0.5 | 0.5 | 0.5 | tag vs release; else position in the preferred-country list |
| label | 0.5 | 0.5 | 0.5 | equality |
| catalog_number | 0.5 | 0.5 | 0.5 (unused) | equality |
| album_disambiguation | 0.5 | 0.5 | 0.5 | string |
| barcode | 1.0 | — | — | Arrmada: only when both sides have one |
| *track:* track_title | 3.0 | 3.0 | 3.0 | string |
| *track:* track_artist | 2.0 | 2.0 | 2.0 | credit vs the artist tag; skipped for Various Artists and blank tags |
| *track:* track_index | 1.0 | 1.0 | 1.0 | penalised only when the number matches neither the disc position nor the running number (so "1–24" fits a 2-disc edition) |
| *track:* track_length | 2.0 | 2.0 | 2.0 | none within 10 s, full penalty at 40 s off |
| *track:* track_id | 5.0 | 5.0 | **10.0** | recording MBID in the tags (or from AcoustID) differs; merged-away MBIDs resolve first |
| *track:* isrc | 1.0 | — | — | Arrmada: only when both sides have one |

Arrmada starts from beets' defaults. **Lidarr swapped `missing_tracks` and `unmatched_tracks`** (0.6 / 0.9 against beets' 0.9 / 0.6) **and doubled the recording-id weight to 10.0.** Arrmada keeps beets' values: a candidate track with no file usually means the wrong edition, which should cost more than an extra file in the download (a hidden track or an interlude); and a stale recording id should not outweigh every other field. Weights are code constants with one table test, not settings.

**5. Assign.** Files to tracks by the Hungarian (Munkres) algorithm on the track-distance matrix, padded with cost 1.0 so files and tracks may stay unassigned. A file whose track distance is above `music_match_track_max` is not placed.

**6. Decide.** Thresholds are settings (Settings → Media → Music matching, advanced), with these defaults:

| Setting | Default | Meaning |
|---|---|---|
| `music_match_strong` | 0.04 | beets' strong threshold. Auto-import when there's no grab evidence (a scan, a download nobody grabbed) |
| `music_match_expected` | 0.15 | Auto-import when the download was grabbed for this album. Lidarr's ceiling is 0.20; the grab is the extra evidence |
| `music_match_review` | 0.40 | Up to here the best candidate is offered in Reviews; above it Reviews says "couldn't identify" with the top three |
| `music_match_track_max` | 0.30 | A single file above this isn't placed; the rest of the album still is (Lidarr rejects the whole album at a 0.40 track) |
| `music_match_gap` | 0.03 | Close runner-up: a second, non-equivalent edition within this of the best sends it to Reviews |
| `music_match_partial_min` | 0.50 | A partial download auto-imports only when it covers at least this share of the edition, or exactly the tracks still wanted |

Flow:
1. Best candidate c1; runner-up c2 is the best candidate that is **not equivalent** to c1 (equivalent = same track count, titles fold-equal, lengths within 3 s each).
2. If c2 is within `music_match_gap` of c1 → **Reviews (`close_editions`)**, unless exactly one of them is the album's target edition, which wins. Equivalent editions never trigger this; the edition policy picks among them.
3. "Fit distance" = the distance without `missing_tracks`. Threshold T = `music_match_expected` when the grab points at c1's album, else `music_match_strong`.
4. Fit ≤ T and full distance ≤ `music_match_review`:
   - nothing missing → **import** every file within `music_match_track_max`;
   - tracks missing → **partial import** when matched ≥ `music_match_partial_min` of the edition, or the files are exactly tracks still wanted (song wants, or the gaps of an album already partly held); otherwise **Reviews (`partial`)**.
   Files not placed stay in the download and are named in the album's event.
5. Otherwise, c1 ≤ `music_match_review` → **Reviews (`low_confidence`)** with c1..c3; above it → **Reviews (`unmatched`)**.
6. Always **Reviews**, whatever the distance: the grab was for album A but c1 is album B (`mismatch`, existing); at least 25% of files carry recording MBIDs of another album (`tag_conflict`); AcoustID names other recordings for two or more tracks (`fingerprint_conflict`).
7. If c1 is a different acceptable edition of the target album and the edition isn't pinned → switch the target edition (event: "Switched to the 15-track 2003 edition to match the download"). If pinned, only files whose recordings are on the pinned edition are placed.

Every decision stores its breakdown (candidate, distance, each component's share, and per track "length off by 45 s", "title differs") on the review row, and Reviews shows it in words. **Singletons** (song mode): one file is matched against recordings with the track components only, same thresholds.

### House tag style ([MUS-33](#mus-33))

Picard's names and mappings, so Plex, Jellyfin, Navidrome and any later beets/Picard run agree. Multi-value means one entry per value: repeated Vorbis fields; a null-separated ID3v2.4 frame; several data atoms in one MP4 freeform atom.

| Field | FLAC / Ogg / Opus (Vorbis comment) | MP3 (ID3v2.4) | M4A (MP4) | Value |
|---|---|---|---|---|
| Title | TITLE | TIT2 | ©nam | release track title |
| Artist (display) | ARTIST | TPE1 | ©ART | the track's full credit, "A feat. B" |
| Artists | ARTISTS (multi) | TXXX:ARTISTS (multi) | ----:com.apple.iTunes:ARTISTS (multi) | each credited artist |
| Artist sort | ARTISTSORT | TSOP | soar | |
| Album | ALBUM | TALB | ©alb | release title |
| Album artist (display) | ALBUMARTIST | TPE2 | aART | release credit; "Various Artists" for compilations |
| Album artists | ALBUMARTISTS (multi) | TXXX:ALBUMARTISTS (multi) | ----:…:ALBUMARTISTS (multi) | |
| Album artist sort | ALBUMARTISTSORT | TSO2 | soaa | |
| Track number / total | TRACKNUMBER, TRACKTOTAL, TOTALTRACKS | TRCK "n/total" | trkn | per disc |
| Disc number / total | DISCNUMBER, DISCTOTAL, TOTALDISCS | TPOS "n/total" | disk | always written, also for one disc |
| Disc subtitle | DISCSUBTITLE | TSST | ----:…:DISCSUBTITLE | when the disc has a title |
| Release date | DATE | TDRC | ©day | the edition's date |
| Original date | ORIGINALDATE, ORIGINALYEAR | TDOR | ----:…:ORIGINALDATE | the release group's first date |
| Genre | GENRE (multi) | TCON (multi) | ©gen | MP4 holds one: values joined with "; " |
| Compilation | COMPILATION=1 | TCMP=1 | cpil=1 | Various Artists releases only; absent otherwise |
| Release type | RELEASETYPE (multi) | TXXX:MusicBrainz Album Type | ----:…:MusicBrainz Album Type | "album", "compilation"… |
| Release status | RELEASESTATUS | TXXX:MusicBrainz Album Status | ----:…:MusicBrainz Album Status | |
| Release country | RELEASECOUNTRY | TXXX:MusicBrainz Album Release Country | ----:…:MusicBrainz Album Release Country | |
| Media | MEDIA | TMED | ----:…:MEDIA | CD, Digital Media… |
| Label | LABEL | TPUB | ----:…:LABEL | |
| Catalog number | CATALOGNUMBER | TXXX:CATALOGNUMBER | ----:…:CATALOGNUMBER | |
| Barcode | BARCODE | TXXX:BARCODE | ----:…:BARCODE | |
| ISRC | ISRC | TSRC | ----:…:ISRC | |
| Recording MBID | MUSICBRAINZ_TRACKID | UFID:http://musicbrainz.org | ----:…:MusicBrainz Track Id | |
| Release-track MBID | MUSICBRAINZ_RELEASETRACKID | TXXX:MusicBrainz Release Track Id | ----:…:MusicBrainz Release Track Id | |
| Release MBID | MUSICBRAINZ_ALBUMID | TXXX:MusicBrainz Album Id | ----:…:MusicBrainz Album Id | |
| Release-group MBID | MUSICBRAINZ_RELEASEGROUPID | TXXX:MusicBrainz Release Group Id | ----:…:MusicBrainz Release Group Id | |
| Artist MBIDs | MUSICBRAINZ_ARTISTID (multi) | TXXX:MusicBrainz Artist Id (multi) | ----:…:MusicBrainz Artist Id (multi) | same order as ARTISTS |
| Album-artist MBIDs | MUSICBRAINZ_ALBUMARTISTID (multi) | TXXX:MusicBrainz Album Artist Id (multi) | ----:…:MusicBrainz Album Artist Id (multi) | |
| AcoustID | ACOUSTID_ID | TXXX:Acoustid Id | ----:…:Acoustid Id | when fingerprinted |
| ReplayGain (later, [MUS-50](#mus-50)) | REPLAYGAIN_TRACK_GAIN/_PEAK, REPLAYGAIN_ALBUM_GAIN/_PEAK | TXXX:REPLAYGAIN_* | ----:…:replaygain_* | |
| Front cover | METADATA_BLOCK_PICTURE / FLAC PICTURE, type 3 | APIC, type 3 | covr | the CAA front at its 500 px size, JPEG (no resizing in Arrmada) |

Plus, beside the tracks: **`cover.jpg`** (the CAA front at its 1200 px size, or the original when smaller) and `.lrc` lyrics ([MUS-45](#mus-45)).

Rules: MP3 is written as ID3v2.4 only (an ID3v2.3 tag is upgraded; ID3v1 and APEv2 tags are removed). House fields are set or replaced; other fields the file already has (comments, ratings, the user's own tags) are kept. Fields with no value are removed rather than written empty. Not-on-MusicBrainz releases write no MUSICBRAINZ_* fields. The tags as found are kept in `music_track_files.tags_json`.

### Tag library for Go ([MUS-27](#mus-27))

Arrmada is MIT-licensed, and its Docker image is built with `CGO_ENABLED=0` and cross-compiled for amd64 and arm64. Maintenance dates are the repositories' last pushes, checked 2026-10-19.

| Option | FLAC/Vorbis | ID3v2.4 | MP4 | Multi-value | Licence | arm64, no cgo | Maintained | Verdict |
|---|---|---|---|---|---|---|---|---|
| **go-taglib** (`go.senan.xyz/taglib`): TagLib 2.1.1 compiled to WebAssembly, run by wazero | read + write | read + write | read + write | yes: tags are `map[string][]string` in every format | LGPL-2.1 (TagLib LGPL-2.1/MPL-1.1; wazero Apache-2.0) | yes: pure Go, the WASM is architecture-independent | active (2026-10-12); Navidrome depends on it through a fork | **Recommended** |
| go-flac + flacvorbis + flacpicture | read + write | — | — | yes (Vorbis comments are a list) | Apache-2.0 | yes | quiet (2025-08, 2024-08, 2024-10) | FLAC only; fallback |
| n10v/id3v2 (was bogem/id3v2) | — | read + write | — | not documented; we'd null-join values ourselves | MIT | yes | last push 2023-10 | MP3 only; fallback |
| abema/go-mp4 | — | — | box-level read/write | we'd build the iTunes atoms | MIT | yes | active (2026-10) | a box library, not a tagger |
| dhowden/tag | read | read | read | limited | BSD-2 | yes | 2024-07 | read-only |
| cgo TagLib bindings | read + write | read + write | read + write | yes | LGPL | **no**: needs cgo | — | ruled out by the build |
| Bundled `metaflac` + `mid3v2` + `AtomicParsley` | yes (repeat `--set-tag`) | mid3v2 (mutagen): multi-value awkward from the CLI | AtomicParsley: one value per freeform atom | partial | GPL tools as separate processes | apt packages for both | metaflac and mutagen active; AtomicParsley sporadic | three CLIs, three escaping rules, inconsistent multi-value |
| ffmpeg `-c copy -metadata` (already bundled) | rewrites the file | `-id3v2_version 4` | no proper freeform atoms | one value per key | GPL build, separate process | yes | active | remuxes every file and can't express the house style |
| A small Python script on mutagen (Python 3 is already in the image for Apprise) | yes | yes | yes | yes (Picard uses mutagen) | GPL-2.0, separate process | yes | active (2026-08) | correct, but a Python step per import, a pinned pip install, and tests that need Python |

**Recommendation: go-taglib**, behind a small `internal/audiotag` interface so it can be swapped.
- It is the only option that reads and writes all three formats, with multi-value, as one pure-Go dependency that fits the `CGO_ENABLED=0` amd64/arm64 build.
- TagLib's property names are Picard's (ARTISTS → `TXXX:ARTISTS`, MUSICBRAINZ_ALBUMID → `TXXX:MusicBrainz Album Id` / `----:com.apple.iTunes:MusicBrainz Album Id`), so Arrmada doesn't hand-maintain three mappings.
- It is actively maintained and used by Navidrome, the strictest tag reader of the three players. It also reads cover art and audio properties, which the scan ([MUS-37](#mus-37)) uses.
- It writes in place, which is fine: Arrmada only ever writes staging copies.
- Licence: LGPL-2.1 inside an MIT app is allowed. It needs a notice and the LGPL source offer, and users must be able to swap the library: Arrmada's full source is public, and go-taglib supports replacing its WASM at link time (`-ldflags -X …binaryPath=`). **The owner signs this off** before MUS-27 merges.
- Fallback if the licence is refused: go-flac + flacvorbis + flacpicture for FLAC and n10v/id3v2 for MP3 (both permissive), with MP4 deferred; or the mutagen script.

[MUS-27](#mus-27) proves the claims above on generated files before anything depends on them.

### Import pipeline (after Phase 2)

```
completed download in arrmada-music (module on)
  → group files (disc folders merged)                                       MUS-31
  → read tags (audiotag) + probe (mediainfo.ProbeAudio, toolrun)           MUS-27, MUS-11
  → cue sheet + single image? → virtual tracks                              MUS-35
  → candidates (tags → grab → library → MusicBrainz cache) → distance → assign → decide
        weak → AcoustID second pass → decide again                          MUS-32
        doubtful → Reviews with the breakdown (download untouched)
  → staging job <root>/.arrmada-staging/<job>/
        seeding & retagging → copy in;  not seeding → move in;  tags off → hardlink straight to the library
        split cue images, write house tags + cover, cover.jpg, extras        MUS-33, MUS-35, MUS-36
        verify: tags read back, decoded audio unchanged
  → one move per file into the named path                                   MUS-34
        a file already there: better → old one to the recycle bin (refuse if the bin fails); not better → keep it
  → one transaction: music_track_files, grab → imported, events
  → outbox: Plex partial scan of the album folder, request-ready              MUS-41
```

### Jobs

| Job | Every | Task |
|---|---|---|
| search-missing-music | 30 m | existing (MUS-02 backoff and cap) |
| import-music | 30 s | existing; the matcher and staging from Phase 2 |
| refresh-music-artists | 24 h | [MUS-13](#mus-13), ≤ 50 artists a run, through the cache's background lane |
| refresh-music-cache | 6 h | [MUS-26](#mus-26), stale cache entries, background lane, capped |
| upgrade-music | 6 h | [MUS-16](#mus-16), ≤ 20 albums a run |
| verify-music-files | 24 h | [MUS-15](#mus-15), guarded against unmounted roots |
| scan-music-library | on demand / daily incremental | [MUS-37](#mus-37) |
| fetch-lyrics | 24 h | [MUS-45](#mus-45) |
| staging-recovery | at start-up | [MUS-33](#mus-33), finishes or rolls back an interrupted staging job |

Every job is registered with a plain label and description, gated by the module, and capped per run. User-started work goes through a.submit and answers 202 with a job id.

### API

All routes sit behind `musicRoute` (404 when the module is off). Staff routes need RoleManager; requester routes ([MUS-39](#mus-39), [MUS-40](#mus-40)) are signed-in and filtered through shapeRequests. Every change to routes regenerates the golden table (`go test ./internal/httpapi -run TestRouteScopesGolden -update`). New staff calls in the web app go in a lazy `lib/musicApi.ts`; requester music UI is lazy so the first-load budget holds.

### Testing approach

- Table tests for every pure function: fold, quality, track parsing, the distance components, the Hungarian assignment, the decision flow, tag mapping, naming templates, cue parsing.
- `musicTestCoord(t)` (MUS-02's harness) with a fake MusicBrainz provider; the cache tested against an httptest server that counts requests and returns 503 on demand.
- Real files only in env-gated tests (`ARRMADA_FFMPEG_IT=1`): ffmpeg lavfi audio generated in temp dirs, in the `arrmada:sycl` image, `--cpus=3`, container named `arrmada-tests-…`.
- The migration (MUS-28) tested on a DB built by today's migrations and filled with fixtures covering every edge case (blank MBIDs, a shared file path, albums with no listing, multi-disc).
- Race-test the packages touched; httpapi with `-timeout 40m`.

#### Milestone: M1: Contain (shipped in Phase 0)

_Music is off on fresh installs and labelled Preview. Switching it off stops its jobs, manual actions and API. When it is on, each incomplete album is searched on a 30 min → 12 h → weekly backoff, at most 25 albums per sweep, never for albums with no listing or not yet released._

<a id="mus-01"></a>
- [x] **MUS-01 · Music off by default and labelled Preview; switching it off stops its jobs, actions and API** — `P0` · `S` · Phase 0
  - **Problem:** module_music_enabled defaults to true (internal/httpapi/settings.go:51-55), so Music shows in the nav of every install. Meanwhile:
- /status reports {music, false, 'planned'} (server.go:517). The plannedModules comment also wrongly says Insights is on the roadmap.
- The Settings hint says the module is 'still on the roadmap' (Settings.tsx:166).

The toggle only filters the Sidebar (Sidebar.tsx:34) and the Downloads type pill. Everything else stays live:
- the search-missing-music and import-music jobs (cmd/arrmada/main.go:396-404)
- GrabDiscography and the library scan
- the API routes (server.go:402-414) and the SPA routes (App.tsx:94-96)

Switching Music off therefore leaves background grabbing running for every monitored artist.
  - **Approach:** 1. internal/settings/settings.go:
       - Export KeyModuleMusic = "module_music_enabled" and KeyModuleBooks = "module_books_enabled" (move them out of the httpapi consts), plus ModuleMusicDefault = false.
       - Add EnsureModuleDefault(ctx, key string, keepOn func(context.Context) (bool, error)) (changed bool, err error). When Get(key, "") == "" and keepOn returns true, it calls SetBool(key, true). It never touches an explicit value. This is generic so Books can reuse it.
    2. internal/music: add Repo.HasArtists / Service.HasArtists (SELECT EXISTS(SELECT 1 FROM artists)).
    3. cmd/arrmada/main.go, right after musicSvc is built (~line 198): call EnsureModuleDefault(ctx, settings.KeyModuleMusic, musicSvc.HasArtists). When it changes the value, log Info: "music: kept the Music module on because your library has artists. Switch it off in Settings → System → Modules."
    4. automation/coordinator.go:
       - Add field moduleGate func(context.Context, string) bool, SetModuleGate(fn), and moduleOn(ctx, m) bool (nil gate → true).
       - Add var ErrModuleOff = errors.New("the Music module is turned off").
       - Guard the top of SearchMusicMissing and ImportMusicDownloads (return), and GrabDiscography and ScanMusicLibrary (return ErrModuleOff). detectStalledMusic is NOT gated, so in-flight grabs still resolve.
       - main.go wires SetModuleGate next to SetMusic (main.go:330): "music" → settingsSvc.GetBool(ctx, settings.KeyModuleMusic, settings.ModuleMusicDefault); every other module → true.
    5. httpapi/settings.go: musicEnabled/booksEnabled read the settings constants (default ModuleMusicDefault). Delete the local key consts and fix the stale doc comment.
    6. httpapi/server.go:
       - Add func (a *api) musicRoute(h http.HandlerFunc) http.HandlerFunc. When !a.musicEnabled(ctx) it calls a.writeError(w, 404, "The Music module is turned off (Settings → System → Modules)").
       - Wrap every /api/v1/music/* handler INSIDE protected/requireRole (lines 402-414), e.g. a.requireRole(auth.RoleManager, a.musicRoute(a.handleAddArtist)), so unauthenticated calls still get 401.
       - Role changes on the GET routes are left to SEC.
    7. /status: replace the plannedModules var with a.modules(ctx) []module. Books is enabled from booksEnabled; music is {enabled: musicEnabled, status: "preview"}. Delete the stale roadmap comment.
    8. Web:
       - me.tsx: musicEnabled defaults to false (context default and useState).
       - App.tsx: render the three /music routes only when musicEnabled (pattern at :56/:71).
       - nav.ts gains an optional badge: "Preview" on the Music item, and Sidebar.tsx renders it as a small mono pill in the existing chip style (rounded-full font-mono text-[10.5px] uppercase, var(--panel-2) / var(--ink-faint)).
       - Settings.tsx:166: label "Music (preview)", hint "Artists and albums from MusicBrainz with automatic album downloads. Still being hardened. Turning it off hides Music and stops its searches and imports; finished downloads wait until it's back on. Nothing is deleted."
       - Leave the Modules subtitle generic. It must not claim that Books stops its jobs, because Books doesn't.
    9. README.md Features: 'Music (preview, off by default): artists, albums and whole-discography grabs.'
  - **Files:** `internal/settings/settings.go`, `internal/settings/settings_test.go`, `internal/music/repo.go`, `internal/music/service.go`, `internal/automation/coordinator.go`, `internal/automation/music.go`, `internal/automation/music_scan.go`, `internal/httpapi/settings.go`, `internal/httpapi/server.go`, `internal/httpapi/music_gate_test.go`, `cmd/arrmada/main.go`, `web/src/lib/me.tsx`, `web/src/App.tsx`, `web/src/lib/nav.ts`
  - **Acceptance:**
    - Fresh install:
- no Music entry in the nav
- GET /api/v1/music/artists returns 404 with the module-off message
- /music deep links render the 404 page
- no 'music:' search log lines appear
    - Upgrading a DB that has at least one artist and no saved setting keeps Music on. The settings row reads 'true', and the startup log line says why.
    - Switching Music off (once the CFG Save fix is in):
- no new arrmada-music torrents are added, and the next cycle logs no music search lines
- POST discography and POST scan return 404
- switching it back on restores everything without a restart
    - An in-flight music grab still resolves while the module is off (the detect-stalled music branch keeps running).
    - /status reports music with status 'preview' and the live enabled flag. The word 'roadmap' no longer appears in Settings or in the server.go module comment.
  - **Tests:** Go (settings): EnsureModuleDefault:
- unset + keepOn true → value 'true', changed=true
- unset + keepOn false → stays unset (GetBool returns false)
- explicit 'false' is never overwritten
- keepOn error → no write; Go (music): HasArtists is false on an empty DB and true after CreateArtist.; Go (httpapi, security_test.go setup): a music route returns 404 when off and 200 when on; an unauthenticated request still gets 401.; Go (automation): with the gate returning false:
- SearchMusicMissing and ImportMusicDownloads return before touching c.downloads or c.indexers (nil services don't panic)
- GrabDiscography and ScanMusicLibrary return ErrModuleOff
- a nil gate behaves as on; UI (manual):
- off → no Music in the Sidebar, /music renders NotFound, no Music pill in Downloads
- on → the Preview pill shows
  - **Depends on:** CFG-xx (fix the broken Save settings round-trip: GET returns server_time/server_tz, which PUT rejects). Without it the toggle can't be switched off from the UI; default-off and the keep-on rule work regardless., SEC-xx (deny-by-default route roles) for the GET music routes; soft
  - **Risk:** The startup keep-on check is the only thing that stops an active music user from having the module silently switched off, so it must log clearly and must never overwrite an explicit value. While the module is off, finished music torrents sit unimported and import once it is switched back on (the hint says so). Books keeps its current toggle behaviour. The gate is built so Books can reuse it later.
  - **Resolves:** music-12
<a id="mus-02"></a>
- [x] **MUS-02 · Per-album search backoff, 25-album cap per sweep, and no searches for albums with no listing or not yet released** — `P0` · `M` · Phase 0
  - **Problem:** The SearchMusicMissing doc comment promises exponential backoff, but the loop (internal/automation/music.go:45-62) only skips albums that are unmonitored, complete or downloading, then calls grabAlbum. grabAlbum runs a full multi-indexer search every time and never records a miss. The albums table (0070_music.sql) has no search-state columns. The job runs every 30 minutes (main.go:396).

Costs:
- An artist with 20 unfindable albums costs about 960 indexer searches a day. That risks tracker API caps and bans that would also hit Movies and Series.
- Albums with no MusicBrainz listing (EnsureTracks returns nil, service.go:133-134) also cost a MusicBrainz call every sweep.
- Pre-announced albums are searched before they exist.

There is also no test harness for the music sweep.
  - **Approach:** 1. Migration (next free number, tentatively 00NN_album_search_backoff.sql, mirroring 0074_book_search_backoff):
       ALTER TABLE albums ADD COLUMN last_search_at TIMESTAMP;
       ALTER TABLE albums ADD COLUMN search_misses INTEGER NOT NULL DEFAULT 0;
    2. internal/music:
       - Album gains LastSearchAt string `json:"last_search_at,omitempty"` and SearchMisses int `json:"search_misses"` (albumCols/scanAlbum).
       - Repo/Service gain RecordSearchMiss(ctx, id) (misses+1, last_search_at = CURRENT_TIMESTAMP) and ResetSearchMisses(ctx, id) (0, last_search_at = now), shaped like books/service.go:331-341.
       - New Repo.WantedAlbums(ctx) returns monitored albums of monitored artists with track counts and have counts in ONE grouped query (albums JOIN artists LEFT JOIN tracks GROUP BY al.id). This replaces the per-album fillAlbumCounts loop in the sweep, and [MUS-18](#mus-18) reuses it.
    3. automation/music.go: musicSearchWait(misses int) time.Duration: 0 for ≤0, searchBackoff(misses) (series.go:333) for 1-6, 7*24h from 7.
    4. Introduce type albumOutcome struct{ Code, Detail, Release string } with code constants and a miss() method:
       - miss: no_listing, no_results, no_match, blocked, below_profile
       - not a miss: indexer_error, listing_error, grab_failed, no_space
       - grabbed resets the misses
       grabAlbum returns it instead of returning silently. Detail strings come from what it already knows (candidate counts, best tier vs profile). This follows ACQ's rule: no miss when every indexer errored.
    5. SearchMusicMissing:
       - Read the queue once (existing).
       - Build the due list from WantedAlbums: !Complete; release_date empty or ≤ today (YYYY-MM-DD string compare; a year-only date in the future also counts as future); not albumDownloading; now ≥ last_search_at + musicSearchWait(misses).
       - Sort never-searched first, then oldest last_search_at.
       - Process at most musicSweepCap = 25.
       - Miss → RecordSearchMiss. Grabbed → ResetSearchMisses. no_space → stop the sweep for this cycle, since every other album would hit the same wall.
       - Log once at the 7-miss transition: "music: nothing found 7 times, searching weekly from now on".
       - Future-dated albums are skipped and NOT counted as misses.
    6. grabAlbum: after EnsureTracks, re-read the track count via c.music.Tracks. If it is still 0, return no_listing without an indexer search.
    7. Test seams on Coordinator (nil means the real service), used by SearchMusicMissing, grabAlbum and GrabDiscography:
       - musicSearchFn func(context.Context, indexer.SearchQuery) (indexer.SearchResult, error)
       - musicQueueFn func(context.Context) ([]download.Item, error)
       - musicGrabFn func(ctx, indexerName, url, title, category string) (string, error)
    8. New internal/automation/music_harness_test.go: musicTestCoord(t), modelled on packTestCoord (books_packs_test.go:17):
       - store.Open(t.TempDir())
       - music.NewService(db, &fakeMusicProvider{}, slog.Default())
       - quality.NewService(db)
       - library.NewImporter(t.TempDir(), …)
       - an eventbus
       fakeMusicProvider implements metadata.MusicProvider from canned artists, albums and tracks.
    9. Rewrite the doc comment at music.go:23-29 to describe the real behaviour.
  - **Files:** `internal/store/migrations/00NN_album_search_backoff.sql`, `internal/music/music.go`, `internal/music/repo.go`, `internal/music/service.go`, `internal/automation/music.go`, `internal/automation/coordinator.go`, `internal/automation/music_harness_test.go`, `internal/automation/music_sweep_test.go`
  - **Acceptance:**
    - Two sweeps back to back: the second makes no indexer search for an album that just missed.
    - An album with no MusicBrainz track listing never triggers an indexer search.
    - A future-dated album is not searched and its search_misses stays 0.
    - A successful grab sets search_misses back to 0 in the DB.
    - For an artist with 20 unavailable albums, a 24 h log shows at most about 7 searches per album and never more than 25 album searches in one sweep, then weekly retries.
  - **Tests:** Go: TestMusicSearchWait table: 0→0, -1→0, 1→30m, 2→1h, 6→12h, 7→168h, 50→168h.; Go (music repo): RecordSearchMiss increments and stamps last_search_at; ResetSearchMisses zeroes; WantedAlbums returns the right counts and excludes unmonitored artists and albums.; Go (sweep, musicTestCoord, with musicSearchFn counting calls):
- an immediate second sweep makes 0 searches
- a no-listing album makes 0 searches and records a miss
- 40 due albums → exactly 25 searched, oldest first
- a future-dated album → 0 searches and 0 misses
- musicSearchFn returning an error → no miss recorded
- no_space stops the sweep; Go: grabAlbum outcome codes for no_results, no_match (non-matching releases), below_profile (MP3 against a lossless-only profile) and grabbed (musicGrabFn returns a hash).
  - **Risk:** An album that has missed a lot is retried automatically only weekly. MUS-09 (Search now) and MUS-13 (immediate search of new releases) cover that. Existing rows start at 0 misses, so the first sweeps after deploy still search everything once, 25 per sweep. Land this after MUS-01 because both edit SearchMusicMissing.
  - **Resolves:** music-1

#### Milestone: M2: Decide (decided 2026-10-19: Rebuild)

_A dated Rebuild, Park or Cut decision is recorded, and every later MUS task is marked go, superseded or won't-do._

<a id="mus-03"></a>
- [x] **MUS-03 · Decide: rebuild Music, park it, or cut it** — `P1` · `S` · Phase 1
  - **Decision (2026-10-19): Rebuild**, to be better than Lidarr rather than a Lidarr-lite. Recorded with its reasons in [docs/decisions/music.md](../decisions/music.md): MusicBrainz direct through one limiter with a permanent cache and an optional self-hosted mirror, never a central proxy; Deezer gap-fill on by default, Discogs opt-in; Cover Art Archive then Fanart.tv for art; AcoustID as a second pass; house-style tags, copying rather than hardlinking a file that is still seeding; no Soulseek, YouTube or streaming-rip sources; Plex first, with tags that also suit Jellyfin and Navidrome.
  - **Tasks marked to match:** MUS-01 and MUS-02 stay done. MUS-04..09, 11..17 and 21..24 go (MUS-12 now waits for R2), several re-scoped by a rebuild note. MUS-10, 18, 19 and 20 are superseded (see the last milestone). MUS-25 (Cut) is won't-do. New work is MUS-26..50.
  - **Problem:** The audit's verdict was to hide Music and then either rebuild it properly or cut it, because shipping it as it stood was the worst option. The decision had to be explicit, or the epic would drift into sunk-cost work.
  - **Approach (as planned):** gather Plex music plays, library counts and grab history read-only; apply the rule of thumb (Rebuild with regular listening or if Lidarr should go; Cut with about zero plays; Park in between); record the decision in docs/decisions/music.md; mark every later task. The owner decided on the research (a report and notes on Lidarr's design, its complaints, the metadata providers and today's module) rather than waiting for the soak numbers; recording those numbers as a baseline is an open question at the end of this file.
  - **Files:** `docs/decisions/music.md`, `docs/roadmap/MUS.md`, `ROADMAP.md`
  - **Acceptance:**
    - A dated decision exists with its reasons and the owner's choices on metadata, fallbacks, tags, sources and players. ✓
    - Every MUS task after M1 is marked go, superseded or won't-do. ✓
  - **Tests:** None (decision task).
  - **Depends on:** [MUS-01](#mus-01), [MUS-02](#mus-02)
  - **Risk:** The rebuild is far larger than the Lidarr-lite. The phases are ordered so each one ships value on its own (R1 alone fixes the wrong-file bugs).
  - **Resolves:**

#### Milestone: M2-alt: Cut path (won't do: MUS-03 decided Rebuild)

_Kept for the record. Nothing here will be built._

<a id="mus-25"></a>
- [ ] ~~**MUS-25 · If the decision is Cut: retire the Music module cleanly, keeping every byte of data**~~ — **Won't do: [MUS-03](#mus-03) decided Rebuild (2026-10-19).** Original: `P2` · `S` · Phase 2
  - **Problem:** This task runs only if MUS-03 decides Cut. A half-removed module would leave dead routes, jobs and copy behind. Worse, leftover 'grabbed' music rows would fall through to DetectStalled's default movie branch, which would treat album ids as movie ids: blocklisting under a movie and removing torrents.
  - **Approach:** 1. Remove:
       - the Music nav entry (nav.ts), the SPA routes (App.tsx) and pages (Music.tsx, ArtistDetail.tsx, AlbumDetail.tsx)
       - the Downloads 'Music' pill and the Quality page's music tab and presets UI
       - the API route block (server.go:401-414) and handlers (httpapi/music.go), and the job registrations (main.go:395-404)
       - the Settings toggle and the music entry in /status
       - the README line (replace it with a line in docs/decisions/music.md)
    2. DetectStalled: case "music", "music_artist" → leave the row alone (log once). The movie branch must never see them. ManageSeeding may keep applying recorded seed rules to imported music grabs; the data is hardlinked, so that is safe.
    3. Delete internal/music, automation/music*.go and metadata/musicbrainz.go only after `grep -rn` shows nothing else uses them.
       - The Reviews 'music' branch (reviews.go:277, 360) keeps Reject and Dismiss working for old held items; Import returns a clear 'Music has been removed' error.
       - Keep quality.MediaMusic constants if other code references them.
    4. Keep the DB tables (artists, albums, tracks, artist_events) and every file on disk: no DROP TABLE, no file deletion, no new migration. Old migrations stay, so the module can be restored from git history.
    5. Leave torrents in the arrmada-music category alone.
  - **Files:** `web/src/App.tsx`, `web/src/lib/nav.ts`, `web/src/pages/Music.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/pages/AlbumDetail.tsx`, `web/src/pages/Downloads.tsx`, `web/src/pages/Quality.tsx`, `web/src/pages/Settings.tsx`, `internal/httpapi/server.go`, `internal/httpapi/music.go`, `internal/automation/coordinator.go`, `internal/automation/reviews.go`, `cmd/arrmada/main.go`, `internal/automation/music.go`
  - **Acceptance:**
    - No Music appears anywhere in the UI or API, and no music jobs are registered.
    - The music tables and files are untouched (row counts are the same before and after the deploy).
    - A leftover 'grabbed' music row is not blocklisted or removed by DetectStalled.
    - go vet and go test -race pass (race tests run in Docker before pushing).
  - **Tests:** Go: full build plus go vet and go test -race (Docker one-liner).; Go: DetectStalled with a pending 'music' row and an empty queue leaves the row and the blocklist untouched.; UI (manual): no Music in the nav, Settings, Quality or Downloads; an old music review can still be rejected or dismissed.
  - **Depends on:** [MUS-03](#mus-03) (decided Cut)
  - **Risk:** Some code may share the music helpers (quality ladder, reviews), so grep before deleting. The data stays in the DB, so the module can come back.
  - **Resolves:** 

#### Milestone: R1: Safety — no wrong files, no wasted calls

_Accented, punctuated and scene-style names match. Quality tiers are read from whole tokens. Songs with numeric titles land on the right track. Discography and partial-album grabs stall, blocklist and seed correctly, and a held track is never downgraded or duplicated. Every track records its real codec and bitrate. MusicBrainz is called through one limiter with an honest User-Agent, every answer is cached for good, and a self-hosted mirror can be set. The tag library is chosen and proven on all three tag formats. Estimate: 3–3.5 weeks._

<a id="mus-04"></a>
- [ ] **MUS-04 · One music text-folding function for release matching and scan keys (accents, apostrophes, &, +, slashes, acronyms)** — `P1` · `S` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Build foldMusic on parser.FoldTitle and parser.TitleWords: ACQ-21 shipped them as the one normaliser, and a second one is not allowed. foldMusic adds only the music rules they don't cover: joining letter/letter slashes and dotted single-letter acronyms, '+' as 'and', and the leading-'the' rule for artists. Like TitleKey it drops the standalone word 'and', so '&', 'and' and a missing conjunction agree. BOOK-06 already shipped its fold on the same base, so step 4's coordination is done. Phase 2's matcher ([MUS-31](#mus-31)) uses foldMusic for every string it compares.
  - **Problem:** normWords (internal/music/match.go:150-166) and NormKey (release.go:231-240) keep only ASCII a-z/0-9, and music never calls parser.FoldAccents. A release that keeps the original spelling does match. Matching fails whenever a release spells the name differently from MusicBrainz, which is common:
- Bjork vs Björk, Motorhead vs Motörhead
- ACDC vs AC/DC
- 'Sgt Peppers' vs "Sgt. Pepper's"
- 'and' vs '&'
- scene underscore names ('The_Beatles-Sgt_Peppers_…-FLAC')

indexer/service.go:363 folds accents out of the outgoing query, so results lean ASCII and then fail ReleaseIsForAlbum. The same gate controls in-flight detection (music.go:71), import routing (music.go:304), discography matching and the library scan (music_scan.go:104-123, 179-190). Failures leave only an Info log line.
  - **Approach:** 1. New internal/music/fold.go, foldMusic(s string) string:
       - parser.FoldAccents, then lowercase
       - delete apostrophes (' ’ ‘ ` ´) without leaving a gap
       - standalone '&' and '+' → 'and'
       - join letter/letter slashes and single-letter dotted acronyms ('ac/dc' → 'acdc', 'r.e.m.' → 'rem')
       - treat '_' and '.' as separators in scene names (already true once they are non-alphanumeric)
       - keep unicode.IsLetter/IsDigit runes, so CJK and Cyrillic names don't fold to ''
       - every other run of characters becomes one space
    2. normWords becomes foldMusic. NormKey becomes foldMusic with the spaces removed.
    3. ReleaseIsForAlbum and ReleaseIsDiscographyFor:
       - The artist check also accepts the artist with a leading 'the ' dropped when other words remain ('The Beatles' ↔ 'Beatles'). This applies to the artist only, never the album.
       - An artist that folds to '' returns false. Today `a != "" &&` silently skips the artist check.
    4. Coordinate with BOOK's quick win #13 (fold accents, apostrophes and & in book matching). If BOOK lands a shared parser-level helper first (e.g. parser.FoldWords), build foldMusic on it. Otherwise keep foldMusic self-contained so BOOK can lift it.
    5. Callers need no changes. albumDownloading, grabAlbum, albumForRelease, artistForRelease, scanResolveArtist, matchAlbumByTitle and MatchTracks' title pass all go through these functions.
  - **Files:** `internal/music/fold.go`, `internal/music/fold_test.go`, `internal/music/match.go`, `internal/music/release.go`, `internal/music/match_test.go`, `internal/music/release_test.go`
  - **Acceptance:**
    - When indexers return ASCII-spelled releases ('Bjork - Homogenic (1997) [FLAC]'), a monitored Björk album is grabbed.
    - Scan library matches a library folder 'Bjork/Homogenic' to MusicBrainz 'Björk'.
    - 'Kid A' still does not match a 'Kid Amnesiae' release.
  - **Data safety:** Pure functions; no file or DB writes.
  - **Migrations:** None.
  - **Tests:** Go table, ReleaseIsForAlbum true:
- Björk vs 'Bjork - Homogenic (1997) [FLAC]'
- Motörhead vs 'Motorhead - Ace of Spades [FLAC]'
- Sigur Rós vs 'Sigur Ros - Takk [MP3 320]'
- AC/DC vs 'ACDC - Back in Black'
- "Sgt. Pepper's Lonely Hearts Club Band" vs 'The_Beatles-Sgt_Peppers_Lonely_Hearts_Club_Band-REMASTERED-FLAC'
- "(What's the Story) Morning Glory?" vs 'Oasis - Whats the Story Morning Glory'
- 'Simon & Garfunkel' vs 'Simon and Garfunkel - Bookends'
- 'Florence + the Machine' vs 'Florence and the Machine - Lungs'
- 'The Beatles' vs 'Beatles - Abbey Road'
- R.E.M. vs 'REM - Automatic for the People'; Go table, ReleaseIsForAlbum false:
- 'Kid A' vs 'Radiohead - Kid Amnesiae'
- discography names
- an artist that folds to empty
- 'The The' (dropping 'the' must not leave an empty artist); Go: NormKey('Björk') == NormKey('Bjork'); NormKey of a CJK title is non-empty; matchAlbumByTitle matches '&' against 'and'.; Go: all existing match_test.go and release_test.go cases still pass.
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild), BOOK-xx (quick win #13 fold helper); soft, coordinate rather than block
  - **Risk:** Looser matching could accept a different album with a near-identical title. Whole-word matching and the artist check still apply, and the 'the' drop applies only to the artist. The acronym join must only fire on single letters separated by dots, so 'Mr. Bungle' is unaffected.
  - **Resolves:** music-2
<a id="mus-05"></a>
- [ ] **MUS-05 · Token-based DetectQuality so 'Escape', 'Palace', 'New Wave', 'Mixtape', 'Isaac' and 'Doggystyle' can't set the tier** — `P1` · `S` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Unchanged. From [MUS-11](#mus-11) on, the tier of a file on disk is measured; DetectQuality remains the tier of a release *name*, used only for grabbing and ranking.
  - **Problem:** DetectQuality (internal/music/release.go:94-108) does plain substring matching on the lowercased name. Current failures:
- 'Journey - Escape [MP3 128]' → FLAC
- 'Palace … [MP3 320]' → ALAC
- 'New Wave [MP3 V0]' → WAV
- 'Roberta Flack' and every 'Escape the Fate' release → FLAC
- 'Mixtape ' matches 'ape '
The lossy fallbacks have the same flaw: 'Isaac' contains 'aac' and 'Doggystyle' contains 'ogg'.

albumScore (automation/music.go:156-175) scores on this tier alone. So 'Lossless only' profiles grab MP3s, and those MP3s outrank real FLAC on seeders.
  - **Approach:** 1. Rewrite DetectQuality to tokenize the lowercased name with strings.FieldsFunc on non-letter/non-digit runes.
    2. Lossless tokens:
       - 'flac' or ^flac\d+$ → FLAC, or FLAC-24 when reHiRes matches (regex unchanged)
       - 'alac' → ALAC
       - 'ape', 'wv', 'wavpack', 'tak' → FLAC tier
       - 'wav' → WAV
    3. Lossy codec fallbacks also go by token: 'aac'/'m4a' → AAC, 'opus', 'ogg'/'vorbis', 'mp3'.
    4. The VBR and bitrate regexes stay as they are (already \b-bounded). '24bit' stays a hi-res modifier only.
    5. Delete containsAny.
    6. ParseRelease keeps calling DetectQuality, so discography and import paths pick up the fix automatically.
    The other half of music-3, the release tier being stored as each track's format, is [MUS-11](#mus-11).
  - **Files:** `internal/music/release.go`, `internal/music/release_test.go`
  - **Acceptance:**
    - A 'Lossless only' profile never grabs a release whose only lossless-looking text sits inside another word.
    - All existing release_test.go cases still pass.
  - **Data safety:** Pure functions; no file or DB writes.
  - **Migrations:** None.
  - **Tests:** Go table, false positives that must now read correctly:
- 'Journey - Escape (1981) [MP3 128]' → MP3-128
- 'Palace - Life After (2019) [MP3 320]' → MP3-320
- 'Against Me! - New Wave (2007) [MP3 V0]' → MP3-V0
- 'Roberta Flack - First Take [MP3 320]' → MP3-320
- 'Escape the Fate - Ungrateful [MP3 320]' → MP3-320
- 'Ed Sheeran - Shape of You [MP3 320]' → MP3-320
- 'Chance the Rapper - Coloring Book (Mixtape) [MP3 320]' → MP3-320
- 'Isaac Hayes - Hot Buttered Soul [MP3 256]' → MP3-256
- 'Snoop Dogg - Doggystyle [MP3]' → MP3-192; Go table, positives that must still be detected:
- '[FLAC]' → FLAC
- 'FLAC 24bit 96kHz' → FLAC-24
- 'WEB-FLAC' → FLAC
- '(ALAC)' → ALAC
- 'WavPack' → FLAC
- '[WAV]' → WAV
- 'Album.FLAC24' → FLAC-24
- '[APE]' → FLAC
- 'AAC 256' → AAC-256
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild)
  - **Risk:** Releases that say only 'Lossless' with no codec now read as Unknown and are refused, which is the conservative choice. A title word that is exactly 'ape', 'tak' or 'wav' as a whole token (e.g. an album called 'Ape') would still read as lossless. That is rare, and interactive search (MUS-17) shows the tier so it can be spotted.
  - **Resolves:** music-3
<a id="mus-06"></a>
- [ ] **MUS-06 · Safer track-number parsing so '99 Problems', '7 Rings' and '4 Minutes' land on the right track** — `P1` · `S` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Unchanged in scope. From Phase 2 the parsed number is one signal (track_index) of the matcher ([MUS-31](#mus-31)) instead of the decision, and 'left unmatched beats misfiled' stays the rule. The AudioFile.Disc hint feeds the matcher's disc-folder grouping.
  - **Problem:** reLeadingNum (internal/music/match.go:18) reads any 'NN - ' as a disc number whenever another number follows:
- '04 - 99 Problems' parses as disc 4, track 99.
- '10 - 7 Rings' parses as disc 10, track 7.
- '05. 4 Minutes' parses as disc 5, track 4.
Pass 2 fails too, because titleOf strips the same prefix and leaves only 'Problems'.

When the prefix parses as disc 1, the file is silently misfiled: '01 - 7 Rings' lands on track 7, the real track 7 file is left unmatched, and "01 - 22 (Taylor's Version)" reads as track 22. That is exactly the failure match.go:50-58 says it prevents. The tests have no numeric-title cases.
  - **Approach:** Changes in internal/music/match.go:
    1. Split the regex in two:
       - reDiscTrack `^\s*(\d{1,2})[-.](\d{2,3})(?:[\s._)-]|$)`: compact and unspaced, like '1-04' or '2.07'.
       - reTrack `^\s*(\d{1,3})\s*[-._)\s]`.
    2. MatchTracks decides per folder, not per file. A disc-track reading is accepted only when:
       - the disc exists in the listing (≤ max DiscNumber), and
       - at least 2 files in the folder have the compact shape.
       Otherwise every file is parsed track-only. A 3-digit '101 - Title' on a multi-disc listing reads as disc 1, track 1.
    3. titleOf strips only the number token that was actually used.
    4. Agreement check: if a file's stripped title (NormKey) exactly matches a DIFFERENT track than its number points to, prefer the title. If the title matches nothing, keep the number. This prevents 'one off' misfiles.
    5. Pass 2 tries the full base name (minus extension) against track titles before trying the stripped name.
    6. Add AudioFile.Disc (a hint, 0 = unknown) for [MUS-15](#mus-15). When it is set and the filename carries only a track number, use it as the disc.
  - **Files:** `internal/music/match.go`, `internal/music/match_test.go`
  - **Acceptance:**
    - Albums whose song titles start with a number import with every track in the right slot.
    - No file lands on a wrong track in any test case; when in doubt, files are left unmatched.
    - Existing multi-disc '1-04' naming still works.
  - **Data safety:** Pure functions; no file or DB writes.
  - **Migrations:** None.
  - **Tests:** Go TrackNumberOf/MatchTracks cases:
- '04 - 99 Problems.flac' → track 4
- '10 - 7 Rings.flac' → track 10
- '05. 4 Minutes.flac' → track 5
- "01 - 22 (Taylor's Version).flac" → track 1; Go misfile regression: an album with tracks [1 '7 Rings', 7 'Fake Smile'] and files '01 - 7 Rings.flac' and '07 - Fake Smile.flac' → both placed correctly.; Go multi-disc:
- '1-04 Airbag.flac' + '2-01 X.flac' → discs 1 and 2
- '04-99 Problems.flac' alone on a 1-disc listing → track 4
- a folder with a single compact-shaped file falls back to track-only
- an AudioFile.Disc=2 hint with '03 - X.flac' → disc 2, track 3; Go: all existing match_test.go cases still pass.
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild), [MUS-04](#mus-04) (titleOf/NormKey change underneath); soft
  - **Risk:** Odd naming schemes that mix shapes in one folder fall back to track-only, which may leave some files unmatched. That is deliberate: unmatched is recoverable, a misfile isn't.
  - **Resolves:** music-4
<a id="mus-07"></a>
- [ ] **MUS-07 · Record discography grabs as media_type 'music_artist' so stall, blocklist and seeding act on the artist** — `P1` · `S` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Unchanged. Grabs keep pointing at album (release-group) ids after [MUS-28](#mus-28), so nothing here moves in Phase 2. setMusicGrabStatus must use the grab-status words in internal/automation/grabstatus.go.
  - **Problem:** GrabDiscography records its grab with recordMusicGrab(ctx, artistID, …) and media_type 'music' (internal/automation/music.go:553), and blocklists with dropBlockedMusic(ctx, artistID) (:539). Every 2 minutes, detectStalledMusic reads g.MovieID as an album id (:477). The artist's discography is therefore judged by whichever album happens to share its numeric id:
- No such album: the grab is marked failed within 2 minutes and is never seed-managed.
- That album is complete: the grab is marked 'imported', so ManageSeeding may remove a torrent that is still importing (with seeding off for that indexer).
- Stall fail-over blocklists the release under an unrelated album.
  - **Approach:** 1. recordMusicGrab gains a mediaType parameter. grabAlbum passes 'music'; GrabDiscography passes 'music_artist' with the artist id.
    2. Blocklist helpers become media-type aware:
       - blockedSetMusicArtist(ctx, artistID) → blockedSetOf(ctx, artistID, "music_artist")
       - addBlockMusicArtist(ctx, artistID, title, indexer, reason)
       GrabDiscography uses both.
    3. Add one shared helper, setMusicGrabStatus(ctx, mediaType, id, infoHash, releaseName, status). It mirrors setSeriesGrabStatus (store.go:443): hash first, then a normRelease title fallback, and it closes rows before writing. [MUS-08](#mus-08) reuses it.
    4. DetectStalled (coordinator.go:1216-1228) gets case 'music_artist' → detectStalledDiscography:
       - hash in imports (hashAlreadyImported) → 'imported'
       - otherwise the usual stall window → addBlockMusicArtist → remove the torrent → 'failed', plus the artist event 'Discography stalled after N min, blocklisted'
       - The default movie branch must never see these rows.
    5. ImportMusicDownloads discography branch (music.go:210-231): on placed > 0, also setMusicGrabStatus(ctx, 'music_artist', art.ID, it.Hash, it.Name, 'imported').
    6. Migration (next free number, tentatively 00NN_music_discography_grabs.sql). Rows in grabs and blocklist with media_type='music' and a lower(title) LIKE any of '%discography%', '%complete collection%', '%all albums%', '%box set%', '%boxset%' or '%anthology%' become 'music_artist'. The patterns mirror reDiscographyTG (release.go).
    7. No label change is needed here. Downloads and Activity label by download category; arrmada-music labelling is ACQ's ops-10 fix.
  - **Files:** `internal/automation/music.go`, `internal/automation/coordinator.go`, `internal/automation/store.go`, `internal/store/migrations/00NN_music_discography_grabs.sql`, `internal/automation/music_discography_test.go`
  - **Acceptance:**
    - After a discography grab, the grabs row has media_type 'music_artist' and the artist id.
    - The grab is never marked failed or imported because of an unrelated album.
    - Once the discography imports something, the grab is 'imported' and its seed rule applies.
    - A stalled discography is blocklisted under the artist and writes an artist event.
  - **Data safety:** DB rows only (grabs, blocklist). The reclassifying migration touches only rows whose title matches the discography patterns, and runs after BE's automatic snapshot.
  - **Migrations:** One, next free: `music_discography_grabs` (data only, as specified).
  - **Tests:** Go (musicTestCoord + seams): GrabDiscography records media_type music_artist with the artist id.; Go: DetectStalled with a music_artist grab whose artist id equals an unrelated complete album's id leaves the status 'grabbed'. With its hash in imports, the status becomes 'imported'.; Go: a stalled music_artist grab (musicQueueFn with no item) is blocklisted under media_type music_artist, and blockedSetMusicArtist returns it.; Go: the migration reclassifies 'Artist - Discography (1990-2010) [FLAC]' and leaves 'Artist - Album [FLAC]' alone.
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild), [MUS-02](#mus-02) (search and grab seams for the tests)
  - **Risk:** The LIKE patterns could reclassify an old single-album row titled '…Anthology…'. ParseRelease already treats such names as discographies, so this stays consistent with import routing.
  - **Resolves:** music-6
<a id="mus-08"></a>
- [ ] **MUS-08 · Partial albums: mark the grab imported, never overwrite a track with a worse copy, recycle replaced siblings** — `P1` · `M` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Two changes. (1) Replaced files go through library.RemoveToBin / CheckBin (per-root bins shipped in Phase 3), and the import refuses when the bin fails: no RecycleReplaced helper, never an os.Remove fallback. (2) HeldTier / BetterTier read today's tracks.format; after [MUS-28](#mus-28) the same helpers read music_track_files.quality, which the migration copies across.
  - **Problem:** A music grab only becomes 'imported' when the album is Complete (internal/automation/music.go:482-485). ManageSeeding reads only 'imported' grabs (store.go:327-330), so any partial import (say 11/12 tracks) seeds forever and its seed rules never run.

importAlbumContent re-places every matched track with no HasFile check (music.go:350-369; the scan has one at music_scan.go:160). pickBestAlbum never compares against the tier already held. BetterFormat/FormatRank are never called. On a re-grab:
- A same-extension lower tier replaces the existing file. linkOrCopy recycles the old one (importer.go:1433-1451), so it is a silent downgrade.
- A different extension writes a second sibling file (.mp3 next to .flac) and orphans the old one.

When a seeding torrent is removed, detectStalledMusic blocklists the good release with a false 'stalled' reason, because findQueued no longer finds it.
  - **Approach:** 1. ImportMusicDownloads (music.go:266-269): when placed > 0, call setMusicGrabStatus(ctx, 'music', album.ID, it.Hash, it.Name, 'imported') (the [MUS-07](#mus-07) helper). ManageSeeding then applies the indexer's seed rule.
    2. If the album is still incomplete after import:
       - call c.music.RecordSearchMiss ([MUS-02](#mus-02))
       - write the artist event 'Imported 11/12 of "X"; still missing: "Bonus Track"', listing up to 5 titles
    3. Migration (next free number, tentatively 00NN_music_partial_grabs.sql) heals existing rows: UPDATE grabs SET status='imported' WHERE media_type='music' AND status='grabbed' AND info_hash != '' AND lower(info_hash) IN (SELECT lower(download_hash) FROM imports).
    4. detectStalledMusic: a grab whose hash is already in imports is marked 'imported' and never blocklisted as stalled. The Complete() shortcut stays.
    5. Tier helpers in internal/music/format.go replace the unused FormatRank/BetterFormat (remove them and move format_test.go cases over):
       - HeldTier(format string, bitrate int) Quality accepts both tier names ('MP3-320', written by imports) and extension tags ('MP3', 'FLAC', written by the scan; a bitrate maps MP3 to a tier).
       - BetterTier(cand, held Quality) bool: strictly higher QualityRank, except that lossless vs lossless never churns (FLAC over ALAC is not an upgrade).
       - Repo.HeldTiers(ctx, albumID) returns the lowest held tier.
    6. importAlbumContent and importAlbumFolder (music.go:350-369, 649-667): for a track that already HasFile, place the new file only when BetterTier(newTier, HeldTier(t.Format, t.BitrateKbps)). Otherwise skip it and count it as 'kept'.
    7. If the better file lands at a different path (extension change), recycle the old file with a new library.Importer.RecycleReplaced(path) (string, error): RecycleFile(im.recycleDir, path). With no recycle dir it keeps the old file and logs. Never hard-delete.
    8. grabAlbum: when HaveTracks > 0, drop candidates whose DetectQuality rank is below the album's lowest held tier. Report them as outcome below_profile with the detail 'releases below the MP3-320 you already have'.
  - **Files:** `internal/automation/music.go`, `internal/automation/store.go`, `internal/music/format.go`, `internal/music/format_test.go`, `internal/music/repo.go`, `internal/library/importer.go`, `internal/store/migrations/00NN_music_partial_grabs.sql`, `internal/automation/music_import_test.go`
  - **Acceptance:**
    - Partial-album torrents leave the client according to their seed rule (visible on the Seeding tab).
    - After a re-grab, an album folder never holds both '05 - X.flac' and '05 - X.mp3'.
    - A held FLAC track is never replaced by a lower tier.
    - Removing a seeding music torrent no longer blocklists its release as 'stalled'.
    - A partial import writes an artist event naming the missing tracks and backs the album off.
  - **Data safety:** A held file is never overwritten by a worse copy. A replaced file goes to the recycle bin on its own drive; if the bin fails, the import refuses, leaving the old file and the download untouched.
  - **Migrations:** One, next free: `music_partial_grabs` (data only, as specified).
  - **Tests:** Go (musicTestCoord with temp-dir byte-blob tracks): a partial import sets the grab status to 'imported' and records a search miss.; Go: re-importing an MP3-320 download over FLAC tracks leaves the files on disk and the DB format unchanged.; Go: a FLAC download over MP3 tracks records the new .flac path and moves the old .mp3 into the temp recycle dir; with no recycle dir the old file stays.; Go: HeldTier/BetterTier table (MP3 tag + 320 → MP3-320; FLAC over ALAC → false; FLAC over MP3-320 → true; unknown held → any known tier is better).; Go: the candidate filter drops releases below the held tier.; Go: the heal SQL marks a grabbed music row imported when its hash is in imports and leaves others alone.; Go: detectStalledMusic with the hash in imports and no queue item → 'imported', no blocklist row.
  - **Depends on:** [MUS-02](#mus-02) (miss accounting, harness), [MUS-07](#mus-07) (setMusicGrabStatus helper; both edit detectStalledMusic), SAFE-xx (recycle-bin cap never purges the newest item); soft, should land before replaced tracks start going to the bin
  - **Risk:** Marking a grab imported on the first partial import lets ManageSeeding remove the torrent (and its data, when seeding is off) while unmatched files exist only in the download. This matches movie behaviour, and the Review flow still works while the torrent seeds. Replaced files take space in the recycle bin. HeldTier reads the stored tier string until MUS-11 makes it a measured value.
  - **Resolves:** music-5
<a id="mus-11"></a>
- [ ] **MUS-11 · Record each track's real codec and bitrate with ffprobe; keep the release tier separate** — `P2` · `M` · Phase 15 · R1
  - **Rebuild note (2026-10-19):** Reuse, don't rewrite. internal/audioserver/probe.go already runs ffprobe per audio file and parses duration, bitrate, codec and tags. Lift its ffprobe call and JSON parsing into internal/mediainfo.ProbeAudio (through toolrun, which the archtest requires in mediainfo), have audioserver call the shared function, and keep its cache, metaVersion and ABS reply shapes exactly as they are (the testdata/abs shape tests must pass). ProbeAudio also returns bit depth, sample rate, channels and bitrate mode, which audioserver ignores. Until [MUS-28](#mus-28) the measured tier and bitrate go into tracks.format / bitrate_kbps as specified; the migration copies them into music_track_files, and the remaining facts are stored from then on.
  - **Problem:** On import, the tier read from the release name is stored as every track's format (internal/automation/music.go:349, 364, 642-663). AlbumDetail shows it as a green badge (AlbumDetail.tsx:161-165), so a mislabeled release shows MP3s as FLAC. The scan stores only the extension tag ('MP3') with bitrate 0 (music_scan.go:162). The no-downgrade check (MUS-08) and the upgrade sweep (MUS-16) need the tier actually held.
  - **Approach:** 1. internal/mediainfo: ProbeAudio(path) (AudioInfo{Codec, BitrateKbps, BitsPerSample, SampleRateHz, DurationSec}, error).
       - Use the existing ffprobe invocation (-show_format -show_streams) and the first audio stream.
       - Read stream bit_rate, falling back to format.bit_rate, plus bits_per_raw_sample / sample_fmt and sample_rate.
       - The JSON parse is a separate parseAudio([]byte) for tests.
    2. internal/music: TierFromProbe(AudioInfo) Quality:
       - flac/alac/wav(pcm_*)/ape/wavpack by codec; FLAC-24 when 24-bit or above 48 kHz
       - mp3 by bitrate: ≥315 → MP3-320; 230-314 VBR → MP3-V0; 250-260 CBR → MP3-256; 170-229 → MP3-V2/192 by mode; <170 → MP3-128
       - aac ≥240 → AAC-256; opus → OPUS; vorbis → OGG
    3. Coordinator seam probeAudioFn (nil → mediainfo.ProbeAudio). importAlbumContent, importAlbumFolder and recordAlbumFiles (scan) store tracks.format = measured tier and bitrate_kbps = measured bitrate.
       - When mediainfo.Available() is false or the probe fails, use the release tier only if it agrees with FormatOf(ext) on lossless vs lossy; otherwise use the extension tag.
       - tracks.source_release keeps the release name.
    4. Backfill: ScanMusicLibrary re-probes up to 200 tracks per run where has_file=1 AND bitrate_kbps=0 (read-only ffprobe; files are never modified).
    5. Album JSON gains held_tier, the lowest tier among held tracks (music.Repo.HeldTiers from [MUS-08](#mus-08)). HeldTier now reads exact values.
    6. AlbumDetail.tsx:
       - the per-track badge is green only for lossless, with the bitrate in a tooltip
       - the header says 'Held: MP3-320'
       - when the release name claimed a better tier than measured, show a small 'labelled FLAC' note
  - **Files:** `internal/mediainfo/audio.go`, `internal/mediainfo/audio_test.go`, `internal/audioserver/probe.go`, `internal/music/release.go`, `internal/music/release_test.go`, `internal/music/repo.go`, `internal/automation/music.go`, `internal/automation/music_scan.go`, `web/src/pages/AlbumDetail.tsx`, `web/src/lib/musicApi.ts`
  - **Acceptance:**
    - An album grabbed from a release mislabeled 'FLAC' that actually contains MP3s shows MP3-xxx on the album page.
    - After a rescan, previously scanned files show real tiers and bitrates.
    - With ffprobe missing, imports still work and fall back as described.
  - **Data safety:** ffprobe only reads; files are never modified. Probes run at idle priority through toolrun.
  - **Migrations:** None.
  - **Tests:** Go: parseAudio with canned ffprobe JSON for FLAC 24/96, MP3 320 CBR, MP3 V0 and AAC 256. No real library files are used.; Go: TierFromProbe table, including pcm_s16le → WAV and opus.; Go (musicTestCoord, probeAudioFn stub): an import stores the measured format and bitrate; a disagreeing release tier with no probe falls back to the extension tag.
  - **Depends on:** [MUS-05](#mus-05) (correct release tier for the fallback agreement check), [MUS-08](#mus-08) (HeldTiers / held_tier); either order works, but land after [MUS-08](#mus-08) to avoid conflicts in importAlbumContent
  - **Risk:** ffprobe on every track adds a few seconds per album import. Inferring VBR tiers is approximate, and the bands should be generous so MUS-16 doesn't churn on borderline files.
  - **Resolves:** music-3, music-5
<a id="mus-26"></a>
- [ ] **MUS-26 · MusicBrainz client: one app-wide limiter, an honest User-Agent, a permanent local cache, and an optional self-hosted mirror** — `P1` · `M` · Phase 15 · R1
  - **Problem:** internal/metadata/musicbrainz.go paces at one call per 1.1 s with a mutex on the client value, so it only protects the app while exactly one client exists; nothing stops a second `NewMusicBrainz()`. Going over MusicBrainz's limit gets every request refused with 503, not just the extra ones. The User-Agent is a constant, `Arrmada/1.0 ( https://github.com/tristenlammi/Arrmada )`: it isn't the build's version, and "1.0" breaks the no-v1 rule. Nothing is cached (metadata_cache from 0080 is used by TMDB and Hardcover, not MusicBrainz), so every page open, listing and refresh pays 1.1 s a call again; the first load of a 300-artist library is about three hours, and every refresh repeats it. There is no way to point at a self-hosted mirror. And metadata_cache can't simply be reused: its rows are pruned by age.
  - **Approach:** 1. **One client.** main builds the only `*metadata.MusicBrainz` and passes it to music.Service, the coordinator, Discover/requests (R4) and the art fetcher (MUS-23). An archtest fails if `metadata.NewMusicBrainz` is called outside cmd/arrmada and tests.
    2. **Limiter** (`internal/metadata/mblimit.go`): one token bucket shared by every call. For musicbrainz.org the interval is fixed at 1.1 s and can't be set lower. For a mirror it is a setting (`music_mb_mirror_rate`, default 10 a second, at most 50). Two lanes: **interactive** (a person is waiting: add-artist search, opening an album, an import that is running) goes before **background** (refresh, backfill, cache refresh). Lanes share the one rate. A 503 or 429 pauses the background lane for 5 minutes (doubling to an hour) and records it in connstatus; the existing 3-try retry stays for interactive calls.
    3. **User-Agent:** `Arrmada/<buildinfo.Version> ( https://github.com/tristenlammi/Arrmada )`, with `; <contact>` appended when the admin sets `music_mb_contact` (an email, validated). The contact is kept out of logs and the support bundle (applog scrub).
    4. **Cache** (`internal/metadata/mbcache.go`, table `music_mb_cache`): key = entity kind + MBID + `inc` set (or the normalised query for searches), body = gzipped JSON, `fetched_at`, `checked_at`, `status` (200 or 404). Never pruned by age. Per-kind freshness: artist 7 days, release-group list 3 days, release 30 days, recording 30 days, search 1 day.
       - fresh → answer from the cache, no call;
       - stale → answer from the cache now, and queue a background refresh;
       - missing → fetch on the caller's lane;
       - fetch fails (503, timeout, network) → the stale answer if there is one; otherwise a typed `ErrMusicBrainzUnavailable`, which callers treat as "unknown", never as "empty".
       A lookup that comes back under a different MBID (a MusicBrainz merge) is cached under both, and from MUS-28 on the old id is stored as `musicbrainz_old`.
    5. **Stale, never gone.** The client returns `(value, stale bool, err)`. music.Service.Refresh keeps everything it has when the answer is unavailable, and an artist whose release-group list comes back empty while the cache had groups is treated as "no change" (logged, and an artist event "MusicBrainz didn't answer; kept what we had"). A test pins that syncAlbums never deletes or unmonitors an album.
    6. **Mirror setting.** A MusicBrainz card in Settings → Connections: base URL (default `https://musicbrainz.org/ws/2`; an admin-entered LAN address is allowed, like other integration URLs), the mirror rate, the contact email, a **Test** button (looks up a fixed, well-known artist MBID), cache size and entry count, and "Refresh stale entries now" (admin, a job). The cache is keyed without the host, so switching to or from a mirror keeps it.
    7. **Job** `refresh-music-cache` every 6 h: refreshes the stalest entries on the background lane, at most 300 calls a run (about 6 minutes against musicbrainz.org).
    8. **Health:** a check warns "MusicBrainz is refusing requests; Arrmada is serving cached data" while the background lane is paused, with a Fix link to the Connections card.
    9. **Docs:** a "Self-hosted MusicBrainz" section in docs/configuration.md: what a mirror needs (about 100 GB disk and 4 GB RAM without search indexes, 350 GB and 16 GB with them, a MetaBrainz token for daily updates), that its Solr must never be exposed publicly, and that Arrmada never runs a central proxy.
  - **Files:** `internal/metadata/musicbrainz.go`, `internal/metadata/mblimit.go`, `internal/metadata/mbcache.go`, `internal/metadata/musicbrainz_test.go`, `internal/store/migrations/NNNN_music_mb_cache.sql`, `internal/settings/settings.go`, `internal/music/service.go`, `internal/httpapi/` (Connections card endpoints), `web/src/components/connections/MusicBrainzCard.tsx`, `internal/health/`, `internal/applog/scrub.go`, `internal/archtest/arch_test.go`, `cmd/arrmada/main.go`, `docs/configuration.md`
  - **Acceptance:**
    - Two features calling MusicBrainz at the same moment never exceed one request per 1.1 s to musicbrainz.org (measured against a test server).
    - Opening the same artist twice makes no second network call.
    - With MusicBrainz unreachable, artist and album pages, imports from cached data and the scan keep working, and nothing is deleted, unmonitored or relisted.
    - The User-Agent carries the real build version and, when set, the contact.
    - With a mirror set, every request goes to the mirror at its own rate, and Test reports success or a plain error.
    - No code path creates a second client (archtest).
  - **Data safety:** No files are touched. A failed or empty answer never deletes, unmonitors or relists anything. Cache rows are never pruned by age.
  - **Migrations:** One, next free: `music_mb_cache(key TEXT PRIMARY KEY, body BLOB NOT NULL, status INTEGER NOT NULL, fetched_at INTEGER NOT NULL, checked_at INTEGER NOT NULL)`.
  - **Tests:** Go: the limiter with a fake clock (20 concurrent callers on two lanes → never faster than the interval; interactive ahead of background); the User-Agent string from buildinfo with and without a contact; cache fresh / stale / missing / failed paths against an httptest server that counts requests; a 503 serves the stale answer and pauses the background lane; a merged MBID is cached under both ids; Refresh with an unavailable or empty answer leaves albums and monitored flags unchanged; the mirror URL is validated and used; the contact is scrubbed from the support bundle; the archtest.
  - **Depends on:** none (Connections and connstatus shipped)
  - **Risk:** The cache grows with the library (roughly 2–10 KB gzipped per entity, so 50–100 MB for a large library); the card shows its size. Stale data can show for days when MusicBrainz is down; that is the point, and pages say "last checked".
  - **Resolves:** 
<a id="mus-27"></a>
- [ ] **MUS-27 · Choose the tag library and prove it on FLAC, ID3v2.4 and MP4 before anything depends on it** — `P1` · `M` · Phase 15 · R1
  - **Problem:** go.mod has no audio-tag library, so Music can neither read the tags files arrive with nor write any. Phase 2's matcher reads tags and its tagger writes the house style ([Design: House tag style](#house-tag-style-mus-33)), in three tag systems, with multi-value fields. The research compared the options ([Design: Tag library for Go](#tag-library-for-go-mus-27)) and recommends go-taglib (TagLib compiled to WASM, run by wazero; LGPL-2.1). Its claims must be proven on real files before Phase 2 builds on it, and the licence needs the owner's sign-off.
  - **Approach:** 1. Owner signs off the LGPL-2.1 dependency (or picks the fallback); record it in docs/decisions/music.md.
    2. New package `internal/audiotag`: `Read(path) (Tags, error)`, `Write(path, Tags, WriteOptions) error`, `ReadCover(path)`, `WriteCover(path, jpeg)`, `Properties(path)`. `Tags` is `map[string][]string` with Picard's key names. The go-taglib backend is the only implementation; nothing outside the package imports go-taglib, so a fallback can replace it.
    3. Mapping layer: the house keys of the design table → TagLib property names, plus the special cases TagLib needs explicitly (UFID for the recording MBID, TCMP/cpil, TRCK/TPOS "n/total", MP4's single-genre join). Pure code with a table test.
    4. Proof harness (`internal/audiotag/proof_it_test.go`, gated by `ARRMADA_FFMPEG_IT=1`): generate 10-second sine files with the bundled ffmpeg in a temp dir: FLAC 16/44.1, FLAC 24/96, Ogg Vorbis, Opus, MP3 CBR, MP3 VBR, M4A AAC, M4A ALAC; and an MP3 that starts with an ID3v2.3 + ID3v1 tag. For each:
       - write the full house set, including multi-value ARTISTS, ALBUMARTISTS, MUSICBRAINZ_ARTISTID and GENRE, and a front cover;
       - read it back through audiotag, and independently through `ffprobe -show_format -show_streams` tags;
       - check the MP3 header says ID3v2.4 and no ID3v1/APE tag is left;
       - check each multi-value field round-trips as separate values in each format;
       - check the decoded audio is bit-identical before and after (`ffmpeg -map 0:a -f md5`), and FLAC's STREAMINFO MD5 is unchanged;
       - run 8 writes in parallel on different files.
    5. Measure and record (in the decision doc): first-call start-up time (WASM compile), memory, time per read and write, and binary size growth. Run the proof on arm64 too (an emulated `linux/arm64` golang:1.26.9 container), and make sure `GOARCH=arm64 CGO_ENABLED=0 go build ./...` passes.
    6. Licence notices: add go-taglib (LGPL-2.1), TagLib (LGPL-2.1 / MPL-1.1) and wazero (Apache-2.0) to a third-party notices file shipped in the image, with links to their source; document how to swap the WASM (`-ldflags -X …binaryPath=`).
    7. If any proof step fails and can't be fixed in the mapping layer, stop and report: the fallback is go-flac + flacvorbis + flacpicture (FLAC) and n10v/id3v2 (MP3), MP4 deferred.
  - **Files:** `go.mod`, `go.sum`, `internal/audiotag/audiotag.go`, `internal/audiotag/mapping.go`, `internal/audiotag/mapping_test.go`, `internal/audiotag/proof_it_test.go`, `THIRD_PARTY_NOTICES.md` (or the image's notices file), `Dockerfile` (copy the notices), `docs/decisions/music.md`
  - **Acceptance:**
    - Every format in the harness round-trips every house field, multi-value fields as separate values, and the cover.
    - Audio is bit-identical after writing, in every format.
    - MP3 output is ID3v2.4 with no ID3v1 or APE tag left.
    - The amd64 and arm64 builds pass with CGO_ENABLED=0; the arm64 proof passes.
    - The decision doc records the owner's licence sign-off and the measured costs.
  - **Data safety:** The library is only ever pointed at temp files in this task, and later only at staging copies (MUS-33): it writes in place, so it must never be given a library or download file. An archtest (with MUS-33) forbids calling audiotag.Write outside the staging code.
  - **Migrations:** None.
  - **Tests:** Go: the mapping table test (every house key → each format's name, multi-value handling, MP4 genre join); the env-gated proof harness above, run in the `arrmada:sycl` image with `--cpus=3` and an `arrmada-tests-` name; an arm64 build check.
  - **Depends on:** owner sign-off of the licence
  - **Risk:** go-taglib's WASM compile adds start-up time on first use; measure it and compile lazily on first tag call, not at boot. TagLib may map a key differently from Picard; the mapping layer overrides it and the table test pins it. LGPL needs the notice to ship with the image.
  - **Resolves:** 

#### Milestone: R2: Core model — editions, files, credits, the matcher and the tagger

_Every edition of an album is stored and a target edition is chosen by policy; featured artists, collaborations and compilations are real credits; each file's measured facts are stored. Downloads are matched by a tiered, beets-style matcher that imports what it is sure of, explains what it isn't in Reviews, and accepts partial albums. Files are tagged in the house style in staging, named by a template, and moved once; cue-sheet albums are split; extras come along. After a 14-day soak the Preview label comes off. Estimate: 7–9 weeks plus the soak._

<a id="mus-28"></a>
- [ ] **MUS-28 · New music schema (editions, discs, recordings, credits, track files, external ids) and a lossless migration from today's four tables** — `P1` · `L` · Phase 15 · R2
  - **Problem:** 0070_music has artists → albums → tracks, with the file stored on the track row and `UNIQUE(album_id, disc_number, track_number)`. It can't hold editions, more than one artist on an album or a track, Various Artists compilations, a file's real properties, or any identifier beyond one MBID per row. Every later task (matcher, editions, tagging, requests) needs the wider model, and the migration must not lose a single file path, monitored flag, quality profile or event that current users have.
  - **Approach:** 1. One migration (next free number) creating the tables and columns in [Design: Data model](#data-model-mus-28), in that order (music_releases before the albums columns that reference it).
    2. In the same migration, copy the data exactly as the design's migration table says: one-name credits per artist; albums' types and credit; a legacy edition, discs, recordings and release tracks per album that has tracks rows; music_track_files for every `has_file = 1` row; clashes written to music_migration_notes, never dropped.
    3. The migration's own check (SQL `SELECT … HAVING` that makes the migration fail when counts disagree): per album, release tracks of the legacy edition = old tracks rows; mapped files + notes = old has_file rows.
    4. Go types and repo functions for the new tables in internal/music (`Release`, `Medium`, `ReleaseTrack`, `Recording`, `Credit`, `TrackFile`, `ExternalID`), with reads that assemble an album's editions and files in a fixed number of queries (no query per track).
    5. No caller changes in this task: MUS-29 cuts the code over. **MUS-28 and MUS-29 merge together** so the old code never writes `tracks` after the copy was taken.
  - **Files:** `internal/store/migrations/NNNN_music_model.sql`, `internal/music/model.go`, `internal/music/repo_model.go`, `internal/music/repo_model_test.go`, `internal/store/migrate_music_test.go`
  - **Acceptance:**
    - On a copy of a real-shaped DB, every old file path is in music_track_files or music_migration_notes, every monitored flag and quality profile is unchanged, every artist_event is still there, and grabs/blocklist/search_attempts still resolve to the same albums.
    - The migration fails, leaving the snapshot as the way back, if its counts disagree.
    - An album's editions, discs, tracks and files load in a constant number of queries.
  - **Data safety:** DB only; no file on disk is touched. BE's runner snapshots the DB before migrating. Nothing is dropped: `tracks` stays as the migration's source and an audit trail. Clashes are recorded, not discarded.
  - **Migrations:** One, next free: `music_model` (tables, columns and the data copy above).
  - **Tests:** Go: build a DB with today's migrations, fill fixtures (blank recording MBIDs, the same recording on two albums, two tracks rows with one path, an album with no listing, a two-disc album, an unmonitored track, a scanned artist with profile ''), migrate, and assert every row and value above; a forced count mismatch fails the migration; repo assembly of a two-edition album; `go test ./internal/store` migration-order tests still pass.
  - **Depends on:** [MUS-07](#mus-07), [MUS-08](#mus-08) and [MUS-11](#mus-11) shipped (their data is what gets copied)
  - **Risk:** The biggest single change in the epic. Mitigations: additive only, the self-check, the snapshot, the fixture test, and merging with MUS-29 so there is no window of divergence. SQLite ALTER TABLE ADD COLUMN with a REFERENCES clause needs a NULL default, which every such column has.
  - **Resolves:** music-5 (the structural part)
<a id="mus-29"></a>
- [ ] **MUS-29 · Cut everything over to the new model: services, import, scan, stats, API and pages** — `P1` · `L` · Phase 15 · R2
  - **Problem:** After MUS-28 the new tables exist but nothing reads them. Repo, service, sweep, import, scan, Wanted, Reviews, the dashboard counts and the three music pages all read `tracks`. Completeness also needs the new rule: an album is complete when every monitored track of its target edition has a file, which also fixes music-10's "12/12" for an album with no listing (superseding [MUS-10](#mus-10)).
  - **Approach:** 1. music.Repo/Service read and write only the new tables: `Tracks(album)` becomes the target edition's tracks with their files; MarkTrackImported writes music_track_files; ClearTrackFile clears a file row's mapping.
    2. Completeness (one grouped query, used by artist stats, the sweep's WantedAlbums, ACQ-18's Wanted rows, the dashboard and filters): `tracks = monitored release tracks of the target edition`, `have = those with a non-missing file`, `complete = tracks > 0 && have == tracks`. An album with no target edition is "listing unknown", never complete. ArtistStats gains the album counts MUS-10 specified (monitored, complete, unlisted, with files).
    3. Import and scan keep today's decisions for now (MatchTracks against the target edition) but write the new rows; MUS-31 replaces the decision.
    4. API: album detail returns editions (summary), the target edition's discs and tracks with credits and file facts (codec, bit depth / sample rate, bitrate, measured tier, tag state); artist detail shows credited-on albums separately ("Appears on"). JSON field names that pages already read stay as they are.
    5. Pages: AlbumDetail lists tracks by disc, shows the track artist when it differs from the album artist, and the measured format per file (green only for lossless); ArtistDetail shows real album counts and an "Appears on" shelf. Music.tsx filters use the new completeness. e2e fixtures updated.
    6. An archtest forbids SQL that writes the `tracks` table outside migrations.
    7. **Deleting an artist** changes meaning: credits may still reference it (featured on another artist's album), so "Delete" removes it from the library (`in_library = 0`, unmonitored) and deletes only the albums filed under it that no other library artist is credited on; rows only, files untouched unless the user also picks "delete files", which goes to the recycle bin. A credited-only artist with no remaining credits is then removed.
  - **Files:** `internal/music/repo.go`, `internal/music/service.go`, `internal/music/music.go`, `internal/automation/music.go`, `internal/automation/music_scan.go`, `internal/httpapi/music.go`, `internal/httpapi/wanted.go`, `internal/httpapi/dashboard.go`, `internal/automation/reviews.go`, `internal/archtest/arch_test.go`, `web/src/pages/AlbumDetail.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/pages/Music.tsx`, `web/src/lib/musicApi.ts`, `web/e2e/` fixtures
  - **Acceptance:**
    - After upgrading a DB with music, every album shows the same tracks and files as before, and the same completeness or better (albums with no listing now say "unknown").
    - A scanned artist with 1 of 20 albums shows "1/20 albums" and is listed under Incomplete.
    - Imports, the sweep, Wanted, Reviews and the dashboard work unchanged from the user's side.
    - No code writes `tracks` (archtest).
    - Deleting an artist who is featured on another library artist's album keeps that album and its credit intact.
  - **Data safety:** Same file rules as today's import (hardlink or copy into the library, replace through the bin per MUS-08). No file is touched by the cut-over itself.
  - **Migrations:** None (MUS-28's).
  - **Tests:** Go: completeness for complete / partial / unlisted / unmonitored-track albums; artist stats equal between list and detail; import writes music_track_files; WantedAlbums from the new model; httpapi album JSON shape (golden-style test); archtest. Web: Music.test.tsx filters; e2e album page with discs and an "Appears on" shelf.
  - **Depends on:** [MUS-28](#mus-28) (merged together)
  - **Risk:** Wide change across backend and pages. Keep JSON shapes stable where pages already read them, and land it as one merge with MUS-28.
  - **Resolves:** music-10
<a id="mus-30"></a>
- [ ] **MUS-30 · Editions: store every release, pick a target by policy, let any acceptable edition satisfy the album, and switch by hand without losing files** — `P1` · `M` · Phase 15 · R2
  - **Problem:** Today each album is read through one implicit edition (the earliest official release), not stored. Lidarr's version of this (one active edition, "most files then most tracks") is its most-reported import failure: a correct standard-edition download fails against a deluxe listing. Arrmada needs every edition stored, a stated policy, and editions that are really the same treated as one. This supersedes [MUS-19](#mus-19).
  - **Approach:** 1. metadata: `AlbumReleases(rgMBID)` from one browse call (`/release?release-group=…&inc=media&limit=100`, paged), giving status, date, country, label, barcode, media summary, track and disc counts; `ReleaseTracks(releaseMBID)` (`inc=recordings+artist-credits+isrcs`) fetched only for the target edition and for matcher candidates. Both through MUS-26's cache.
    2. Store summaries in music_releases (and music_media); full track lists only when fetched (`tracks_fetched = 1`).
    3. **Acceptable editions:** status Official (Promotion, Bootleg and Pseudo-Release only when pinned); media not in the ignored list (default: DVD, DVD-Video, Blu-ray, VHS, HD-DVD, DualDisc's video side).
    4. **Equivalence:** two editions are equivalent when they have the same number of tracks, fold-equal titles in order, and lengths within 3 s each. Any edition equivalent to the target satisfies the album.
    5. **Policy** (setting `music_edition_policy`, per-album override): `original` (default: the earliest acceptable release of the group, ties → Digital Media, then CD, then fewest tracks), `most_complete` (most tracks), or `preferred` (ordered country and media lists, e.g. GB, US, XW; Digital Media, CD).
    6. **Re-keying the legacy edition** (from MUS-28): on the album's first refresh, pick the policy edition, fetch its tracks, and move each legacy release track's file to the new edition's track by recording MBID, then (disc, position), then fold-equal title. Files that fit nothing stay as album extras (release_track_id NULL). The legacy edition row stays (marked, hidden) until it has no files.
    7. **Switching** (automatic from the matcher, MUS-31, unless pinned; or by hand): the same file-moving rule; an event "Switched to the 15-track 2003 edition"; never a file row lost.
    8. API and UI: `GET /api/v1/music/albums/{id}/editions`, `PUT /api/v1/music/albums/{id}/edition {release_id, pinned}` (RoleManager, musicRoute; a job when it must fetch). AlbumDetail gets an Edition picker listing editions with date, country, media, track count and a "same as" marker for equivalents.
  - **Files:** `internal/metadata/musicbrainz.go`, `internal/music/editions.go`, `internal/music/editions_test.go`, `internal/music/service.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `internal/httpapi/testdata/routes.golden`, `web/src/pages/AlbumDetail.tsx`, `web/src/lib/musicApi.ts`, `internal/settings/settings.go`
  - **Acceptance:**
    - Every album lists all its editions; the target is the policy's choice unless pinned.
    - A 15-track deluxe download completes an album whose target was the 12-track standard (via MUS-31's switch), and switching back by hand loses no file.
    - Equivalent editions (a US and a UK CD with the same tracks) are interchangeable.
    - Migrated albums are re-keyed to real MusicBrainz editions without losing any file.
  - **Data safety:** DB only; edition changes move file *rows*, never files. A file that fits no track of the new edition is kept as an extra, not unmapped silently or deleted.
  - **Migrations:** None (MUS-28's tables).
  - **Tests:** Go: policy table (original / most_complete / preferred, ties); acceptability (bootleg excluded unless pinned, DVD ignored); equivalence (same tracks, a 4 s length difference is not equivalent); re-keying a legacy edition by MBID, by position and by title, with an extra left over; a manual switch keeps every file row; httpapi routes 404 when off, 400 for an unknown release; canned-JSON parse of the browse call.
  - **Depends on:** [MUS-26](#mus-26), [MUS-29](#mus-29)
  - **Risk:** Big box sets have many editions; the browse is paged and track lists are fetched only when needed. Policy defaults may not suit every genre (classical); the per-album pin covers it.
  - **Resolves:** music-5
<a id="mus-31"></a>
- [ ] **MUS-31 · A beets-style tiered matcher: confident imports go in, partial albums import what matches, doubtful ones go to Reviews with a per-field breakdown** — `P1` · `L` · Phase 15 · R2
  - **Problem:** Import today needs the release name to contain the artist and album, then places files by filename number or exact title (MatchTracks). It can't use tags, editions, durations or MBIDs; it can't tell two pressings apart; and it has no middle ground between "placed" and "unmatched". Lidarr's version has fixed 80%/60% gates that reject a whole album over one track and silently pick between near-identical pressings. Library scans have the same gap (and supersede part of [MUS-20](#mus-20)).
  - **Approach:** Implement [Design: Matcher](#matcher-mus-31) in `internal/music/match/`:
    1. Grouping (disc folders merged, the tag-consensus split rule).
    2. Inputs from audiotag (MUS-27), ProbeAudio (MUS-11), filename parsing (MUS-06) and ParseRelease; grab context from the acquisition record (`ActiveByItem` / the grab hash).
    3. Candidate generation in the design's order, through MUS-26's cache on the interactive lane.
    4. Distance components with the design's weights (code constants), the beets arithmetic, foldMusic strings with the bracketed-suffix rule.
    5. A Hungarian (Munkres) assignment in pure Go; O(n³) is fine up to box sets of a few hundred tracks.
    6. The decision flow with the six settings (Settings → Media → Music matching, advanced card, with defaults and a "reset" link), the close-runner-up rule with edition equivalence, partial imports, singletons.
    7. Reviews: new reason codes `low_confidence`, `close_editions`, `partial`, `tag_conflict`, `fingerprint_conflict`, alongside the existing `mismatch`, `unmatched`, `no_media`. The breakdown is stored on the review row (`match_json`) and Reviews renders it in words ("Track 7: length off by 45 s"; "2 editions match almost equally: US CD 1997, JP CD 1997"). Reviews' actions: pick a candidate (including one not in the list, by MBID or search), import anyway into the chosen edition, or reject.
    8. ImportMusicDownloads and the scan call the matcher; MatchTracks' number/title rules survive as the track_index/title signals. Imports still go through the existing placing code until MUS-33's staging lands.
    9. Every decision is logged once at Info with the album, edition, distance and tier (no per-track spam).
  - **Files:** `internal/music/match/group.go`, `internal/music/match/candidates.go`, `internal/music/match/distance.go`, `internal/music/match/munkres.go`, `internal/music/match/decide.go`, `internal/music/match/*_test.go`, `internal/automation/music.go`, `internal/automation/music_scan.go`, `internal/automation/reviews.go`, `internal/store/migrations/NNNN_review_match_json.sql`, `internal/settings/settings.go`, `internal/httpapi/reviews.go`, `web/src/pages/NeedsYou.tsx` (Reviews breakdown), `web/src/pages/settings/` (matching card)
  - **Acceptance:**
    - A correctly tagged album auto-imports; the same album with stripped tags and good filenames auto-imports when it was grabbed for that album, and goes to Reviews when nobody grabbed it.
    - A download of the deluxe edition completes the album instead of failing; a standard-edition download matched against a deluxe target switches to the standard edition (unpinned).
    - Two non-equivalent pressings scoring within 0.03 go to Reviews naming both; equivalent pressings import silently.
    - An album with one off track imports the other tracks and names the one it left.
    - A 3-file download of a 12-track album goes to Reviews (`partial`) unless those 3 are exactly the album's missing tracks.
    - Every Reviews item says in words why it's there.
  - **Data safety:** The matcher only reads files. Nothing doubtful is placed: Reviews holds it with the download untouched. Unplaced files stay in the download. Replacements still go through the bin (MUS-08 rules).
  - **Migrations:** One, next free: reviews gain `match_json TEXT NOT NULL DEFAULT ''`.
  - **Tests:** Go: each distance component (table); the weighted arithmetic against hand-computed values; Munkres on square, rectangular and degenerate matrices; grouping (CD1/CD2, two albums in one folder, a VA folder); the decision table for every branch (strong, expected, close runner-up, equivalent editions, partial ≥ and < the minimum, wanted-tracks partial, track above max, mismatch, tag conflict); singletons; settings bounds. Fixtures are in-memory tag sets plus byte-blob files; no real library.
  - **Depends on:** [MUS-27](#mus-27), [MUS-29](#mus-29), [MUS-30](#mus-30), [MUS-04](#mus-04), [MUS-06](#mus-06), [MUS-11](#mus-11)
  - **Risk:** Thresholds that are too loose place wrong files; too strict floods Reviews. Defaults start stricter than Lidarr for unexpected files and near it for grabbed ones, and are settings. Watch the Reviews volume during MUS-12's soak and tune the defaults before graduation.
  - **Resolves:** music-4, music-5, music-14 (the matching parts)
<a id="mus-32"></a>
- [ ] **MUS-32 · AcoustID fingerprints as the matcher's second pass** — `P2` · `M` · Phase 15 · R2
  - **Problem:** Files with junk or missing tags and messy names can't be identified from text. AcoustID turns an audio fingerprint into MusicBrainz recording IDs, which is how Picard rescues such files. The owner chose it as a second pass (2026-10-19).
  - **Approach:** 1. **Fingerprinter.** Check whether the bundled jellyfin-ffmpeg has the `chromaprint` muxer (`ffmpeg -hide_banner -muxers`). If yes, fingerprint with `ffmpeg -i f -t 120 -f chromaprint -fp_format base64 -`; if not, add Chromaprint's static `fpcalc` for amd64 and arm64 to the image as its own Dockerfile stage, pinned by SHA-256 like the hdrtools and kepubify stages (LGPL-2.1, added to the notices). Run through toolrun (idle priority).
    2. **Key.** The user's own AcoustID application key, entered in Settings → Connections (an apikeys Hidden entry with a Test button), kept out of logs and the support bundle. No key → the second pass is off and Reviews says "add an AcoustID key to identify files by sound". Never a key in code.
    3. **Client** with its own limiter (3 requests a second), results cached (the fingerprint and the recording ids on music_track_files and in music_external_ids `acoustid`).
    4. **When:** the matcher runs it only when the best candidate's distance > 0.15, or tracks are missing or extra, or the worst track > 0.40, or the files have no MBIDs and fewer than half have title tags (Lidarr's trigger plus the no-tags case). Fingerprint recording ids then count as tag recording ids (track_id), candidates are regenerated from them, and the decision runs again.
    5. **Conflict:** when fingerprints name other recordings than the tags for two or more tracks → Reviews `fingerprint_conflict`.
  - **Files:** `internal/acoustid/` (client, limiter), `internal/music/match/candidates.go`, `internal/music/match/decide.go`, `internal/mediainfo/fingerprint.go`, `Dockerfile`, `THIRD_PARTY_NOTICES.md`, `web/src/components/connections/` (AcoustID card)
  - **Acceptance:**
    - With a key set, an album of untagged files named "track01.flac…" whose audio is in AcoustID is identified and imported (if grabbed for it) or offered in Reviews with the right candidate first.
    - Without a key, nothing calls AcoustID and Reviews says how to turn it on.
    - The key never appears in logs, API responses or the support bundle.
  - **Data safety:** Read-only on files (fingerprinting decodes, never writes). Runs at idle priority.
  - **Migrations:** None (MUS-28's columns and external ids).
  - **Tests:** Go: the trigger rules (table); candidate regeneration from fingerprint ids with a fake client; the conflict rule; the limiter; key redaction. Env-gated: fingerprinting a generated file works with the chosen tool on amd64 (and arm64 emulated).
  - **Depends on:** [MUS-31](#mus-31)
  - **Risk:** AcoustID is free for non-commercial use only, and coverage of obscure music is thin; it's a second pass, never required. Fingerprinting costs a decode of the first two minutes per file.
  - **Resolves:** 
<a id="mus-33"></a>
- [ ] **MUS-33 · House-style tags written in staging; copy instead of hardlink when the file is still seeding; then one move into the library** — `P1` · `L` · Phase 15 · R2
  - **Problem:** Files land with whatever tags they arrived with, so Plex, Jellyfin and Navidrome group albums and artists inconsistently, featured artists and compilations break, and no MBIDs protect future re-matches. Lidarr's tag writer flattens artists to one name and writes into hardlinked files, which changes the bytes a torrent is still seeding.
  - **Approach:** 1. **Staging.** `<root>/.arrmada-staging/<job-id>/` on the music root's drive, made with library.PrepareHiddenDir (with `.plexignore`), skipped by every scanner (SkipScanDir). A job writes `manifest.json` first: for each file its source, staging path, final path, and whether staging holds the **only** copy.
    2. **Into staging**, per file:
       - tag writing off (setting `music_write_tags`, default on) → no staging: hardlink (or copy) straight into the library as today;
       - the download client still holds the torrent (acquisition record / download snapshot), or the file has other hard links → **copy** into staging; the seeding file is never written;
       - otherwise (usenet, or seeding finished and the torrent removed) → move into staging (the manifest marks it as the only copy).
       The disk guard counts copies before starting.
    3. **Tag** with audiotag (MUS-27) in the house style ([Design: House tag style](#house-tag-style-mus-33)) from the matched edition and credits; embed the CAA front (500 px); write `cover.jpg` (1200 px) into the staging album folder; keep the tags as found in tags_json.
    4. **Verify** each file: tags read back equal what was written; the decoded audio's MD5 equals the source's (ffmpeg `-f md5` through toolrun; FLAC also compares STREAMINFO MD5). A failure leaves the source untouched, deletes only Arrmada's *copies* from staging, and sends the album to Reviews (`import_failed`, "tag write failed: …").
    5. **One move** per file into the path from MUS-34 (a rename on the same drive). If a file is already there: better per MUS-08's tier rule → the old one goes to the recycle bin first (refuse if the bin fails: the staged file stays, Reviews says why); not better → keep the old one and discard the staged copy. Then one DB transaction: music_track_files (tag_state `house`, tagged_at), grab → imported, events.
    6. **Crash recovery** (`staging-recovery` at start-up): for each manifest, finish the moves that were verified, or roll back: copies are deleted, only-copies are moved back to where they came from (or, if that's gone, into the library path untagged with tag_state `as_found`). An only-copy is never deleted.
    7. **Retag an existing album** (album action, RoleManager, a job): copy each file to staging, tag, verify, then swap: original → recycle bin, staged copy → its place. Files already in the library are never written in place. "Retag artist" runs the same per album, capped per run.
    8. An archtest forbids `audiotag.Write` / `WriteCover` outside the staging package.
  - **Files:** `internal/music/stage/` (manifest, stage, verify, swap, recover), `internal/music/housetags.go`, `internal/music/housetags_test.go`, `internal/automation/music.go`, `internal/library/importer.go`, `internal/archtest/arch_test.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `internal/httpapi/testdata/routes.golden`, `cmd/arrmada/main.go`, `web/src/pages/AlbumDetail.tsx`, `web/src/pages/settings/` (Write tags setting)
  - **Acceptance:**
    - Imported files carry every house field (spot-checked by audiotag and ffprobe), with multi-value artists and a compilation flag on a VA album, and `cover.jpg` sits beside them.
    - A torrent that is still seeding has bit-identical files after its album is imported and tagged (hash check), and the disk guard accounted for the copy.
    - Killing the app mid-import and restarting finishes or rolls back the job with no file lost and no duplicate left in the library.
    - A failed tag write leaves the download untouched and puts the album in Reviews with the reason.
    - With "Write tags" off, imports hardlink exactly as before.
    - Retagging an existing album leaves the originals in the recycle bin.
  - **Data safety:** This task is the staging rule: tags are only ever written to staging copies; a seeding file is copied, never written; one move into the library; replaced files to the bin, refusing on bin failure; an only-copy in staging is never deleted; library files are never retagged in place.
  - **Migrations:** None (MUS-28's columns).
  - **Tests:** Go (temp dirs, byte blobs, a fake tagger): copy-vs-move-vs-hardlink choice from a fake download snapshot and link counts; verify failure → Reviews and source untouched; replace better/worse; bin failure refuses; recovery for every manifest state (verified, half-moved, only-copy); the archtest. Env-gated (lavfi files): a real tag write plus verify on FLAC, MP3 and M4A; a seeding-source file's hash unchanged.
  - **Depends on:** [MUS-27](#mus-27), [MUS-31](#mus-31), [MUS-34](#mus-34) (final paths; until it lands, today's AlbumTrackTarget), [MUS-23](#mus-23)'s art cache (soft: fetch the CAA image directly until it lands)
  - **Risk:** Copies double disk use while torrents seed (the owner accepted this; "see if it becomes an issue"). Decode-verify costs a few seconds per album at idle priority. Plex may need a metadata refresh to pick up retagged albums (MUS-41 triggers a scan).
  - **Resolves:** 
<a id="mus-34"></a>
- [ ] **MUS-34 · Music naming templates, built on the books naming engine, with a safe rename of existing albums** — `P2` · `M` · Phase 15 · R2
  - **Problem:** Music paths are hard-coded (`library.Importer.AlbumTrackTarget`: `<Artist>/<Album> (Year)/<NN> - <Title>.<ext>`, disc in the file name). Owners coming from Lidarr or beets have their own layout, compilations need the track artist in the file name, and multi-disc albums want a disc prefix only when there's more than one disc. Books already has a template engine (`internal/library/booknaming.go`: tokens, "/" for folders, `<optional>` and `<A|B>` fallbacks, validation, presets, a rename journal).
  - **Approach:** 1. Lift the engine's parser, validator and renderer so books and music share them (books' behaviour and tests unchanged), and add `musicnaming.go` with music tokens: `{Album Artist}`, `{Album Artist Sort}`, `{Album Artist First Letter}`, `{Album Title}`, `{Release Year}`, `{Original Year}`, `{Album Type}`, `{Edition}` (the release's disambiguation), `{Media}`, `{Disc}`, `{Disc:00}`, `{Disc Count}`, `{Disc Title}`, `{Disc Prefix}` ("1-" only when the edition has more than one disc), `{Track:00}`, `{Track Title}`, `{Track Artist}`, `{Quality}` (measured tier), `{Codec}`, `{Bit Depth}`, `{Sample Rate}`, `{Album MBID}`, `{Release MBID}`.
    2. Three templates: folder (default `{Album Artist}/{Album Title} ({Original Year})< [{Edition}]>`), track file (default `{Disc Prefix}{Track:00} - {Track Title}`), compilation track file (default `{Disc Prefix}{Track:00} - {Track Artist} - {Track Title}`). Presets: the default, Lidarr-style, and "disc folders" (`…/Disc {Disc}/{Track:00} - {Track Title}`). The default reproduces today's paths for single-artist, single-disc albums.
    3. Settings → Library → Naming gets a Music card (the books card's pattern) with live preview against a real album.
    4. **Rename existing albums:** Phase 10's RenamePlan / ApplyRename pattern: a plan listing from → to (files, `cover.jpg`, extras and `.lrc` together), never clobbering, protected paths respected, journaled (the books rename journal's shape), run as a job, with Plex notified for both old and new folders.
  - **Files:** `internal/library/naming_engine.go` (lifted), `internal/library/booknaming.go`, `internal/library/musicnaming.go`, `internal/library/musicnaming_test.go`, `internal/library/importer.go`, `internal/httpapi/music_naming.go`, `internal/httpapi/server.go`, `internal/httpapi/testdata/routes.golden`, `web/src/pages/settings/` (Music naming card)
  - **Acceptance:**
    - The default template produces today's paths for ordinary albums, so existing libraries don't need a rename.
    - A compilation's files carry the track artist; a two-disc album gets "1-04 …" and a one-disc album "04 …".
    - Renaming an album moves its files, cover, extras and lyrics together, never overwrites anything, and can be read back from the journal.
    - Books naming behaves exactly as before (its tests unchanged).
  - **Data safety:** Renames never clobber (protected paths, the never-overwrite check); they are journaled; a failure mid-plan stops and leaves a consistent state. No file content is written.
  - **Migrations:** One, next free, only if the journal needs a music column (else none): `music_rename_journal` in the books journal's shape.
  - **Tests:** Go: token table; default reproduces today's AlbumTrackTarget output for a set of albums; disc prefix rules; compilation template; validation errors; books naming tests unchanged; rename plan with a clash refused; journal round-trip.
  - **Depends on:** [MUS-29](#mus-29)
  - **Risk:** Lifting the engine touches books; keep the change mechanical and books' tests as the guard.
  - **Resolves:** 
<a id="mus-35"></a>
- [ ] **MUS-35 · Cue-sheet albums: a single FLAC/APE/WavPack image plus a .cue imports as tracks** — `P2` · `M` · Phase 15 · R2
  - **Problem:** Many lossless rips are one file per disc with a cue sheet. Lidarr has never supported them (its most-reacted issue). Plex doesn't read cue sheets, so for a Plex-first library the image must become per-track files.
  - **Approach:** 1. A pure-Go cue parser: FILE, TRACK, TITLE, PERFORMER, INDEX 00/01, REM DATE/GENRE/DISCID; encodings UTF-8 (with or without BOM), UTF-16 with BOM, and Windows-1252 as the fallback; multi-FILE cues.
    2. The matcher (MUS-31) sees a cue image as virtual tracks with lengths from the INDEX 01 points and titles from the cue.
    3. Import splits in staging with ffmpeg through toolrun: sample-accurate cut points, FLAC output (APE and WavPack images become FLAC), pregaps (INDEX 00) appended to the previous track, a hidden track before track 1 longer than 4 s kept as an album extra. Then MUS-33 tags, verifies and moves as usual.
    4. Verify the split: the total decoded samples of the tracks equal the image's.
    5. The image and cue in the download stay untouched (seeding). An image already in the library (scan) gets a "Split into tracks" album action; after a verified split the image and its cue go to the recycle bin.
  - **Files:** `internal/music/cue/` (parser, tests), `internal/music/match/group.go`, `internal/music/stage/split.go`, `internal/automation/music.go`, `internal/httpapi/music.go`
  - **Acceptance:**
    - A FLAC+cue album downloads, matches and appears in Plex as separate, tagged tracks.
    - The seeding image is untouched; a library image is in the bin only after its tracks verified.
    - Cue files in Windows-1252 and UTF-16 read correctly.
  - **Data safety:** Splitting happens in staging; source images are never modified; a library image goes to the recycle bin only after verified tracks are in place, and the action refuses if the bin fails.
  - **Migrations:** None.
  - **Tests:** Go: cue parser table (encodings, pregaps, multi-FILE, missing INDEX 00); virtual track lengths; split command lines. Env-gated: split a generated 3-track FLAC image and compare total samples and per-track lengths.
  - **Depends on:** [MUS-31](#mus-31), [MUS-33](#mus-33)
  - **Risk:** Odd cues (HTOA, data tracks) — those not handled go to Reviews rather than being guessed.
  - **Resolves:** 
<a id="mus-36"></a>
- [ ] **MUS-36 · Extras move with the album: artwork, booklets, logs, cue sheets and lyrics** — `P3` · `S` · Phase 15 · R2
  - **Problem:** Imports today bring only the audio files. Lyrics, artwork scans, booklets, rip logs and cue sheets are left in the download folder (a 2018 Lidarr request with 55 comments).
  - **Approach:** 1. An allowlist setting (`music_extras`), default: `.jpg .jpeg .png .webp .gif` (images), `.pdf` (booklets), `.lrc .txt` (lyrics), `.log .cue .accurip .m3u .m3u8 .nfo`.
    2. Images keep their names in the album folder (a `Scans/` or `Artwork/` subfolder kept as-is); `cover.jpg` is reserved for MUS-33's cover; a downloaded `cover.jpg`/`folder.jpg` is kept as `folder.jpg` unless one exists. A `.lrc` that names a placed track (same base name) is renamed with it. Cue sheets of a split image go to the album folder unchanged (they describe the original rip).
    3. Extras are never retagged, so they are **hardlinked** (copied across drives), even while seeding. Never overwrite: an existing file with the same name is kept, and the extra is skipped with a log line.
    4. MUS-34 renames move extras with their album.
  - **Files:** `internal/music/stage/extras.go`, `internal/music/stage/extras_test.go`, `internal/settings/settings.go`
  - **Acceptance:**
    - An album's booklet PDF, scans, rip log and `.lrc` files arrive beside it, with lyrics renamed to match their tracks.
    - Nothing in the album folder is ever overwritten.
  - **Data safety:** Hardlink or copy only; never overwrite; the download is untouched.
  - **Migrations:** None.
  - **Tests:** Go (temp dirs): allowlist filtering; `.lrc` renaming; the no-overwrite rule; subfolder handling.
  - **Depends on:** [MUS-33](#mus-33)
  - **Risk:** Low. Junk files (.nfo, .txt) are on the allowlist only because rip folders use them; the owner can trim the list.
  - **Resolves:** 
<a id="mus-12"></a>
- [ ] **MUS-12 · Graduate Music out of Preview after R1 and R2 and a 14-day soak** — `P2` · `S` · Phase 15 · R2
  - **Rebuild note (2026-10-19):** Graduation now waits for R1 and R2 (the safety work, the new model, the matcher and tagging), with a 14-day soak instead of 7. Extra soak checks: no wrong-album or wrong-edition imports; tags checked in Plex/Plexamp on at least three albums (a compilation, a multi-disc album, one with featured artists); a seeding torrent's files bit-identical after their album was tagged, and the copy counted by the disk guard; no MusicBrainz 503s in the log; Reviews volume reasonable and every item explained (tune MUS-31's defaults here if not). Record the results in docs/decisions/music.md. The default stays off for fresh installs unless the owner decides otherwise.
  - **Problem:** MUS-01 labels Music 'Preview' until the Phase 1 correctness work (M3 and M4) has shipped. Someone has to check that it holds up on the real server and then lift the label, or the label either lingers forever or comes off untested.
  - **Approach:** 1. Soak: with [MUS-04](#mus-04)..[MUS-11](#mus-11) deployed, run Music enabled on the owner's server for 7 days. Watch:
       - Logs, filtered to the 'music:' prefix: searches per day stay within the backoff/cap expectation.
       - Indexers page: no rate-limit or ban warnings.
       - At least one album goes end to end: Search now → grab → import, with a measured tier that matches the profile.
       - No 'stalled' blocklist rows for completed music torrents.
       - The Seeding tab removes music torrents per their rule.
       Record the results in docs/decisions/music.md.
    2. Then:
       - /status music status → 'available'
       - remove the Sidebar 'Preview' badge (nav.ts)
       - Settings label 'Music', hint 'Artists and albums from MusicBrainz. Monitored albums download automatically, and each album says why it isn't downloading yet. Turning it off hides Music and stops its searches and imports; nothing is deleted.'
       - README line 'Music: artists, albums, automatic album downloads, discography grabs'
    3. The default stays off for fresh installs (opt-in module) unless the owner decides otherwise in docs/decisions/music.md.
  - **Files:** `internal/httpapi/server.go`, `web/src/lib/nav.ts`, `web/src/components/Sidebar.tsx`, `web/src/pages/Settings.tsx`, `README.md`, `docs/decisions/music.md`
  - **Acceptance:**
    - docs/decisions/music.md holds the 7-day soak results: searches per day, indexer warnings (none), and one end-to-end album with its tier.
    - No 'Preview' or 'still being hardened' copy remains, and /status reports 'available'.
  - **Data safety:** Copy and labels only; no files.
  - **Migrations:** None.
  - **Tests:** Go (httpapi): /status reports music status 'available' and the live enabled flag.; UI (manual): no Preview pill; the Settings hint is updated.
  - **Depends on:** [MUS-04](#mus-04)..[MUS-08](#mus-08), [MUS-11](#mus-11), [MUS-26](#mus-26)..[MUS-36](#mus-36)
  - **Risk:** A soak on one server with few artists may not exercise edge cases. Keep the Preview label if any acceptance item fails rather than graduating on a partial pass.
  - **Resolves:** music-12

#### Milestone: R3: Stay current — it runs itself

_New releases from monitored artists are found and searched within a day, filtered by type rules people can actually express. Albums say why they aren't downloading and can be searched by hand, interactively too. Upgrades follow the profile. The library scan is fast, incremental and reads tags; vanished files go back to Wanted. Artists are found by any of their names. Upcoming releases are on the Calendar. Estimate: 5–6 weeks._

<a id="mus-13"></a>
- [ ] **MUS-13 · Scheduled artist refresh that finds new releases, searches them, and fills missing listings** — `P2` · `M` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Reads through [MUS-26](#mus-26)'s cache and limiter on the background lane. An empty or failed MusicBrainz answer never removes, unmonitors or moves anything: the artist gets stale_since and an event 'MusicBrainz didn't answer; kept what we had'. A release group missing from a *successful* answer is marked stale_since and shown 'no longer on MusicBrainz', kept with its files. New release groups pass [MUS-14](#mus-14)'s type rules and singles dedupe before they're monitored. For monitored albums the refresh also updates the edition list ([MUS-30](#mus-30)), and the listing fill fetches the target edition's tracks. Register the job with a plain label and description.
  - **Problem:** New release groups only arrive through Service.Refresh, and that runs only from the manual Refresh button (internal/httpapi/music.go:92). No refresh-artists job is registered (cmd/arrmada/main.go:395-404). Monitored artists therefore never pick up a new album, which is the main reason to run Lidarr. Scanned artists' other albums also never get listings, so their completeness stays unknown (MUS-10).
  - **Approach:** 1. Migration (next free number, tentatively 00NN_artist_refresh.sql): artists.last_refreshed_at TIMESTAMP.
    2. music.Service.syncAlbums returns ([]Album newlyInserted, total int, error). It checks for an existing mbid (repo.AlbumIDByMBID) before UpsertAlbum, whose ON CONFLICT already preserves the user's monitored flag. New albums are monitored per the artist's policy: artist.Monitored now, monitor_new once [MUS-14](#mus-14) lands. Refresh stamps last_refreshed_at.
    3. automation RefreshMusicArtists(ctx), module-gated:
       - Pick artists with last_refreshed_at NULL or older than 20 h, nulls first, oldest first, at most 50 per run. The shared MusicBrainz client already paces at 1.1 s.
       - Call Service.Refresh for each.
       - For each new monitored album, write the event 'New release: Title (EP, 2026-10-03)'.
       - If the album is out (release_date ≤ today), EnsureTracks and run SearchAlbumNow ([MUS-09](#mus-09)) once. That bypasses the wait and records the outcome.
       - Afterwards fill listings for up to 30 monitored albums that have none (EnsureTracks only, no search).
    4. Future-dated albums are skipped by the [MUS-02](#mus-02) sweep without misses, so a pre-announced album is searched from its release date on.
    5. main.go: sched.Register("refresh-music-artists", 24*time.Hour, false, …) calling coordinator.RefreshMusicArtists.
  - **Files:** `internal/store/migrations/00NN_artist_refresh.sql`, `internal/music/service.go`, `internal/music/repo.go`, `internal/automation/music.go`, `cmd/arrmada/main.go`, `internal/music/service_test.go`, `internal/automation/music_refresh_test.go`
  - **Acceptance:**
    - Within a day, without pressing Refresh, a new release group for a monitored artist appears on the artist page, writes an event, and is searched once.
    - An album the user unmonitored stays unmonitored after a refresh.
    - Future-dated albums are not searched before their release date.
  - **Data safety:** DB only. No path deletes, unmonitors or relists on an upstream failure or an empty answer.
  - **Migrations:** One, next free: `artists.last_refreshed_at` (as specified).
  - **Tests:** Go (service, fakeMusicProvider whose second ArtistAlbums call returns an extra release group): the group is inserted, returned as new and monitored per the policy; an existing unmonitored album is untouched.; Go (automation): the 50-artist cap, oldest-first ordering and the 20 h freshness skip.; Go: a new, released, monitored album triggers exactly one search via musicSearchFn; a future one triggers none.; Go: the listing fill stops at 30 albums.
  - **Depends on:** [MUS-02](#mus-02), [MUS-09](#mus-09)
  - **Risk:** MusicBrainz load is about 50 artists × 2-3 calls plus up to 30 listing fills (2 calls each) a day, well under the limit. Big libraries take several days to cycle, which is fine. Avoid running at the same moment as a manual scan, because both queue on the same paced client.
  - **Resolves:** music-8
<a id="mus-14"></a>
- [ ] **MUS-14 · Add-artist options (quality profile, monitor mode, release types, search now) and a seeded default music profile** — `P2` · `L` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Release types become two sets, primary (Album, EP, Single, Broadcast, Other) and secondary (Compilation, Live, Soundtrack, Remix, DJ-mix, Mixtape/Street, Demo, Spokenword, Interview, Audio drama), and an album must pass BOTH: its primary type is allowed AND every one of its secondary types is allowed. (Lidarr uses OR, so 'studio compilations yes, live compilations no' can't be said there.) Albums with no type are shown, never hidden. 'Single already on an album': a Single whose recordings all appear on a monitored album of the same artist is listed but not monitored. 'Latest' monitors the newest *released* album, never an announced one. artists.release_types stores JSON `{"primary":[…],"secondary":[…]}`; the default reproduces today's list (Album, EP; no secondary types). ArtistAlbums now fetches all types once (cached) and filters locally, so changing the types costs no MusicBrainz calls.
  - **Problem:** AddArtistModal always sends {mbid, monitored: true} (web/src/pages/Music.tsx:398), and syncAlbums monitors every Album and EP (internal/music/service.go:106-121). Missing pieces:
- no profile picker
- no monitor option
- no release-type choice: ArtistAlbums hard-codes album|ep and drops every secondary type (metadata/musicbrainz.go:190-200)
- no search on add
- no per-artist profile picker on the artist page (the only one is the bulk action)

handleAddArtist falls back to DefaultProfile(music), which falls back to the first custom music profile. No music profile is seeded, so a user who never made one ends up on the permissive FallbackMusicProfile ('Any quality', MP3-128 accepted).
  - **Approach:** Ship this in two commits: the backend, then the UI.
    1. Migration (next free number, tentatively 00NN_artist_monitor_options.sql):
       - artists.monitor_new TEXT NOT NULL DEFAULT 'all' ('all'|'none')
       - artists.release_types TEXT NOT NULL DEFAULT 'Album,EP'
    2. Default profile seeding happens in Go, not SQL, so the ladder isn't duplicated from quality.MusicPresets(). quality.Service.EnsureDefaultMusicProfile(ctx, settings) creates 'Lossless, or the best MP3' when no music profile exists AND the settings flag music_default_profile_seeded is unset, then sets the flag so a profile the user later deletes is never resurrected. It is called from main.go at startup.
    3. metadata MusicBrainz.ArtistAlbums(ctx, mbid, types []string):
       - Request type=album|ep, plus |single only when Singles are selected, so big artists don't page through hundreds of singles.
       - Map album_type to the first secondary type when present (Live, Compilation, Soundtrack, Remix), else the primary type.
       - Return everything; the service filters by artist.release_types (Album, EP, Single, Live, Compilation, Soundtrack).
       - The defaults reproduce today's list exactly.
    4. music.Service.AddArtist(ctx, mbid, profile, AddOptions{Monitor, ReleaseTypes, SearchNow}). Monitor values:
       - all: every album monitored
       - future: existing albums unmonitored, monitor_new='all'
       - latest: the newest released album plus future ones
       - none: nothing monitored, monitor_new='none'
       syncAlbums and the [MUS-13](#mus-13) refresh use monitor_new for newly inserted albums. scanResolveArtist keeps AddArtist(…, Monitor:'none').
    5. httpapi:
       - handleAddArtist accepts monitor, release_types and search_now. search_now defaults to the search_on_add setting and starts SearchArtistNow ([MUS-09](#mus-09)).
       - New PUT /api/v1/music/artists/{id}/options {quality_profile, monitor_new, release_types} (RoleManager, musicRoute). It validates the profile with KnownProfile and the enum values (400 otherwise).
    6. UI:
       - AddArtistModal gets a second step after a result is picked: profile select (default DefaultProfile(music)), monitor radio (All / Future only / Latest / None), release-type checkboxes, and 'Start searching now'.
       - ArtistDetail gets an 'Edit' panel with the same options.
       - Existing modal and form styles are kept.
  - **Files:** `internal/store/migrations/00NN_artist_monitor_options.sql`, `internal/quality/service.go`, `internal/metadata/musicbrainz.go`, `internal/metadata/provider.go`, `internal/music/service.go`, `internal/music/repo.go`, `internal/music/music.go`, `internal/automation/music_scan.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `cmd/arrmada/main.go`, `web/src/pages/Music.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - Adding an artist with 'Future only' makes nothing wanted now, and the artist's next release is wanted.
    - The add dialog shows the chosen profile, and the artist page can change it.
    - A fresh install gets one sensible music profile, and 'Any quality' is no longer the silent default.
    - Existing artists' album lists don't change after a refresh (release_types defaults to Album,EP).
  - **Data safety:** DB only. Changing an artist's types never unmonitors an album that has files; it changes only what new albums get.
  - **Migrations:** One, next free: `artists.monitor_new`, `artists.release_types` (as specified, with the JSON shape above).
  - **Tests:** Go (service): each monitor option ('future' leaves existing albums unmonitored; 'latest' monitors exactly one released album; 'none' monitors nothing).; Go: the release-type filter keeps Singles only when selected and maps a Live secondary type to album_type 'Live'.; Go (quality): EnsureDefaultMusicProfile seeds exactly once on an empty DB, not when a music profile exists, and not again after the seeded profile is deleted.; Go (httpapi): unknown monitor values and unknown profiles are rejected with 400.; Go (metadata, canned JSON): the query includes 'single' only when requested.
  - **Depends on:** [MUS-13](#mus-13) (refresh consumes monitor_new), [MUS-09](#mus-09) (SearchArtistNow for Search now)
  - **Risk:** Including Singles for big artists can add hundreds of albums, so it stays opt-in. The seeding flag lives in settings; if CFG restructures settings storage, keep the key.
  - **Resolves:** music-8
<a id="mus-09"></a>
- [ ] **MUS-09 · Say why an album isn't downloading on its own page: latest outcome, next try, Search now (outcomes already recorded by ACQ-16)** — `P1` · `S` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Re-scoped. ACQ-16 already records every music search in search_attempts, and ACQ-18's Wanted (Activity → Wanted) lists every missing album with its outcome, next try and Search now. So: **no** albums.last_search_outcome/detail columns and no migration (step 1–2 and 4 are dropped). What remains: the album page shows the latest attempt (LatestAttempts / AttemptSummary, ScopeAlbum) and the next try (AlbumNextSearchAt), with a Search now button that calls the existing `POST /api/v1/wanted/music/{id}/search` (a job, followed with useJob); the artist page gets 'Search monitored' as one job (single-flight per artist through internal/jobs) instead of step 5's goroutine; artist History gets a 'searched' event only when the outcome code changes; outcome wording comes from lib/searchOutcome.ts, not a second map; pages refresh through useLiveEvent, not useLive.
  - **Problem:** There is no album search route (internal/httpapi/server.go:401-414). grabAlbum returns silently on an indexer error or an empty result (automation/music.go:92-93). 'No release matched', 'no release met the profile' and 'not enough space' are log lines only (:105-121) and never become artist_events. detectStalledMusic writes no event. The album page offers only a Monitor toggle, and music pages don't use useLive.

The owner sees a wall of wanted albums with no way to tell whether Arrmada searched, found nothing, rejected everything or ran out of space, and no way to try again. Without this there is also no workaround for matching bugs.
  - **Approach:** 1. Migration (next free number, tentatively 00NN_album_search_outcome.sql): albums.last_search_outcome TEXT NOT NULL DEFAULT '' and albums.last_search_detail TEXT NOT NULL DEFAULT ''.
    2. music.Repo.RecordSearchOutcome(ctx, albumID, code, detail, miss bool). It sets last_search_at, the outcome and the detail, and increments or resets misses per [MUS-02](#mus-02)'s rules, all in one UPDATE. The sweep switches to it.
    3. Events: write an artist_events 'searched' row only when the code differs from the stored one, e.g. 'OK Computer: 14 releases, none named this album', so History doesn't flood. 'grabbed' keeps its existing event. detectStalledMusic writes a 'failed' event ('Download stalled after 60 min, blocklisted: <release>').
    4. Album JSON adds last_search_at, last_search_outcome, last_search_detail and a computed next_search_at: last_search_at + musicSearchWait(misses), 'now' when never searched, the release date when it is in the future.
    5. Coordinator:
       - SearchAlbumNow(ctx, albumID) (albumOutcome, error) ignores the wait but still honours albumDownloading, the module gate and the disk check.
       - SearchArtistNow(ctx, artistID) (int, error) runs the artist's monitored incomplete albums one at a time in a goroutine with a 30 min context. A sync.Map guard prevents a second run for the same artist.
    6. Routes, all RoleManager and wrapped in musicRoute:
       - POST /api/v1/music/albums/{id}/search: synchronous, 90 s timeout, returns {outcome, detail, release}.
       - POST /api/v1/music/artists/{id}/search: 202 {count}.
    7. UI:
       - AlbumDetail.tsx: a 'Search now' button and an outcome line ('Searched 2 h ago: 14 releases, none named this album. Next automatic search in about 6 h.'). Copy for each code lives in one map in web/src/lib/api.ts.
       - ArtistDetail.tsx: album rows show the short outcome under the title, and the header gets 'Search monitored'.
       - HistoryPanel: a tone for 'searched'.
       - Both pages refetch on 'music.imported' via useLive (web/src/lib/useLive.ts).
       - Wording should match ACQ's shared 'why isn't this downloading' copy where it exists.
  - **Files:** `internal/store/migrations/00NN_album_search_outcome.sql`, `internal/music/music.go`, `internal/music/repo.go`, `internal/music/service.go`, `internal/automation/music.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `web/src/pages/AlbumDetail.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - Every monitored missing album shows when it was last searched, why nothing was grabbed, and when the next automatic search is.
    - 'Search now' answers in the UI within 90 s, even while the album is backed off.
    - Artist History shows when an album's search result changes, not one entry per sweep.
    - A stalled album download appears in History.
  - **Data safety:** No files. DB rows only (events).
  - **Migrations:** None (ACQ-16's search_attempts).
  - **Tests:** Go: each outcome code is produced by a scripted case: musicSearchFn returning nothing, non-matching releases, below-profile releases, an error; musicGrabFn success; a disk-full stub via downloadsDir.; Go: event dedupe: two identical outcomes in a row write one event; a different code writes a second.; Go (httpapi): the album search endpoint searches even with misses=5 and last_search_at=now, and returns 404 when the module is off.; Go: next_search_at for never-searched, backed-off and future-dated albums.
  - **Depends on:** [MUS-02](#mus-02)
  - **Risk:** The synchronous search holds a request for up to 90 s, the same pattern as book releases. The background artist search is an untracked goroutine until BE's job runner exists; move it there when that lands.
  - **Resolves:** music-9
<a id="mus-15"></a>
- [ ] **MUS-15 · Tracks whose files vanish go back to Wanted, safely (disc folders move to the matcher)** — `P2` · `M` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Re-scoped onto the new model. Step 1 (CD1/CD2 folders as one album) moves into the matcher's grouping ([MUS-31](#mus-31)) and scan v2 ([MUS-37](#mus-37)). This task keeps step 2–4, the verify pass, over music_track_files: a vanished file sets missing_since on its row (kept, so a remount re-links it by path or file id), its album counts it as missing and is wanted again, with one event per album. The guards stay: a missing or unreadable music root, or more than 20% of files gone at once, changes nothing and raises a health warning (the Phase 10 movie missing-file pattern).
  - **Problem:** findAlbumFolders reads Artist/Album/CD1 as artist 'Album', album 'CD1' (internal/automation/music_scan.go:220-228). Multi-disc albums never catalogue, and scanResolveArtist may even create an unrelated artist on an exact name hit (:111-129).

ClearTrackFile (internal/music/service.go:197-200) has no callers and the scan only adds files. Tracks deleted on disk stay has_file=1 forever, and their albums are never wanted again.
  - **Approach:** 1. findAlbumFolders: when the leaf matches reDiscFolder `(?i)^(cd|disc|disk)\s*0*(\d{1,2})\b` (optionally followed by a title), merge it into the parent album:
       - album = parent, artist = grandparent; if the grandparent is the root, parse the parent as 'Artist - Album'
       - all discs become one albumFolder
       - each file carries AudioFile.Disc = n ([MUS-06](#mus-06)), which MatchTracks uses when filenames carry only a track number
       importDiscographyContent gets this for free, since it uses findAlbumFolders.
    2. New Coordinator.VerifyMusicFiles(ctx) (int, error), module-gated:
       - Load has_file tracks (repo.TracksWithFiles: id, album_id, artist_id, album title, file_path).
       - os.Stat each path. For each missing file call music.ClearTrackFile, and write ONE artist event per album: '3 track files of "X" are gone from disk, wanted again'.
       - Missing tracks also reset the album's search_misses, so the sweep picks it up promptly.
    3. Safety: skip the whole pass with a warning, changing nothing, when:
       - the music root (c.imp.MusicDir()) is missing or unreadable, or
       - more than 20% of recorded files are missing at once (an unmounted share or a changed root)
       It never mass-flips a library.
    4. Run it at the end of ScanMusicLibrary and as a daily 'verify-music-files' job registered in main.go.
  - **Files:** `internal/automation/music_scan.go`, `internal/automation/music_scan_test.go`, `internal/music/match.go`, `internal/music/repo.go`, `internal/automation/music.go`, `cmd/arrmada/main.go`
  - **Acceptance:**
    - A library laid out as Artist/Album/CD1 and CD2 catalogues as one album on Scan library.
    - A track deleted on disk shows as missing after the next verify.
    - An unmounted music share or a changed music root does not flip the library to missing.
  - **Data safety:** DB only; never deletes or moves a file. The guards stop an unmounted share from flipping the library to missing.
  - **Migrations:** None (`music_track_files.missing_since` is in [MUS-28](#mus-28)).
  - **Tests:** Go: TestFindAlbumFolders gains Artist/Album/CD1 and CD2 → album=Album, artist=Artist, disc hints 1 and 2; 'Artist - Album/Disc 2' under the root parses too.; Go (temp dirs only): VerifyMusicFiles clears a deleted file and writes one event; it does nothing when the root is missing, and nothing when more than 20% of files are missing.
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild), [MUS-06](#mus-06) (AudioFile.Disc hint)
  - **Risk:** A slow or flaky share could look like vanished files; the thresholds guard against that. Clearing only changes DB state and never deletes anything.
  - **Resolves:** music-14
<a id="mus-16"></a>
- [ ] **MUS-16 · Music upgrade sweep that honours upgrades_enabled** — `P2` · `M` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Held quality comes from music_track_files (measured), the lowest tier across the album's mapped files. Grabs and imports go through the matcher ([MUS-31](#mus-31)) and staging ([MUS-33](#mus-33)); replaced files go to the recycle bin via RemoveToBin, refusing if the bin fails. A download that covers only part of the album upgrades only the tracks it makes strictly better, and the album keeps its upgrade want. The candidate filter includes [MUS-21](#mus-21), and the sweep obeys the per-sweep upgrade budget the other upgrade sweeps use. COPY-04 hid the music 'Automatically upgrade' switch; step 5 restores it.
  - **Problem:** Music profiles carry upgrades_enabled and the presets turn it on, but no code ever upgrades an album. SearchMusicMissing skips complete albums, and no upgrade-music job exists. Comments in pickBestAlbum (automation/music.go:134-138) and quality/music.go:9-14 assume an upgrade loop that doesn't exist. An owner on 'Lossless, or the best MP3' keeps their MP3s forever. Until this ships, the COPY task for music-7 hides the music 'Automatically upgrade' switch.
  - **Approach:** 1. Migration (next free number, tentatively 00NN_album_upgrade_backoff.sql): albums.last_upgrade_search_at TIMESTAMP, albums.upgrade_misses INTEGER NOT NULL DEFAULT 0.
    2. automation UpgradeMusic(ctx), module-gated, considers complete, monitored albums of monitored artists where:
       - the profile has UpgradesEnabled, and
       - held_tier ([MUS-11](#mus-11)) ranks below the profile's top-scored tier, and
       - now ≥ last_upgrade_search_at + musicSearchWait(upgrade_misses)
       It processes at most 20 albums per run, oldest first, and skips albums in the queue.
    3. Candidates must pass the shared filter (ReleaseIsForAlbum, [MUS-21](#mus-21) sanity, blocklist, pending) and albumScore, and BetterTier(cand, held) must be true. Grab with the event 'Upgrading "X" from MP3-320 to FLAC'. The import replaces files through [MUS-08](#mus-08)'s path, with old files going to the recycle bin. A miss increments upgrade_misses; a grab resets it.
    4. main.go: register 'upgrade-music' every 6 h.
    5. Restore the Quality.tsx music 'Automatically upgrade' switch and copy (hidden by the COPY music-7 task). Rewrite the comments at automation/music.go:134-138 and quality/music.go:9-14 to describe the real loop.
  - **Files:** `internal/store/migrations/00NN_album_upgrade_backoff.sql`, `internal/automation/music.go`, `internal/music/repo.go`, `cmd/arrmada/main.go`, `web/src/pages/Quality.tsx`, `internal/quality/music.go`, `internal/automation/music_upgrade_test.go`
  - **Acceptance:**
    - An album held as MP3-320 under 'Lossless, or the best MP3' is replaced by FLAC when a FLAC release appears, and the MP3s go to the recycle bin.
    - With the switch off, no upgrade searches are logged.
    - An album already at the profile's top tier is never searched.
  - **Data safety:** Replaced files go to the recycle bin, refusing on bin failure; a worse or equal file never replaces a held one (BetterTier is strict, lossless never replaces lossless).
  - **Migrations:** One, next free: `albums.last_upgrade_search_at`, `albums.upgrade_misses` (as specified).
  - **Tests:** Go: candidate selection: with MP3-320 held and FLAC on top, a FLAC release is accepted and MP3-V0 is rejected.; Go: upgrades_enabled=false → no search (musicSearchFn never called).; Go: held FLAC with FLAC on top → album skipped.; Go: upgrade_misses backoff, and the 20-album cap.
  - **Depends on:** [MUS-11](#mus-11), [MUS-08](#mus-08), [MUS-02](#mus-02), COPY-xx (music-7 copy task that hides the music upgrade switch, draft music.t4)
  - **Risk:** Indexer load, bounded by the per-run cap and the backoff. Replaced files fill the recycle bin, so SAFE's recycle-bin cap fix matters here. VBR tier inference must not cause churn (BetterTier is strict, and lossless never replaces lossless).
  - **Resolves:** music-7
<a id="mus-17"></a>
- [ ] **MUS-17 · Interactive album search with reasons, and manual grabs that import into the chosen album** — `P2` · `M` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Use the shared interactive search: release rows carry a token, never a download URL (SEC-07), the grab endpoint takes `{token}`, and the search runs as a job (a.submit) like the other interactive searches; the response-scan test must pass. Routes: `GET /api/v1/music/albums/{id}/releases` and `POST /api/v1/music/albums/{id}/grab {token}` (RoleManager, musicRoute, golden table regenerated). A manual grab imports into the chosen album through the matcher with that album as the expected candidate: the matcher still decides positions and still sends doubtful files to Reviews. Results show an edition hint when the name or size suggests one ('Deluxe', '2CD').
  - **Problem:** There is no interactive search for an album. The owner can't see what indexers return, can't tell why each result was refused, and can't override when the automatic gate is wrong (the music-2/3/4 class of bugs).
  - **Approach:** 1. automation RankAlbumReleases(ctx, albumID) (ReleaseList, error) runs the grabAlbum query plus a foldMusic variant ([MUS-04](#mus-04)) when it differs, deduplicated by normTitle. For each release it fills a RankedRelease (coordinator.go:347):
       - Format = DetectQuality tier
       - Summary like 'FLAC · 412 MB · 12 seeders'
       - Blocklisted
       - Eligible, with a plain-words RejectReason: "doesn't name this album", "MP3-320 isn't on this profile's ladder", "rejected term 'web'", 'blocklisted: stalled', 'lower than the FLAC you have', plus [MUS-21](#mus-21)'s size and category reasons
       - Recommended = the pickBestAlbum choice
       ReleaseList.Why explains the pick.
    2. GrabForAlbum(ctx, albumID, indexer, url, title):
       - grabTo on musicCategory
       - recordMusicGrab('music', albumID)
       - markGrabManual (store.go:217, column from 0076)
       - an event 'Grabbed by hand: <release>'
    3. Import routing: in ImportMusicDownloads, when grabbedMediaForHash finds the album and grabWasManual(hash) is true, import into that album without the release-name check. The user chose it, and MatchTracks still refuses to guess positions.
    4. Routes, both RoleManager and wrapped in musicRoute:
       - GET /api/v1/music/albums/{id}/releases
       - POST /api/v1/music/albums/{id}/grab
    5. UI:
       - AlbumDetail.tsx 'Interactive search' opens ReleaseSearchModal with a new variant="audio" prop that hides the resolution/HDR/feature chips and the bitrate sort and shows the Format column.
       - Movie and series callers don't pass variant, so they are unchanged.
  - **Files:** `internal/automation/music.go`, `internal/automation/coordinator.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `web/src/components/ReleaseSearchModal.tsx`, `web/src/pages/AlbumDetail.tsx`, `web/src/lib/api.ts`, `internal/automation/music_interactive_test.go`
  - **Acceptance:**
    - From an album page the owner sees every release, whether it is eligible and why not, and can grab any of them; the download lands on that album.
    - Movie and series interactive search look unchanged.
  - **Data safety:** No file rules beyond the import path's (MUS-31, MUS-33). A manual grab never bypasses Reviews for doubtful files.
  - **Migrations:** None (grabs.manual exists).
  - **Tests:** Go: ranking over a fixed release list asserts eligibility, the reason strings and the recommended pick.; Go: a manual-grab import whose release name doesn't name the album still imports into the grabbed album; a non-manual one still goes to review.; UI (manual): the audio variant of the modal shows no video chips; movie search is unchanged.
  - **Depends on:** [MUS-09](#mus-09), [MUS-04](#mus-04), [MUS-05](#mus-05), [MUS-21](#mus-21) (size and category reasons); soft, FE-xx (shared modal kit); soft
  - **Risk:** A manual override can import the wrong audio. That is the user's explicit choice, and per-track matching still won't guess. Two queries per interactive search double the indexer load for that one action only.
  - **Resolves:** music-9
<a id="mus-21"></a>
- [ ] **MUS-21 · Drop video and implausibly sized releases from album searches** — `P3` · `S` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Unchanged. INT-23 (shipped) already sends audio categories from caps; this filter still guards mixed-category indexers and fakes.
  - **Problem:** grabAlbum searches with no Categories (internal/automation/music.go:89-91), and albumScore never checks Release.Categories or size. torznab.go:347-352 falls back to the indexer's configured categories, so only mixed-category indexers are affected. On those, a name like 'Pink Floyd The Wall 1982 1080p BluRay FLAC 2.0 x264' passes and scores FLAC, and tiny fake 'FLAC' torrents can win on seeders.
  - **Approach:** 1. A pure helper in internal/music/size.go: ReleaseSane(rel Release-like fields, tier Quality, listingSeconds int) (ok bool, reason string).
       - Drop when rel.Categories is non-empty, contains none of 3000/3010/3040/3050/3060, and has no custom tracker category (≥100000). Reason: 'listed as video'.
       - Drop names with video markers (2160p, 1080p, 720p, x264, x265, HEVC, BluRay, WEB-DL) via one regex. Reason: 'looks like a video release'.
       - Size window: expected = listing seconds (sum of tracks.duration_sec, else track count × 240) × the tier's rate in MB/min (FLAC-24 ≈ 20, FLAC/ALAC/WAV ≈ 8 (WAV ≈ 10), MP3-320 ≈ 2.4, V0 ≈ 1.8, 256 ≈ 1.9, 192 ≈ 1.4, 128 ≈ 1). Reject below 35% or above 400% of expected, with a reason like '30 MB is too small for a 45-minute FLAC album'.
    2. The automation candidate filter (shared by grabAlbum, UpgradeMusic and RankAlbumReleases) calls it after ReleaseIsForAlbum. Rejections count toward the no_match/below_profile detail.
    3. Outgoing query categories don't change. Caps-aware audio categories belong to INT's integrations-10 task.
  - **Files:** `internal/music/size.go`, `internal/music/size_test.go`, `internal/automation/music.go`
  - **Acceptance:**
    - No concert-video torrent is grabbed for an album.
    - A 30 MB 'FLAC' release for a 45-minute album is refused, and the reason shows in interactive search.
  - **Data safety:** Pure functions; no file or DB writes.
  - **Migrations:** None.
  - **Tests:** Go filter table:
- '1080p BluRay FLAC 2.0 x264' name → rejected
- 30 MB FLAC for a 45-min listing → rejected
- 300 MB FLAC → accepted
- categories [2040] → rejected
- [3040] → accepted
- [] → accepted
- [100123] → accepted
- a listing with no durations uses track count × 4 min
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild), INT-xx (integrations-10 Torznab caps and audio categories); related, not blocking
  - **Risk:** Unusually long or hi-res albums could fall outside the window, so the bounds are generous and the reason is visible in interactive search.
  - **Resolves:** music-15
<a id="mus-37"></a>
- [ ] **MUS-37 · Library scan v2: parallel, incremental and throttled; reads tags and MBIDs; unmatched folders go to Reviews** — `P2` · `L` · Phase 15 · R3
  - **Problem:** ScanMusicLibrary walks the folder, guesses `Artist/Album` from folder names, creates an artist only on an exact name match, reports nothing back, and re-reads everything every time. Lidarr's scans of large libraries take hours and gigabytes of RAM. Owners importing an existing collection need a scan that is fast, uses the tags and MBIDs their files already carry, and shows what it couldn't place. Supersedes [MUS-20](#mus-20).
  - **Approach:** 1. **Incremental:** compare each file's size, mtime and file id with music_track_files; unchanged files aren't opened. A moved file (same file id, new path) updates its row instead of looking deleted.
    2. **Parallel and throttled:** a bounded worker pool (default 4, a setting) for tag reads and probes, through toolrun at idle priority; a per-run cap so a first scan of a huge library proceeds in chunks; progress through the job runner.
    3. **Tags first:** groups go through the matcher (MUS-31) with no grab context, so the strict threshold applies. Files with release MBIDs match exactly; others by tags, then folder names.
    4. **Artists:** an artist found in tags (by MBID or a single exact match) is added unmonitored (`in_library = 1`), as today.
    5. **Results:** a scan summary (counts, duration) shown in a results drawer; every group that didn't import becomes a Reviews item (`unmatched` or `low_confidence`) with its candidates and the Match… action (pick an album or edition, by search or MBID), replacing MUS-20's separate table.
    6. Scan never moves, renames or retags files: placement in the library as-is is recorded (tag_state `as_found`); renaming and retagging are separate, explicit actions (MUS-33, MUS-34).
    7. Skips bins, staging and hidden Arrmada folders (SkipScanDir).
  - **Files:** `internal/automation/music_scan.go`, `internal/automation/music_scan_test.go`, `internal/music/match/`, `internal/automation/reviews.go`, `internal/httpapi/music.go`, `web/src/pages/Music.tsx` (results drawer)
  - **Acceptance:**
    - A rescan of an unchanged 10,000-track library opens no file and finishes in seconds.
    - A well-tagged existing collection imports without any folder-name guessing.
    - Every folder that didn't import is in Reviews with a reason and can be matched by hand without renaming anything on disk.
    - A renamed album folder is followed, not reported missing.
  - **Data safety:** Read-only on files. DB rows only. Skips bins and staging.
  - **Migrations:** None (MUS-28's columns).
  - **Tests:** Go (temp dirs, fake tagger and prober): incremental skip, moved-file detection, worker-pool bound, chunking cap, Reviews items per reason, SkipScanDir respected; a 10,000-file synthetic tree benchmark that asserts zero opens on the second run.
  - **Depends on:** [MUS-31](#mus-31), [MUS-27](#mus-27)
  - **Risk:** First scans of big libraries still take a long time because of MusicBrainz's 1/s (the cache helps on every later run); progress and chunking make that visible.
  - **Resolves:** music-14
<a id="mus-38"></a>
- [ ] **MUS-38 · Find artists by any of their names: aliases, other scripts and old names** — `P2` · `S` · Phase 15 · R3
  - **Problem:** Search, release matching and the scan use only an artist's primary name. Releases named by an alias, a transliteration or an old name never match (Lidarr issues with 30 and 39 reactions), and non-Latin artists are hard to add.
  - **Approach:** 1. Fetch aliases (`inc=aliases`) with the artist, cached; store them as music_external_ids `alias` rows (with locale and type), and keep the primary-locale Latin alias when the name isn't Latin.
    2. Add-artist search shows a matching alias under the result ("also known as …").
    3. Release matching (ReleaseIsForAlbum) and the matcher's artist component accept any alias (each through foldMusic); the search query adds a second query by the Latin alias only when the primary name isn't Latin.
  - **Files:** `internal/metadata/musicbrainz.go`, `internal/music/service.go`, `internal/music/match.go`, `internal/music/match/distance.go`, `internal/automation/music.go`, `web/src/pages/Music.tsx`
  - **Acceptance:**
    - A release named by an artist's alias is grabbed and imported.
    - A Japanese or Korean artist can be added by its romanised name.
  - **Data safety:** No files. Aliases only widen matching for the artist's own albums; the album check still applies.
  - **Migrations:** None (music_external_ids).
  - **Tests:** Go: alias parse from canned JSON; ReleaseIsForAlbum with an alias; the second-query rule.
  - **Depends on:** [MUS-26](#mus-26), [MUS-28](#mus-28)
  - **Risk:** Common-word aliases could widen matches; the album-title check and the matcher still gate.
  - **Resolves:** 
<a id="mus-24"></a>
- [ ] **MUS-24 · Show Music on the Dashboard and Calendar** — `P3` · `S` · Phase 15 · R3
  - **Rebuild note (2026-10-19):** Calendar entries come from release groups of monitored artists with a future or recent date (filled by [MUS-13](#mus-13)), in the agenda view (lib/calendar.ts) too. Dashboard counts use [MUS-29](#mus-29)'s completeness; recent activity already includes artist events (History's feed).
  - **Problem:** From the audit's Phase 3 overhaul (not a single finding). Music is invisible outside its own pages. The dashboard computes artist and album counts (internal/httpapi/dashboard.go:270-271) but never renders them, and the Calendar has no upcoming album releases.
  - **Approach:** 1. dashboard.go:
       - add MissingAlbums: monitored albums of monitored artists that are not complete, one grouped query
       - include the music counts only when musicEnabled
       - Dashboard.tsx shows Artists / Albums / Missing tiles, linking Missing to Music → Wanted ([MUS-18](#mus-18))
    2. calendar.go: when musicEnabled, add monitored albums whose release_date falls in the window as Type 'album' entries (Title = album, Subtitle = artist) linking to /music/album/:id. Calendar.tsx gets the entry style.
    3. recentActivity: include artist_events (grabbed / imported / failed).
    4. Coordinate rather than duplicate: the music.imported notification is OBS's insights-6, and Downloads labelling is ACQ's ops-10.
  - **Files:** `internal/httpapi/dashboard.go`, `internal/httpapi/calendar.go`, `internal/httpapi/dashboard_test.go`, `web/src/pages/Dashboard.tsx`, `web/src/pages/Calendar.tsx`
  - **Acceptance:**
    - With Music on, the dashboard shows music counts and recent music activity; with it off, they are hidden.
    - Upcoming monitored album releases appear on the Calendar when Music is on.
  - **Data safety:** Read-only.
  - **Migrations:** None.
  - **Tests:** Go: dashboard_test asserts the artist, album and missing-album counts, and their absence when the module is off.; Go: the calendar handler returns an upcoming album entry.; UI (manual): tiles are hidden when the module is off.
  - **Depends on:** [MUS-01](#mus-01), [MUS-13](#mus-13), [MUS-18](#mus-18) (Wanted link); soft, OBS-xx (insights-6 music notification); related, ACQ-xx (ops-10 labels); related
  - **Risk:** Low.
  - **Resolves:** 

#### Milestone: R4: Household — the family can ask for music and find it in Plex

_Requesters see a Music tab in Discover and can request an album or a single song, with approval and a "ready" notice once Plex has it. Imports trigger a Plex scan of the album's folder. The library has artwork, artist images and bios, all cached locally. Estimate: 3–4 weeks._

<a id="mus-39"></a>
- [ ] **MUS-39 · A Music tab in Discover: search and browse artists and albums, see what's already in the library** — `P2` · `M` · Phase 15 · R4
  - **Problem:** Discover covers films, shows and books. Family members can't find or ask for music anywhere in Arrmada.
  - **Approach:** 1. A Music tab in Discover (features/discover/*, behind the module toggle and a lazy chunk), following the Books tab's shape: a search box (artists, albums and songs from MusicBrainz through the cache's interactive lane), an artist page (photo, bio, albums by type, "in your library" chips), an album sheet (cover, tracks, editions collapsed, request button).
    2. Rows: "New from artists you have" (from MUS-13's refresh), "Recently added", and later R5's suggestions.
    3. In-library state from the new model: complete, partly here (with which tracks), or wanted.
    4. Requester-safe API under `/api/v1/discover/music/*` (signed-in, musicRoute); no staff fields; art served from Arrmada's cache.
  - **Files:** `web/src/features/discover/music/*`, `web/src/features/discover/tabs.tsx`, `internal/httpapi/discover_music.go`, `internal/httpapi/server.go`, `internal/httpapi/testdata/routes.golden`, `web/e2e/` fixtures
  - **Acceptance:**
    - A requester can search "Radiohead", open OK Computer, and see whether it's in the library.
    - The requester first-load bundle stays within budget (the tab is a lazy chunk).
    - With Music off, the tab and its API are gone.
  - **Data safety:** Read-only.
  - **Migrations:** None.
  - **Tests:** Go: the discover endpoints' shapes and in-library states; module-off 404; route golden. Web: vitest for the tab; e2e with mocked fixtures; check-size passes.
  - **Depends on:** [MUS-29](#mus-29), [MUS-30](#mus-30), [MUS-23](#mus-23) (art cache)
  - **Risk:** MusicBrainz search latency at 1/s on a cold cache; results show progressively and are cached.
  - **Resolves:** 
<a id="mus-40"></a>
- [ ] **MUS-40 · Album and song requests: approval, quotas, and "ready" once Plex has it** — `P2` · `L` · Phase 15 · R4
  - **Problem:** There is no way for a family member to ask for music. Requests 2.0 handles films, series and books (formats, seasons, quotas, auto-approve, ready_at), but not albums, and nothing in the model can want a single song.
  - **Approach:** 1. Request kinds `album` (a release group; optional edition preference) and `song` (a recording). requests.Create/Approve/Decline take them; announceCreated, shapeRequests, quotas (request_usage) and the RequestSheet handle them; auto-approve for music is off by default (a setting next to the others).
    2. **Album request approved:** add the album (and its artist with `in_library = 1`, unmonitored unless the approver chooses), monitor it, search now (job).
    3. **Song request approved:** a row in `music_song_wants(recording_id, request_id, …)`; the sweep searches for the song's preferred album (the earliest official album containing the recording, else its single), and the matcher's singleton/partial rules import only the wanted track(s) when the download is a whole album: the other tracks are not placed unless their album is monitored.
    4. **Ready:** a request is ready when its album is complete (or its song has a file) **and** Plex has it (MUS-41's Locate); ready_at stamped; requester notified (inbox, push, personal Apprise) with a Plexamp/Plex link.
    5. Discover (MUS-39) shows request state; /requests lists music requests with the artist as byline.
  - **Files:** `internal/requests/*`, `internal/store/migrations/NNNN_music_song_wants.sql`, `internal/music/songwants.go`, `internal/automation/music.go`, `internal/httpapi/requests.go`, `web/src/components/RequestSheet.tsx`, `web/src/features/discover/music/*`, `web/e2e/` fixtures
  - **Acceptance:**
    - A requester asks for an album; once approved it's searched, imported, and the requester is told it's ready only after Plex shows it.
    - A requester asks for one song; only that track is placed from an album download (unless the album is monitored), and the request turns ready.
    - Quotas and approval work as for other media; requesters never see staff fields.
  - **Data safety:** Song imports place only wanted tracks; nothing else from the download is filed. All MUS-33 rules apply.
  - **Migrations:** One, next free: `music_song_wants(id, recording_id REFERENCES music_recordings(id), request_id, added_at)`.
  - **Tests:** Go: request create/approve/decline for both kinds; quota counting; song-want search target selection; singleton import of one wanted track from a full-album download; ready waits for Plex; shapeRequests hides staff fields. Web/e2e: request flow with fixtures.
  - **Depends on:** [MUS-39](#mus-39), [MUS-41](#mus-41), [MUS-31](#mus-31)
  - **Risk:** Songs that exist only on huge compilations; the target-selection rule prefers the smallest official release containing the recording, and staff can pick another in the request.
  - **Resolves:** 
<a id="mus-41"></a>
- [ ] **MUS-41 · Plex music scans after every music change, and Plex-aware "ready"** — `P1` · `M` · Phase 15 · R4
  - **Problem:** internal/plexscan knows only movie and show sections, so imports, renames, retags and deletes in Music never reach Plex until its own periodic scan, and Insights can't locate an album for a "ready" notice or a Watch/Listen link.
  - **Approach:** 1. plexscan gains `KindMusic` (Plex section type `artist`): map the music root to its Plex section(s) the way movie/show folders are mapped, and request a partial scan of the album folder after an import, rename (old and new folders), retag or delete. Through the outbox, so a restart doesn't lose it.
    2. Insights Locate for albums and tracks (by folder path, then by title and artist), PlexIndexReady for music, and a Plexamp/Plex Web link builder.
    3. If a Jellyfin server is connected, the same events refresh its music library (jellyfinsvc already scans).
    4. Settings → Plex shows the music section mapping with the other libraries.
  - **Files:** `internal/plexscan/mapping.go`, `internal/plexscan/scanner.go`, `internal/plexscan/*_test.go`, `internal/insights/` (Locate), `internal/jellyfinsvc/scan.go`, `internal/automation/music.go`, `cmd/arrmada/outbox.go`, `web/src/pages/settings/` (Plex page)
  - **Acceptance:**
    - An imported album appears in Plex/Plexamp within a minute without a full library scan.
    - Renames and retags are picked up the same way.
    - A music request turns ready only once Plex lists the album or track.
  - **Data safety:** No files touched; Plex is only asked to scan.
  - **Migrations:** None.
  - **Tests:** Go: section mapping for a music root; partial-scan requests per event (old and new folders on rename); outbox delivery; Locate by path and by title; Jellyfin refresh call with a fake server.
  - **Depends on:** [MUS-29](#mus-29)
  - **Risk:** Plex's music agent re-matches online and may show its own album names; tags and folders are still correct for other players. Document the recommended Plex music library settings ("Prefer local metadata").
  - **Resolves:** 
<a id="mus-22"></a>
- [ ] **MUS-22 · Artwork-first music library: cover grid, album shelves on the artist page, sorting and a type filter** — `P3` · `M` · Phase 15 · R4
  - **Rebuild note (2026-10-19):** Music already uses the shared LibraryGrid / LibraryToolbar (Phase 10), so this task is the artwork: grid tiles and artist-page shelves use art served by Arrmada from [MUS-23](#mus-23)'s local image cache, not hotlinks to coverartarchive.org. Shelves follow [MUS-14](#mus-14)'s primary and secondary types, plus an 'Appears on' shelf for credited albums ([MUS-29](#mus-29)).
  - **Problem:** Artist cards are text plus a progress bar (web/src/pages/Music.tsx:309-346). Album rows on the artist page are plain text (ArtistDetail.tsx:192-198), even though every album stores a Cover Art Archive URL (metadata/musicbrainz.go:204). The only artwork is a 160 px cover on the album page. ListArtists is fixed newest-first (internal/music/repo.go:39) with no sort control. Hundreds of artists are hard to browse, and the pages feel nothing like Movies and Series.
  - **Approach:** 1. Backend:
       - ListArtists(ctx, sort) accepts name|added|completeness; name orders by COALESCE(NULLIF(sort_name,''), name) COLLATE NOCASE.
       - Each artist row returns cover_url from its newest album with a cover, via one correlated subquery.
       - GET /api/v1/music/artists?sort=.
    2. Music.tsx: a grid of square tiles with:
       - the cover (front-250 derived from the stored front-500 URL), falling back to an initials tile
       - the name, '3/20 albums' and a thin completeness bar (stats from [MUS-10](#mus-10))
       - a sort select (remembered in localStorage, wrapped in try/catch) and a type filter
       It keeps the warm dark palette, terracotta accent and current type scale, and has no horizontal scroll at 375 px.
    3. ArtistDetail.tsx:
       - shelves for Albums, EPs, Singles, and Live & other (by album_type)
       - each shelf is a row of cover tiles: cover, title, year, completeness ring, monitor toggle on hover or focus
       - a list-view toggle for long discographies
    4. AlbumDetail: a 240 px cover. All thumbnails use loading="lazy" and an onError fallback.
  - **Files:** `internal/music/repo.go`, `internal/music/service.go`, `internal/httpapi/music.go`, `web/src/pages/Music.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/pages/AlbumDetail.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - The music library reads like the poster grids in Movies and Series.
    - Alphabetical sort works across hundreds of artists.
    - At phone width there is no horizontal scroll.
    - Broken covers fall back to initials.
  - **Data safety:** Read-only.
  - **Migrations:** None.
  - **Tests:** Go (repo): each sort order, and cover_url comes from the newest album that has one.; UI (manual): desktop and 375 px width; a cover 404 shows the initials fallback; shelves group by album_type.
  - **Depends on:** [MUS-10](#mus-10), FE-xx (shared card/grid components); soft
  - **Risk:** Cover Art Archive has gaps, which the fallback covers. Large libraries load many images, so thumbnails are lazy-loaded. Covers are hotlinked from coverartarchive.org, the same pattern as TMDB posters.
  - **Resolves:** music-13
<a id="mus-23"></a>
- [ ] **MUS-23 · Album art from Cover Art Archive then Fanart.tv, artist images from Fanart.tv (key entered in the app), keyless Wikipedia bios, all cached locally** — `P3` · `M` · Phase 15 · R4
  - **Rebuild note (2026-10-19):** The owner's source order (2026-10-19): album art from Cover Art Archive first, then Fanart.tv; artist images and backgrounds from Fanart.tv; bios from MusicBrainz url-rels → Wikidata → the Wikipedia summary (keyless). **TheAudioDB is dropped.** The Fanart.tv key is entered in Settings → Connections (an apikeys Hidden entry, kept out of logs and the support bundle); without it, artist images are skipped and album art comes from CAA only. Every image is downloaded once into an on-disk image cache in the app's data folder and served by Arrmada (the audiobook server's image-cache pattern), so a CAA or Internet Archive outage doesn't blank the library. Responses are cached in [MUS-26](#mus-26)'s cache, not metadata_cache. [MUS-33](#mus-33) reuses the cached front cover for embedding and cover.jpg.
  - **Problem:** Artist.ImageURL and Overview are never set. AddArtist omits ImageURL, Refresh writes back the existing empty value (internal/music/service.go:84-87, 152-153), and the MusicBrainz GetArtist mapping has no bio (metadata/musicbrainz.go:163-166). Nothing renders them anyway.
  - **Approach:** 1. metadata: an ArtistArt provider interface with two implementations:
       - fanart.tv /v3/music/{mbid}: artistthumb, artistbackground
       - TheAudioDB artist-mb.php?i={mbid}: strArtistThumb, strArtistFanart, strBiographyEN
       Keys:
       - Entered in the app UI only (the Connections / metadata-keys card INT provides, with a Test button), stored in settings.
       - Never committed, never logged (redact them in request logging), and never returned by the API (GET returns {configured: true}).
    2. Bio fallback with no key: MusicBrainz url-rels (inc=url-rels) → Wikidata → Wikipedia REST summary.
    3. Cache every response in metadata_cache (migration 0080) for 30 days.
    4. Migration (next free number, tentatively 00NN_artist_banner.sql): artists.banner_url TEXT NOT NULL DEFAULT ''.
    5. AddArtist and Refresh write image_url, banner_url and overview (repo.UpdateArtistMeta gains banner_url). Refresh no longer passes back the old a.ImageURL.
    6. ArtistDetail: a hero banner that fades into the page background via the existing CSS tokens, and a bio clamped to 4 lines with 'more'. [MUS-22](#mus-22)'s grid tiles prefer the artist image over the album cover.
  - **Files:** `internal/metadata/musicbrainz.go`, `internal/metadata/provider.go`, `internal/metadata/artistart.go`, `internal/music/service.go`, `internal/music/repo.go`, `internal/store/migrations/00NN_artist_banner.sql`, `internal/httpapi/settings.go`, `web/src/pages/ArtistDetail.tsx`, `web/src/pages/Music.tsx`, `web/src/pages/Settings.tsx`
  - **Acceptance:**
    - Artists with fanart get a hero image and a bio.
    - With no key configured, artists that have a Wikipedia link still get a bio.
    - Keys never appear in logs or API responses.
  - **Data safety:** Art files live only in Arrmada's cache; nothing is written into library folders here (cover.jpg is MUS-33's, through staging).
  - **Migrations:** One, next free: `artists.banner_url` (as specified).
  - **Tests:** Go: provider parse tests with canned JSON for fanart.tv, TheAudioDB and the Wikipedia summary.; Go: Refresh writes image_url, banner_url and overview.; Go: with no key set, no fanart.tv or TheAudioDB calls are made and the Wikipedia fallback is used.; Go (httpapi): the settings GET masks the keys.
  - **Depends on:** [MUS-22](#mus-22), INT-xx (Connections / metadata-key card pattern), SEC-xx (secret masking and log redaction); soft
  - **Risk:** Third-party rate limits and terms of use; caching for 30 days keeps calls low. Hotlinked images can disappear, so the initials and cover fallbacks stay.
  - **Resolves:** music-13

#### Milestone: R5: Beyond Lidarr

_Gaps in MusicBrainz are filled from Deezer (default) and Discogs (opt-in) and re-linked when MusicBrainz catches up. Synced lyrics arrive from LRCLIB. Soundtracks of the films and shows already in the library, similar artists and listening-based suggestions are offered for approval. Playlists can be imported. ReplayGain is written. Estimate: 4–5 weeks; tasks are independent and can be reordered._

<a id="mus-42"></a>
- [ ] **MUS-42 · Gap-fill from Deezer (on by default, no key): releases MusicBrainz doesn't have, marked "not on MusicBrainz"** — `P2` · `M` · Phase 15 · R5
  - **Problem:** MusicBrainz is incomplete for some regions, genres and new releases, and Lidarr makes a missing release a hard block. The owner chose Deezer as the default, keyless fallback (2026-10-19).
  - **Approach:** 1. A Deezer metadata client (search, artist albums, album with tracks, ISRC and UPC), its own limiter (well under 50 requests per 5 s), responses cached in the same permanent cache (source-prefixed keys). Setting `music_deezer` default on.
    2. **Artist discography gaps:** on refresh, Deezer albums of an artist (found by the MusicBrainz artist's Deezer URL relation, else by exact name + one confirming album) that don't map to any MusicBrainz release group (by UPC, then by fold-equal title and year ±1) are offered as "not on MusicBrainz" albums: stored with `source = 'deezer'`, `albums.mbid = 'deezer:<id>'`, unmonitored by default.
    3. **Matcher candidates:** Deezer releases compete with `data_source` penalty 0.5 × weight 2.0 (beets' rule).
    4. **Tags:** not-on-MusicBrainz releases get every house field except MUSICBRAINZ_*; ISRC and BARCODE come from Deezer.
    5. Labels: "not on MusicBrainz" on cards, pages and Reviews; a link to add it to MusicBrainz.
    6. Deezer is metadata only. Arrmada never downloads audio from Deezer.
  - **Files:** `internal/metadata/deezer.go`, `internal/metadata/deezer_test.go`, `internal/music/gapfill.go`, `internal/music/match/candidates.go`, `internal/music/housetags.go`, `web/src/pages/ArtistDetail.tsx`, `web/src/pages/AlbumDetail.tsx`
  - **Acceptance:**
    - An artist's release missing from MusicBrainz but on Deezer appears, labelled, and can be monitored, grabbed and imported.
    - Turning Deezer off hides gap-fill albums from new suggestions and removes Deezer candidates; nothing already imported is touched.
  - **Data safety:** Metadata only; turning the source off never removes albums or files. Tags never carry fake MBIDs.
  - **Migrations:** None (MUS-28's `source` columns and external ids).
  - **Tests:** Go: Deezer parse (canned JSON); UPC/title mapping; the data_source penalty in a matcher decision; tags without MUSICBRAINZ_*; off switch.
  - **Depends on:** [MUS-28](#mus-28), [MUS-31](#mus-31), [MUS-33](#mus-33)
  - **Risk:** Deezer's keyless API terms and limits are informal and can change; it's a fallback, so losing it loses gap-fill, never data.
  - **Resolves:** 
<a id="mus-43"></a>
- [ ] **MUS-43 · Discogs gap-fill, opt-in with the user's own token** — `P3` · `S` · Phase 15 · R5
  - **Problem:** Discogs is strongest where MusicBrainz and Deezer are weakest (vinyl, electronic, underground), but needs an account token and allows 60 requests a minute.
  - **Approach:** The MUS-42 framework with a Discogs client (masters and releases, barcodes, catalog numbers), its own 1-a-second limiter, cache, and the token entered in Settings → Connections (apikeys Hidden entry, Test button, kept out of logs and the support bundle). Off unless a token is set. Ids stored as `discogs_master` / `discogs_release`.
  - **Files:** `internal/metadata/discogs.go`, `internal/metadata/discogs_test.go`, `internal/music/gapfill.go`, `web/src/components/connections/` (Discogs card)
  - **Acceptance:** With a token, a vinyl-only release missing elsewhere appears labelled "not on MusicBrainz (Discogs)"; without one, Discogs is never called. The token never appears in logs or API responses.
  - **Data safety:** As MUS-42.
  - **Migrations:** None.
  - **Tests:** Go: parse; limiter; token redaction; off without a token.
  - **Depends on:** [MUS-42](#mus-42)
  - **Risk:** Discogs data quality varies; candidates carry the source penalty.
  - **Resolves:** 
<a id="mus-44"></a>
- [ ] **MUS-44 · Re-link gap-filled albums to MusicBrainz once they appear there** — `P2` · `M` · Phase 15 · R5
  - **Problem:** A gap-filled album should become a normal MusicBrainz album as soon as someone adds it, without the owner doing anything and without touching files.
  - **Approach:** The Books metadata-source upgrade pattern (internal/books/upgrade.go): a periodic pass over `source != 'musicbrainz'` albums looks for a MusicBrainz release by UPC, then by artist + fold-equal title + track count; on a single confident hit it re-keys the album to the MusicBrainz release group (`albums.mbid`, editions, recordings by ISRC/position), keeps monitoring, profile, files and requests, writes an event, and queues a retag (MUS-33) so the files gain MBIDs. Two hits or a doubtful one → flagged for the owner, never merged automatically.
  - **Files:** `internal/music/relink.go`, `internal/music/relink_test.go`, `internal/automation/music.go`
  - **Acceptance:** A Deezer-only album that is later added to MusicBrainz becomes a MusicBrainz album within a day, with its files, monitoring and requests intact and MBIDs in its tags.
  - **Data safety:** Re-keying changes DB rows only; files are retagged only through MUS-33's staging rules; ambiguous cases are never merged.
  - **Migrations:** None.
  - **Tests:** Go: UPC match, title match, two-hit ambiguity flagged, files and monitoring kept.
  - **Depends on:** [MUS-42](#mus-42), [MUS-33](#mus-33)
  - **Risk:** Low with the single-hit rule.
  - **Resolves:** 
<a id="mus-45"></a>
- [ ] **MUS-45 · Synced lyrics from LRCLIB as .lrc sidecars** — `P3` · `M` · Phase 15 · R5
  - **Problem:** Plexamp, Jellyfin and Navidrome show synced lyrics from `.lrc` files beside the tracks, and Arrmada fetches none. LRCLIB is a free, keyless lyrics database.
  - **Approach:** A `fetch-lyrics` job (daily, capped, a setting to turn it off) looks up tracks without a `.lrc` by artist, title, album and duration (LRCLIB's get endpoint), prefers synced lyrics, and writes `<track base name>.lrc` via staging (a new file only; never overwrites an existing `.lrc`). Misses are remembered with a backoff. Renames (MUS-34) move the `.lrc` with its track.
  - **Files:** `internal/lyrics/lrclib.go`, `internal/lyrics/lrclib_test.go`, `internal/automation/music_lyrics.go`, `cmd/arrmada/main.go`
  - **Acceptance:** Albums gain synced lyrics that Plexamp shows; an existing `.lrc` is never replaced.
  - **Data safety:** Only creates new sidecar files; never overwrites; nothing written into audio files.
  - **Migrations:** One, next free: `music_lyrics_attempts(release_track_id, tried_at, misses)` for the backoff.
  - **Tests:** Go: client parse; duration tolerance; never-overwrite; backoff; rename carries the `.lrc`.
  - **Depends on:** [MUS-29](#mus-29), [MUS-34](#mus-34)
  - **Risk:** Lyrics licensing is LRCLIB's; Arrmada only fetches for files the user has.
  - **Resolves:** 
<a id="mus-46"></a>
- [ ] **MUS-46 · Soundtracks for the films and shows already in the library** — `P3` · `M` · Phase 15 · R5
  - **Problem:** Arrmada already knows every film and show in the household's library; Lidarr needs a plugin to suggest their soundtracks.
  - **Approach:** Find soundtrack release groups (MusicBrainz secondary type Soundtrack) for library titles: by the film's MusicBrainz relationship or Wikidata id when present, else by title + year with a strict check. Show them as an approval list (Music → Suggestions): approve adds and monitors the album (no artist follow needed: it files under its credited artist or Various Artists); dismiss remembers.
  - **Files:** `internal/music/suggest/soundtracks.go`, `internal/music/suggest/*_test.go`, `internal/httpapi/music_suggest.go`, `web/src/pages/` (Suggestions tab)
  - **Acceptance:** Films in the library show their soundtrack albums as suggestions; nothing is monitored until approved.
  - **Data safety:** Nothing is added or downloaded without approval.
  - **Migrations:** One, next free: `music_suggestions(id, kind, album_mbid, reason, state, created_at)` shared by MUS-46..48.
  - **Tests:** Go: matching by relationship and by title+year; dismiss is remembered; approve monitors.
  - **Depends on:** [MUS-28](#mus-28)
  - **Risk:** Same-named films; the strict check and the approval step cover it.
  - **Resolves:** 
<a id="mus-47"></a>
- [ ] **MUS-47 · Similar artists, from ListenBrainz (keyless) or Last.fm (the user's key)** — `P3` · `M` · Phase 15 · R5
  - **Problem:** Discovery of new artists is a top reason people bolt tools onto Lidarr.
  - **Approach:** Similar-artist lookups by MBID from ListenBrainz's public endpoints (no key) and, when the user enters a Last.fm key in Connections, Last.fm (with its required attribution). Shown on artist pages ("Similar") and in Discover, and as suggestions (MUS-46's table) for artists the household plays a lot.
  - **Files:** `internal/metadata/listenbrainz.go`, `internal/metadata/lastfm.go`, `internal/music/suggest/similar.go`, `web/src/pages/ArtistDetail.tsx`, `web/src/features/discover/music/*`
  - **Acceptance:** Artist pages show similar artists; with no Last.fm key nothing calls Last.fm; the key never appears in logs.
  - **Data safety:** Read-only; suggestions need approval.
  - **Migrations:** None (MUS-46's table).
  - **Tests:** Go: parse both sources; key redaction; off without a key.
  - **Depends on:** [MUS-46](#mus-46)
  - **Risk:** Third-party limits; cached.
  - **Resolves:** 
<a id="mus-48"></a>
- [ ] **MUS-48 · Listening-based suggestions with an approval queue** — `P3` · `M` · Phase 15 · R5
  - **Problem:** Insights already records the household's Plex music plays. Suggestions grounded in what people actually play are more useful than generic charts, but nothing should be fetched without someone saying yes.
  - **Approach:** A weekly pass: take the most-played artists of the last 90 days (household-wide), find their missing albums and similar artists (MUS-47), and add suggestions with a reason ("you played Portishead 40 times this month"). Staff approve or dismiss; requesters may see their *own* "for you" row in Discover from their own plays only. No list of who played what is shown to anyone else.
  - **Files:** `internal/music/suggest/listening.go`, `internal/insights/` (a read helper), `web/src/pages/` (Suggestions tab), `web/src/features/discover/music/*`
  - **Acceptance:** Suggestions appear with plain reasons; approving monitors and searches; requesters see only suggestions from their own listening.
  - **Data safety:** Nothing is added without approval; per-user listening is never shown to other users.
  - **Migrations:** None (MUS-46's table).
  - **Tests:** Go: ranking from synthetic plays; per-user scoping (a requester never receives another user's basis); approve flow.
  - **Depends on:** [MUS-46](#mus-46), [MUS-47](#mus-47)
  - **Risk:** Privacy: keep reasons household-level for staff and personal only for the person themselves.
  - **Resolves:** 
<a id="mus-49"></a>
- [ ] **MUS-49 · Playlist import from CSV, JSPF and M3U** — `P3` · `M` · Phase 15 · R5
  - **Problem:** People move to Arrmada with playlists from other services and servers, and want the music in them.
  - **Approach:** Upload a CSV (artist, title, album optional), a JSPF (ListenBrainz/XSPF JSON) or an M3U with tags; each row is resolved to a recording (MBID if present, else search with the singleton matcher's thresholds); the result is a review list: matched (in library / wanted / not yet), ambiguous, not found. Approve turns matched rows into song wants (MUS-40's table). Spotify is not used (its developer access is closed to new apps in practice).
  - **Files:** `internal/music/playlist/` (parsers, resolver), `internal/httpapi/music_playlist.go`, `web/src/pages/` (Import playlist)
  - **Acceptance:** A 50-row CSV resolves with clear states and becomes song wants on approval.
  - **Data safety:** Read-only until approved; nothing grabbed before.
  - **Migrations:** None (MUS-40's table).
  - **Tests:** Go: parsers (CSV quirks, JSPF, extended M3U); resolution states.
  - **Depends on:** [MUS-40](#mus-40)
  - **Risk:** Large playlists cost many MusicBrainz searches; resolved in the background lane with progress.
  - **Resolves:** 
<a id="mus-50"></a>
- [ ] **MUS-50 · ReplayGain track and album tags** — `P3` · `S` · Phase 15 · R5
  - **Problem:** Players normalise volume from ReplayGain tags; the house style leaves them for later.
  - **Approach:** In staging (new imports) and as an album action (existing albums, through MUS-33's retag swap): measure each track and the album with ffmpeg's EBU R128 analysis (`ebur128` filter) through toolrun, convert to ReplayGain 2.0 values (−18 LUFS reference), and write REPLAYGAIN_TRACK_GAIN/_PEAK and REPLAYGAIN_ALBUM_GAIN/_PEAK in each format's house mapping.
  - **Files:** `internal/music/stage/replaygain.go`, `internal/music/stage/replaygain_test.go`, `internal/music/housetags.go`
  - **Acceptance:** Newly imported albums carry ReplayGain tags that Plexamp and Navidrome honour.
  - **Data safety:** Only through staging; existing files only by the retag swap with originals in the bin.
  - **Migrations:** None.
  - **Tests:** Go: parsing ebur128 output and the gain arithmetic; env-gated measurement of a generated tone.
  - **Depends on:** [MUS-33](#mus-33)
  - **Risk:** Album gain needs the whole album analysed; partial imports get track gain only until complete.
  - **Resolves:** 

#### Milestone: Superseded by the rebuild

_These Lidarr-lite tasks are replaced by the new model. Their original specs are kept for reference; build the replacement instead._

<a id="mus-10"></a>
- [ ] ~~**MUS-10 · Count completeness by album, and show real stats on the artist page**~~ — **Superseded (2026-10-19)** by [MUS-29](#mus-29) (completeness from the target edition, with the same album counts). Original: `P1` · `S` · Phase 15
  - **Problem:** artistStats LEFT JOINs tracks (internal/music/repo.go:71-96), so albums whose listing hasn't been fetched add nothing. Music.tsx then marks an artist Complete when have >= total (:20-22). A scanned artist with 1 of 20 albums shows '12/12' and is listed under Complete.

Service.GetArtist (service.go:36-47) never fills Stats, so the artist page always shows '0 albums · 0/0 tracks' (ArtistDetail.tsx:93). As a knock-on, mostlyMissing (:72) offers 'Grab discography' for every artist with more than one album, however much of it the user owns.
  - **Approach:** 1. repo.artistStats: aggregate per album first (track count, have count, monitored) in a subquery, then per artist. New ArtistStats fields (JSON snake_case), alongside the existing Albums, Tracks, HaveTracks and SizeBytes:
       - AlbumsMonitored
       - AlbumsComplete (track_count > 0 && have >= count)
       - AlbumsUnlisted (no tracks)
       - AlbumsWithFiles (have > 0)
    2. Factor the per-album aggregation into statsFromAlbums([]Album) so Service.GetArtist fills Stats from the albums it already loads, with no extra query.
    3. Music.tsx:
       - isComplete = albums_monitored > 0 && albums_complete == albums_monitored && albums_unlisted == 0 among monitored albums.
       - Cards show '3/20 albums' first, tracks second.
       - The Incomplete filter uses the new rule; unlisted albums count as unknown, never complete.
       - Filter chips show counts.
    4. ArtistDetail.tsx: the header shows real album and track counts. mostlyMissing = albums_with_files / albums < 0.25.
    5. Listings for unlisted monitored albums are fetched in the background by [MUS-13](#mus-13).
  - **Files:** `internal/music/repo.go`, `internal/music/service.go`, `internal/music/music.go`, `internal/music/repo_test.go`, `web/src/pages/Music.tsx`, `web/src/pages/ArtistDetail.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - A scanned artist with 1 of 20 albums shows '1/20 albums' and appears under Incomplete.
    - The artist page header shows the real album and track counts.
    - 'Grab discography' is hidden for artists who own at least 25% of their albums.
  - **Tests:** Go (repo): an artist with one complete album and one unlisted album is not complete and reports AlbumsUnlisted=1 and AlbumsComplete=1.; Go: GetArtist returns non-nil Stats that equal the ListArtists stats for the same artist.; UI (manual, test library in a temp dir): filter counts and the artist header.
  - **Depends on:** [MUS-03](#mus-03) (decided Rebuild)
  - **Risk:** Low. Until MUS-13 fills listings, many scanned artists show 'unknown' albums. That is honest, but the Incomplete list will look long at first.
  - **Resolves:** music-10
<a id="mus-18"></a>
- [ ] ~~**MUS-18 · Music Wanted view: every missing album, its last outcome, next try and Search now**~~ — **Superseded (2026-10-19)** by ACQ-18 (shipped: Activity → Wanted lists missing albums with outcome, next try and Search now) and [MUS-09](#mus-09) (the album page). Original: `P2` · `S` · Phase 15
  - **Problem:** Even with per-album outcomes (MUS-09), the owner has to open artist pages one by one to see what's missing and why. The audit's Phase 2 calls for a Music → Wanted view listing every missing album with when it was last searched and why nothing was grabbed. Movies and Series have the same gap; this follows the shared 'why isn't this downloading' pattern.
  - **Approach:** 1. Repo: extend [MUS-02](#mus-02)'s WantedAlbums with paging and an outcome filter: WantedAlbums(ctx, WantedQuery{Outcome string; Limit, Offset int}) ([]WantedAlbum, total int, countsByOutcome map[string]int, error). Each row has album fields, artist name, cover_url, track_count, have_tracks, last_search_at, search_misses, outcome and detail.
    2. GET /api/v1/music/wanted?outcome=&page= (musicRoute; RoleManager, since it drives searches) returns {albums:[…, next_search_at, downloading], total, counts}. downloading comes from one queue read and albumDownloading.
    3. POST /api/v1/music/sweep (RoleManager, musicRoute) runs SearchMusicMissing once in the background, guarded by an atomic.Bool like musicScan, and returns 202. It respects the waits and the 25 cap.
    4. Music.tsx:
       - an 'Artists | Wanted (n)' tab switch
       - Wanted rows: small cover, 'Artist: Album (year)', '3/12 tracks', outcome line, 'next try in ~6 h', Search now ([MUS-09](#mus-09) endpoint)
       - outcome filter chips with counts (No match / Below profile / No listing / Nothing found / Downloading)
       - a 'Run search sweep now' button
       Existing list and chip styles are kept.
  - **Files:** `internal/music/repo.go`, `internal/music/music.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `web/src/pages/Music.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - The Wanted tab lists every monitored missing album with its last outcome and next automatic search, filterable by outcome.
    - Search now on a row updates that row without a reload.
    - 'Run search sweep now' never exceeds the 25-album cap or ignores backoff.
  - **Tests:** Go (repo): WantedAlbums paging, the outcome filter and counts; complete and unmonitored albums are excluded.; Go (httpapi): /music/wanted returns 404 when the module is off; a second POST /music/sweep while one is running returns 409.; UI (manual): filter chips and Search now on a row.
  - **Depends on:** [MUS-09](#mus-09)
  - **Risk:** Low. A large library makes the list long, hence paging.
  - **Resolves:** music-9
<a id="mus-19"></a>
- [ ] ~~**MUS-19 · Pick the MusicBrainz edition that matches the download's track count**~~ — **Superseded (2026-10-19)** by [MUS-30](#mus-30) (every edition stored, a policy, equivalence and switching) and [MUS-31](#mus-31) (the matcher picks the edition a download fits). Original: `P2` · `M` · Phase 15
  - **Problem:** AlbumTracks always takes the earliest official release's listing (internal/metadata/musicbrainz.go:236-245). A download of a different edition (deluxe, regional, bonus track) leaves files unmatched or tracks missing forever. The album never completes, keeps being re-searched, and feeds the partial-album problems.
  - **Approach:** 1. metadata:
       - AlbumReleases(ctx, rgMBID) → []ReleaseSummary{ID, Date, Country, Status, TrackCount, Discs}, from one browse call /release?release-group=…&inc=media&limit=100
       - ReleaseTracks(ctx, releaseID)
       Both are added to the MusicProvider interface; fakeMusicProvider implements them.
    2. Migration (next free number, tentatively 00NN_album_edition.sql): albums.mb_release_id TEXT NOT NULL DEFAULT ''. EnsureTracks records the release it picked.
    3. music.Service.RelistAlbum(ctx, album, releaseID), in one transaction:
       - replace the listing
       - keep every row that has a file, re-keyed by disc/track or else by NormKey title
       - delete only file-less rows that aren't in the new listing
       - store mb_release_id
    4. importAlbumContent: when there are audio files and either some are unmatched or len(files) != TrackCount, call AlbumReleases. If an official release has TrackCount == len(files) (ties → earliest) and differs from the current mb_release_id:
       - RelistAlbum
       - re-run MatchTracks
       - write the event 'Switched "X" to the 15-track 2003 edition to match the download'
       This runs at most once per download hash.
    5. AlbumDetail: an 'Edition' select (from AlbumReleases) backed by PUT /api/v1/music/albums/{id}/edition (RoleManager, musicRoute).
  - **Files:** `internal/metadata/musicbrainz.go`, `internal/metadata/provider.go`, `internal/store/migrations/00NN_album_edition.sql`, `internal/music/service.go`, `internal/music/repo.go`, `internal/automation/music.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `web/src/pages/AlbumDetail.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - A deluxe-edition download completes the album instead of leaving bonus tracks unmatched or missing forever.
    - The owner can switch an album's edition by hand, and tracks with files are never lost.
  - **Tests:** Go (fake provider with a 12-track standard and a 15-track deluxe edition): a 15-file download relists to deluxe and places all 15.; Go: a held file on a row absent from the new listing is preserved.; Go (httpapi): a manual edition switch returns the new listing; an unknown release id → 400.; Go (metadata, canned JSON): AlbumReleases parses track counts from media.
  - **Depends on:** [MUS-06](#mus-06), [MUS-08](#mus-08)
  - **Risk:** Relisting changes which tracks count as missing. Rows with files are never deleted, so a relist may leave the album with more rows than the edition has; the UI shows these as 'extra'. Each mismatch costs up to two MusicBrainz calls, and only on import.
  - **Resolves:** music-5
<a id="mus-20"></a>
- [ ] ~~**MUS-20 · Scan results the owner can see and act on: unmatched folders with reasons, and Match…**~~ — **Superseded (2026-10-19)** by [MUS-37](#mus-37) (scan v2: unmatched folders become Reviews items with Match…) and [MUS-31](#mus-31). Original: `P2` · `M` · Phase 15
  - **Problem:** The scan runs in a goroutine and only logs and publishes counts (internal/httpapi/music.go:220-242). MusicScanResult.Unmatched is thrown away apart from len(), and no music page listens for live events. The UI just toasts 'this runs in the background' (Music.tsx:136-151). After a scan the owner can't tell what was imported or why half the collection didn't match.
  - **Approach:** 1. MusicScanResult.Unmatched becomes []UnmatchedFolder{Path, Artist, Album, Reason, Detail}. scanResolveArtist and ScanMusicLibrary return the reason instead of a bool:
       - artist_not_found
       - artist_ambiguous ('2 MusicBrainz artists are named Nirvana')
       - album_not_in_catalogue
       - no_track_matched
    2. Migration (next free number, tentatively 00NN_music_scan_results.sql): music_scan_unmatched(path TEXT PRIMARY KEY, artist TEXT, album TEXT, reason TEXT, detail TEXT, seen_at TIMESTAMP). The last-scan summary (counts, started_at, finished_at) goes in settings key music_last_scan as JSON. Each scan replaces the table contents.
    3. Persistence lives in automation via music.Repo (SaveScanResult, ScanUnmatched(limit, offset), DeleteScanUnmatched), not in the HTTP goroutine.
    4. New endpoints (RoleManager, musicRoute):
       - GET /api/v1/music/scan returns {running, last, unmatched, total}.
       - POST /api/v1/music/scan/match {path, album_id} runs recordAlbumFiles for that folder against that album (MatchTracks still decides positions), then removes the row.
       - POST /api/v1/music/scan/match {path, artist_mbid} adds an ambiguous artist explicitly (unmonitored) and returns their albums to choose from.
    5. UI on Music.tsx:
       - 'Scan library' shows 'Scanning…' and listens for 'library.scanned' via useLive.
       - A results drawer shows the counts and the unmatched list grouped by reason, paginated.
       - Each folder has 'Match…': search the library or MusicBrainz for the artist, then pick the album.
  - **Files:** `internal/automation/music_scan.go`, `internal/store/migrations/00NN_music_scan_results.sql`, `internal/music/repo.go`, `internal/httpapi/music.go`, `internal/httpapi/server.go`, `web/src/pages/Music.tsx`, `web/src/lib/api.ts`
  - **Acceptance:**
    - After a scan the owner sees how many artists, albums and tracks were imported, plus every refused folder with a reason.
    - An unmatched folder can be fixed through Match… without renaming anything on disk.
  - **Tests:** Go (temp-dir fixture, fakeMusicProvider): a scan produces each reason code.; Go (httpapi): the match endpoint records the folder's files against the chosen album and clears the row; module off → 404.; UI (manual): the drawer lists unmatched folders and Match… completes.
  - **Depends on:** [MUS-15](#mus-15), [MUS-04](#mus-04), BE-xx (job runner for the scan goroutine); soft
  - **Risk:** Big libraries produce long unmatched lists, hence pagination. Manual matching still goes through MatchTracks, which never guesses track positions.
  - **Resolves:** music-14

#### Risks

- **Size.** The rebuild is months of work competing with other epics. R1 alone fixes real wrong-file bugs and is worth shipping even if later phases slip. R2 is the core of "better than Lidarr" and must not be cut short: R4 on the old model would recreate Lidarr's edition and compilation walls.
- **The migration (MUS-28).** It rewrites how every music row is stored. Mitigations: additive only, BE's snapshot, the migration's own count check, a fixture test, the frozen `tracks` table, and merging with MUS-29 so there's no window where two models diverge.
- **Matcher thresholds.** Too loose places wrong files; too strict floods Reviews. Defaults are conservative for unexpected files, settings expose them, and MUS-12's soak tunes them.
- **Disk while seeding.** Copies for retagged seeding files double their disk use until seeding ends. The disk guard counts it; the owner chose this knowingly ("see if it becomes an issue").
- **MusicBrainz at 1/s.** First loads of big libraries take hours. The permanent cache, the two lanes and per-run caps keep it bounded; a self-hosted mirror removes the limit for owners who want one.
- **Licence.** go-taglib is LGPL-2.1 inside an MIT app: fine with notices and the swap path, but the owner signs it off (MUS-27), with a permissive fallback ready.
- **Third-party sources.** Deezer, Discogs, AcoustID, Fanart.tv, LRCLIB, ListenBrainz and Last.fm all have limits or terms. Every one is a fallback or an extra, cached, keyed by the user where a key is needed, and switchable off without losing data.
- **Plex re-matches online.** Plex may show its own names whatever the tags say. Tags still serve Jellyfin, Navidrome and every future re-match; MUS-41 documents the Plex settings that help.
- **Privacy.** Listening-based suggestions (MUS-48) must never show one person's plays to another.

#### Out of scope

- **Soulseek, YouTube and streaming-rip sources** (Deezer, Qobuz, Tidal, Spotify or Apple Music rippers, Lucida, DABmusic). Music comes from Usenet and torrents only. Deezer is used for metadata, never audio.
- **A central Arrmada-hosted metadata server.** Each install talks to MusicBrainz or its owner's mirror.
- **A Subsonic/OpenSubsonic server or an in-Arrmada music player.** Plex/Plexamp is the player; revisit when the household asks.
- **Spotify as a dependency** (import lists or recommendations): its developer access is closed to new apps in practice.
- **Audio transcoding of the music library** (FLAC → Opus copies for phones). A later idea; Convert stays video-only.
- **Keeping two qualities of one track side by side.** Upgrades replace, with the old file in the recycle bin.
- **Dropping the old `tracks` table.** It stays frozen.

#### Open questions for the owner

1. **Tag library licence (MUS-27):** OK to ship go-taglib (LGPL-2.1, with TagLib LGPL-2.1/MPL-1.1) inside the MIT app, with a notices file? If not, the permissive fallback covers FLAC and MP3 first and defers MP4.
2. **Write tags by default (MUS-33):** default on, with a setting to keep files exactly as downloaded (hardlinks, no retag)? Default on is assumed.
3. **Cue-sheet albums (MUS-35):** split images into per-track FLAC (Plex-first, assumed) rather than keep image + cue?
4. **Edition policy default (MUS-30):** "original" (earliest acceptable release, prefer Digital Media then CD) is assumed. Any preferred countries to seed the `preferred` policy with?
5. **AcoustID and Fanart.tv keys:** both need a key, and keys can't live in code. Is it fine that each owner registers their own (free) and enters it in Connections, with those features off until then?
6. **Song requests (MUS-40):** when only one song is wanted, import just that track from an album download (assumed), or the whole album?
7. **Music auto-approve (MUS-40):** off by default for everyone, like other new kinds?
8. **Evidence for the record:** the usage numbers MUS-03 originally asked for (90-day Plex music plays and listeners, library counts) weren't needed for the decision. Record them anyway as a baseline for MUS-12's soak?
9. **Graduation default (MUS-12):** after graduation, should Music stay off by default on fresh installs (assumed) or turn on?

