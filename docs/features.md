# Everything Arrmada does

Arrmada does the jobs of Radarr, Sonarr, Readarr, Bazarr, Overseerr, Tautulli and Tdarr in one
place, with one database and one web UI. It ships with qBittorrent and FlareSolverr already
wired up.

## Library and downloads

- **Movies** — search, grab, import, rename and upgrade. Keep several versions of a movie (say 4K and 1080p).
- **TV** — episodes and season packs, anime numbering (TheXEM and TheTVDB), and airing shows kept up to date.
- **Books** — ebooks and audiobooks with Hardcover or Open Library metadata, series tracking, several audiobook versions per book (standard and full cast), and multi-file audiobooks merged into one M4B.
- **Audiobook server** — listening apps built for Audiobookshelf (Lissen) connect to Arrmada directly. Places sync reliably across devices and restarts, a glitch can't reset anyone to the start, and earlier places can be put back. Everyone gets their own audiobook password, per-device sign-out and a page showing where they're up to and how much they've listened. The admin sees how much and when people listen, never what. Imports everyone's places from Audiobookshelf. E-reader apps can browse the books over OPDS.
- **Music** (preview, off by default) — artists, albums and whole-discography grabs. Switch it on in Settings → System → Modules.
- **Quality profiles** — describe the file you want (resolution, codecs, HDR, audio, a bitrate window) once, and it drives what's grabbed, how releases rank, which files in your library fit, and when upgrades stop.
- **Indexers** — any Torznab indexer, one-click sync from Prowlarr, and built-in MyAnonaMouse, TorrentLeech and 1337x. FlareSolverr handles Cloudflare-protected trackers.
- **Downloads** — the bundled qBittorrent sets itself up. Imports hardlink instead of copying, seeding rules clean up, stalled downloads fail over, and mismatched downloads wait in a review queue.
- **Safety nets** — a blocklist, a recycle bin, nightly database backups and a full history for every item.

## Requests and people

- **Discover and requests** — browse and request movies, TV and books, Overseerr-style. Auto-approve per user, and import your existing Overseerr requests.
- **Users** — admin, manager, requester and read-only roles, with optional Plex sign-in. Requesters get Discover, Requests, Calendar, Books and Audiobooks; from outside your network, everything but Calendar.
- **Books shelf** — requesters can download any ebook in the library, or send it to their Kindle.
- **Calendar** — what's coming up.
- **Notifications** — Apprise alerts for admins; an in-app inbox and web push for requesters.
- **Problem reports** — anyone can report a problem with a title, and many have a one-click fix.

## Media tools

- **Subtitles** — pulls embedded subtitles out, fetches them from OpenSubtitles, or writes them with local Whisper AI (GPU-accelerated on Intel).
- **Convert** — switch it on and it works through your library during the hours you choose, re-encoding only wasteful video to HEVC (or AV1, when a quick test shows it's clearly smaller and your devices play it). Every result is checked against the original and must look the same and save at least 20%, or the original stays. Atmos and every audio track are copied untouched, HDR10 and HDR10+ are kept, and Dolby Vision keeps its HDR10 base. Pauses while someone is watching Plex, can use the GPU, and trims audio and subtitle tracks to your languages. Originals are kept for 14 days so a conversion can be undone.
- **Insights** — Plex watch history, stats and buffering diagnostics, Tautulli-style.
