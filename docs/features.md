# Everything Arrmada does

Arrmada does the jobs of Radarr, Sonarr, Readarr, Bazarr, Overseerr, Tautulli and Tdarr in one
place, with one database and one web UI. It ships with qBittorrent and FlareSolverr already
wired up.

## Library and downloads

- **Movies** — search, grab, import, rename and upgrade. Keep several versions of a movie (say 4K and 1080p).
- **TV** — episodes and season packs, anime numbering (TheXEM and TheTVDB), and airing shows kept up to date.
- **More than one folder** — Movies and TV can each have extra folders (Kids, Animation, Anime…), with rules that file new titles into them and a Move to folder that moves a title's files.
- **Import from Radarr and Sonarr** — bring a whole library over in one go: every title with its quality profile (matched to yours), what's monitored, its folder and the files already on disk, which are recorded where they are rather than downloaded again. Preview first, map paths if the apps see your share somewhere else, and import each instance in turn; running it again adds only what's new. Wanted titles are searched a few at a time, never all at once.
- **Books** — ebooks and audiobooks with Hardcover or Open Library metadata, series tracking, several audiobook versions per book (standard and full cast), and multi-file audiobooks merged into one M4B.
- **Audiobook server** — listening apps built for Audiobookshelf (Lissen) connect to Arrmada directly. Places sync reliably across devices and restarts, a glitch can't reset anyone to the start, and earlier places can be put back. Everyone gets their own audiobook password, per-device sign-out and a page showing where they're up to and how much they've listened. The admin sees how much and when people listen, never what. Imports everyone's places from Audiobookshelf. E-reader apps can browse the books over OPDS.
- **Music** (preview, off by default) — artists, albums and whole-discography grabs. Switch it on in Settings → System → Modules.
- **Quality profiles** — describe the file you want (resolution, codecs, HDR, audio, audio languages, a bitrate window) once, and it drives what's grabbed, how releases rank, which files in your library fit, and when upgrades stop.
- **Audio languages** — every movie file and episode shows the languages of its audio tracks (EN, DE…), read from the file itself; an untagged track shows the language the subtitle AI heard on it, or "?". A profile's Audio language row can require, prefer or avoid languages, including "Original language" (each title's own), so "original plus German" is one profile. A release's languages come from its name: one that names none counts as the original language, MULTi or DL as the original plus the ones it names, and Dual Audio on a foreign title as the original plus English. Must refuses a release without the language and marks a file known to lack it as not fitting; a file whose audio hasn't been read yet is left alone rather than judged on a guess; Prefer ranks releases with it first and upgrades a file to one that has it (within the per-sweep upgrade limit); Avoid ranks a release last when every language it has is avoided. With rules set, the badges turn green, orange or red — with a ✓, ! or ✕ and the reason on hover.
- **Titles in other languages** — if your folders or trackers name titles in your own language ("Haus des Geldes" for Money Heist), add that language under Settings → Library → Also match titles in. Library scans and imports then recognise a title by its translated name, and TV searches (automatic, Search now, Search indexers and RSS) also look for a show's releases under it and take them. A name another title in your library goes by, or one two of your shows share, is never used; a year or country in the release name must be the show's; the release must also say the language (German, MULTi…) or the show's year or country, since a translated name is often another show's real one; and a one-word name of four letters or fewer is only taken with the show's year or country in the release. TV searches start using a new language once Arrmada has read it for every show, which happens a little at a time in the background. Empty (the default) changes nothing.
- **Indexers** — any Torznab indexer, one-click sync from Prowlarr, and built-in MyAnonaMouse, TorrentLeech and 1337x. FlareSolverr handles Cloudflare-protected trackers.
- **Downloads** — the bundled qBittorrent sets itself up. Imports hardlink instead of copying, seeding rules clean up, stalled downloads fail over, and mismatched downloads wait in a review queue.
- **Usenet** (optional) — SABnzbd beside qBittorrent: `install.sh --with-sabnzbd` runs one that Arrmada sets up and connects to by itself, or connect your own. Newznab indexers (added in Arrmada or synced from Prowlarr) are searched with the torrent ones; the better release wins wherever it is, or prefer one network. A release SABnzbd can't finish is blocklisted and replaced; a full disk or a provider problem is reported instead. You bring your own Usenet provider and indexers ([Usenet](configuration.md#usenet-sabnzbd)).
- **Safety nets** — a blocklist, a recycle bin, nightly database backups and a full history for every item.

## Requests and people

- **Discover and requests** — browse and request movies, TV and books, Overseerr-style. Auto-approve per user, and import your existing Overseerr requests.
- **Users** — admin, manager, requester and read-only roles, with optional Plex or Jellyfin sign-in. Requesters get Discover, Requests, Calendar, Books and Audiobooks; from outside your network, everything but Calendar.
- **Books shelf** — requesters can download any ebook in the library, or send it to their Kindle.
- **Calendar** — what's coming up.
- **Notifications** — Apprise alerts for admins, or signed JSON to [n8n or any webhook](n8n.md) for your own automations; an in-app inbox and web push for requesters.
- **Problem reports** — anyone can report a problem with a title, and many have a one-click fix.
- **Homepage dashboard** — a read-only widget for [gethomepage.dev](homepage.md): how many titles, what's wanted, downloads, requests waiting, what needs you, disk space and updates, as counts only.

## Media tools

- **Subtitles** — pulls embedded subtitles out, fetches them from OpenSubtitles, or writes them with local Whisper AI (GPU-accelerated on Intel).
- **Convert** — switch it on and it works through your library during the hours you choose, re-encoding only wasteful video to HEVC (or AV1, when a quick test shows it's clearly smaller and your devices play it). Every result is checked against the original and must look the same and save at least 20%, or the original stays. Atmos and every audio track are copied untouched, HDR10 and HDR10+ are kept, and Dolby Vision keeps its HDR10 base. Pauses while someone is watching on Plex or Jellyfin, can use the GPU, and trims audio and subtitle tracks to your languages. Originals are kept for 14 days so a conversion can be undone.
- **Insights** — Plex and Jellyfin watch history and stats, plus Plex buffering diagnostics, Tautulli-style.

## Media servers

Connect Plex, Jellyfin or both (Settings → Connections); how Arrmada uses each is Settings → Media servers.

- **Watch buttons** — a request counts as ready once either server has the title, and the requester's notice and the title's page get a Watch button for each server that has it, their own server's first.
- **Library updates** — after every import, upgrade, rename, delete or conversion, the server is asked to rescan just that folder, with path mappings when its folders have other names.
- **Sign-in** — family can sign in with Plex, or with their Jellyfin username and password (or Jellyfin's Quick Connect code); each server has its own switch and auto-approve choices.
- **Insights** — plays recorded from each server, with a server badge on every stream. Jellyfin keeps no watch history Arrmada can read, so its history starts when its watch monitoring is switched on, and it doesn't report buffering.
- **Several versions of a movie** — Jellyfin shows them as one title only when every file name starts with the movie folder's name, as Arrmada's default naming does. With a naming scheme that doesn't, Jellyfin lists each version as its own movie; Plex groups them either way.
