# Decision: Music is rebuilt, to be better than Lidarr

_Decided by the owner on 2026-10-19. Closes [MUS-03](../roadmap/MUS.md#mus-03). The plan that follows from it is the rebuild in [docs/roadmap/MUS.md](../roadmap/MUS.md); this page records what was decided and why, so later work doesn't reopen it by accident._

## The decision

**Rebuild.** Music becomes a first-class module, built to beat Lidarr rather than copy it. The "Lidarr-lite" that MUS-04..24 described is not the target any more; its tasks are kept where they still fit, re-scoped where the new model changes them, and marked superseded where the new model replaces them.

The other two options are closed:
- **Park** would leave a 3/10 module sitting behind a Preview label indefinitely.
- **Cut** ([MUS-25](../roadmap/MUS.md#mus-25)) is not needed. It is marked won't-do.

Until the rebuild graduates ([MUS-12](../roadmap/MUS.md#mus-12)), Music stays off by default on fresh installs and keeps its Preview label. Everything ships to main behind the existing module toggle with additive migrations, because people already run Arrmada from main.

## Why Rebuild

The research (a report and five sets of notes, written 2026-10-19 and kept outside the repository) changed the question. It is no longer "can Arrmada match a mature competitor?". Lidarr's lead rests on integration, not on anything hard to build, and its worst failures are design choices a newcomer can simply not make:

| Lidarr choice | What it costs Lidarr's users | What Arrmada does instead |
|---|---|---|
| Every lookup goes through one proxy server its own team runs | The 2025 outage broke adds, searches and imports for about four and a half months, and was reopened in January 2026. An empty answer during an outage can delete an album and its files | Talk to MusicBrainz directly, cache everything, never delete on an empty answer |
| One edition per album, picked by "most files, then most tracks" | Deluxe and box-set bias; a correct standard-edition download fails "Album match is not close enough" | Store every edition and pick a target by a stated policy; any acceptable edition satisfies the album |
| Fixed 80% / 60% import gates, all or nothing | One odd track rejects a whole album; near-identical pressings are picked silently | A tiered matcher with settings, partial imports, a close-runner-up check, and Reviews for the doubtful ones |
| Quality guessed from the release name | VBR always reads "V0"; sample rate ignored; WAV ranked above FLAC | Probe the real files |
| One performer name per track; tags written in place | Featured artists lost; seeding torrents damaged by retagging | Multi-value artist tags, written in a staging copy |

Arrmada also has a head start Lidarr can't get: it already owns the household request flow, Plex monitoring (Insights already counts music plays), the movie and TV library that soundtrack suggestions need, and Books code for editions, field edits, naming templates and metadata-source upgrades. Today's Preview module already avoids several Lidarr mistakes (direct MusicBrainz, the earliest official edition rather than the biggest, never placing a file by its folder position). What it lacks is a schema wide enough for editions, credits and files, plus tag handling, a cache and real file facts. Those are what the rebuild adds.

The cost is honest: the rebuild is months of work, not the 3.5–4 weeks the Lidarr-lite was estimated at. The owner accepted that.

## Metadata: MusicBrainz direct, cached, never a central proxy

**MusicBrainz is the single source of truth** for which artists, albums, editions and recordings exist. It is the only large music database whose core data is CC0, the only one that can be self-hosted in full, and the spine that beets, Picard and Lidarr all use. Its IDs also connect everything else: AcoustID fingerprints resolve to MusicBrainz recording IDs, Cover Art Archive and Fanart.tv key art by MBID, and MusicBrainz stores links to Discogs, Deezer and Wikidata.

The rules:

1. **Direct, at most 1 request a second, through one app-wide limiter.** MusicBrainz allows about one request per second per IP, and going over gets *every* request refused with 503 until the rate drops. So every feature that talks to MusicBrainz (add artist, refresh, import, scan, Discover, requests) shares one limiter. No feature gets its own client.
2. **A User-Agent with contact details.** Every request names Arrmada, its real build version (v0.x; never "1.0") and a contact: the project URL, plus an optional contact address the admin can set.
3. **A permanent local cache.** Every MusicBrainz answer is stored in SQLite and kept. It is refreshed in the background on a slow schedule, and the old answer is served whenever MusicBrainz is slow or down. A failed or empty answer marks data "stale", never "gone": nothing is ever deleted, unmonitored or moved because an upstream call failed or came back empty.
4. **An optional self-hosted mirror.** A setting points Arrmada at a self-hosted MusicBrainz mirror instead of musicbrainz.org. With a mirror the limiter can be raised, because the limit is MusicBrainz's, not ours. (A mirror is a big box: about 100 GB of disk and 4 GB of RAM without search indexes, about 350 GB and 16 GB with them, plus a MetaBrainz token for daily updates. It must not be exposed to the internet. This is for owners who want it, never a requirement.)
5. **NEVER a central Arrmada-hosted proxy.** Arrmada will not run a metadata server that every install depends on. That single server is exactly how Lidarr lost search and import for months: one service, run by one volunteer in his spare time, serving tens of millions of requests a day. A one-person project would repeat that failure. Each install talks to MusicBrainz (or its owner's mirror) itself, and its cache keeps it working through upstream outages.

The trade-off is accepted: the first full load of a large library takes hours at 1 request a second (about three hours for 300 artists averaging 15 albums), and the cache means it is paid once.

## Fallback sources

MusicBrainz stays canonical. Other sources only fill gaps and supply art, and anything they supply is marked by source.

| Source | Policy | Why |
|---|---|---|
| **Deezer** | **On by default.** No key needed. Used for gaps: releases MusicBrainz doesn't have, plus ISRC and UPC to bridge sources | Keyless, broad coverage, carries the codes that link back to MusicBrainz |
| **Discogs** | **Opt-in**, with the user's own token entered in the app | Strong for vinyl, electronic and underground releases, but needs an account and has a 60-a-minute limit |
| **Cover Art Archive** | **First** for album art | Keyed by MBID, keyless. Art is cached locally once fetched, because CAA rides on the Internet Archive and has had outages |
| **Fanart.tv** | **Second** for album art, and the source for artist images | Keyed by MBID. The key is entered in the app, never shipped in code |
| **AcoustID** | **Second pass** for identifying files whose tags are poor or contradict the match | Fingerprints resolve to MusicBrainz recording IDs. Free for non-commercial use; the key is entered in the app |

Rules for gap-fill:
- An album that exists only on Deezer or Discogs is labelled "not on MusicBrainz" everywhere it shows.
- It is re-linked to MusicBrainz automatically once someone adds it there, without touching files, monitoring or profile (the Books metadata-source upgrade pattern).
- Sources are bridged by identifier, strongest first: MBID, then ISRC (recording), then UPC/barcode (release), then stored external IDs, then fuzzy title and duration.
- A candidate from another source competes in the matcher with a source-mismatch penalty, the way beets does it.
- Fallback sources never decide that something MusicBrainz has is wrong.

Other sources considered: iTunes Search is a possible last-resort art fallback later; Last.fm and ListenBrainz are for discovery only (R5); Spotify is not depended on (new apps get only a Premium-only development mode limited to five users, and its recommendation endpoints are closed to them).

## Tags, and copies while seeding

**Arrmada writes proper tags in one house style.** One style that Plex, Jellyfin and Navidrome all read well: every MusicBrainz ID, multi-value ARTISTS and ALBUMARTISTS, a COMPILATION flag, separate release and original dates, disc and track totals, the front cover embedded plus `cover.jpg` beside the tracks. FLAC, Ogg and Opus get Vorbis comments, MP3 gets ID3v2.4 (never ID3v2.3, which can't hold multiple values), and AAC/ALAC gets MP4 atoms. ReplayGain comes later. The full tag list is in [MUS-33](../roadmap/MUS.md#mus-33).

Tags are written in a **staging** folder on the same drive as the library, checked, and then moved into the library in **one move**. Players are never shown a half-written file, and Navidrome's "retag before you move" warning is honoured by construction.

**Retagging a file that is still seeding makes a copy, not a hardlink.** A hardlink shares the bytes with the torrent client's copy, so writing tags into it would change the seeding file and break its piece hashes. So when Arrmada needs to retag a file the download client still holds, it copies it into staging and tags the copy; the seeding file is never touched. The owner's words: *go with copies for now, see if it becomes an issue.* The cost is extra disk while the torrent seeds, and the disk guard counts it. If it becomes an issue, the options are a "don't retag, hardlink as downloaded" setting or tagging only after seeding ends; neither is built now.

Files already in the library are never retagged in place. A retag of an existing album stages a tagged copy, checks it, and swaps it in, with the original going to the recycle bin.

## Sources excluded

Music comes from **Usenet and torrents only**, through the indexers and download clients Arrmada already has.

- **No Soulseek** (the owner's earlier ruling, 2026-10-17).
- **No YouTube** or other video-site downloaders.
- **No streaming-rip sources** (Deezer, Qobuz, Tidal, Spotify or Apple Music rippers, and services like Lucida or DABmusic). These are the subject of DMCA anti-circumvention notices, and some need account cookies to work at all.

Deezer appears above only as a **metadata** source. Arrmada never downloads audio from it.

## Players

- **Plex first.** The household listens through Plex and Plexamp (lifetime Plex Pass). Arrmada asks Plex to scan its music library after every import, and a music request is "ready" only once Plex has it. Plex mostly re-matches albums online by name even with good local tags, so clean folders and a consistent album artist matter most for Plex.
- **Tags that also work for Jellyfin and Navidrome.** The house style is chosen so that an owner on Jellyfin or Navidrome gets correct albums, compilations and multi-artist credits without changing anything: one folder per album, disc tags, `cover.jpg`, `.lrc` sidecars and multi-value tags.
- **No Subsonic server now.** Arrmada already serves audiobooks to apps; a Subsonic/OpenSubsonic server for music is a separate product. It waits until the household asks for it.

## What this changes in the roadmap

- [MUS-03](../roadmap/MUS.md#mus-03) is done (Rebuild); [MUS-25](../roadmap/MUS.md#mus-25) is won't-do.
- [docs/roadmap/MUS.md](../roadmap/MUS.md) becomes the rebuild plan in five phases: **1 Safety**, **2 Core model**, **3 Stay current**, **4 Household**, **5 Beyond Lidarr**. Existing task IDs are kept where they apply; new work is MUS-26 onward.
- The README's Music line changes only when [MUS-12](../roadmap/MUS.md#mus-12) graduates the module.

## Revisit when

- Copies-while-seeding use more disk than the owner is happy with (see above).
- MusicBrainz changes its rate limit or User-Agent rules.
- The household asks to play music somewhere other than Plex (a Subsonic server).
- Deezer's keyless API becomes unreliable or changes its terms; it is a fallback, so turning it off loses gap-fill, never data.
