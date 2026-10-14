# Configuration

Almost everything is set in the app (Settings). A few things have to be known before the
container starts; those live in `.env` in Arrmada's folder. Change a line, then run
`sh update.sh` to apply it.

## Ports

The installer picks free ports so nothing clashes with apps you already run. It prints them
at the end and saves them in `.env`.

| Service           | Default | Setting                   |
| ----------------- | ------- | ------------------------- |
| Arrmada           | 7878    | `ARRMADA_PORT`            |
| qBittorrent WebUI | 8080    | `ARRMADA_QBIT_WEBUI_PORT` |
| BitTorrent        | random  | `ARRMADA_QBIT_PORT` — forward this on your router (TCP and UDP) |
| Audiobook server  | 13379   | `ARRMADA_AUDIOBOOK_PORT` — for listening apps; off until switched on in Audiobooks |
| Prowlarr          | 9696    | `ARRMADA_PROWLARR_PORT` (only with `--with-prowlarr`) |

## `.env` settings

| Setting | What it's for |
|---|---|
| `ARRMADA_IMAGE` | The published image `update.sh` downloads; without it, updates compile here ([Updating](updating.md#published-image-or-compiled-here)) |
| `ARRMADA_APP_IMAGE` | The image `docker compose` runs. `arrmada:dev` (what the installer writes) is whatever `update.sh` built or downloaded — leave it. Without the line, a plain `docker compose up -d` runs the published image ([Updating](updating.md#upgrading-from-v010-or-earlier)) |
| `ARRMADA_DATA_HOST` | Where the database, its backups and settings live on the host |
| `ARRMADA_STORAGE_HOST` | Your media folder (for reference; the mount itself is in `docker-compose.override.yml`) |
| `ARRMADA_PUID`, `ARRMADA_PGID` | The user and group Arrmada and qBittorrent run as. On Unraid, 99 and 100 |
| `TZ` | Timezone. Schedules — the Convert window especially — run on this clock ([list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones)) |
| `COMPOSE_PROFILES` | `prowlarr` to run the optional Prowlarr |
| `ARRMADA_RECYCLE_DIR` | Override for the recycle bin (see below) |
| `ARRMADA_SKIP_MIGRATION_SNAPSHOT` | `1` to upgrade the database without the safety copy (a full disk) |
| `ARRMADA_ALLOW_NEWER_SCHEMA` | `1` to start an older version on a newer database — last resort |
| `ARRMADA_EXTERNAL_HEADER` | The header a tunnel or proxy stamps on outside requests (default `Cf-Connecting-Ip`); such requests are limited to the requester pages |
| `ARRMADA_LIBRARY_DIR`, `ARRMADA_DOWNLOADS_DIR`, `ARRMADA_MOVIES_DIR`, `ARRMADA_TV_DIR`, `ARRMADA_EBOOKS_DIR`, `ARRMADA_AUDIOBOOKS_DIR`, `ARRMADA_MUSIC_DIR` | Starting values for the library folders. The folders picked in the app win; most people never set these |
| `ARRMADA_PLEX_URL`, `ARRMADA_PLEX_TOKEN` | An older way to point Insights at Plex — connect Plex in Settings → Connections instead |

API keys (TMDB, Hardcover, OMDb, OpenSubtitles…) and passwords are entered in the app, in
Settings → Connections — never in `.env`. Older installs may still have
`ARRMADA_TMDB_API_KEY` and friends there; they keep working as a fallback.

## Your media folder

Give the installer the folder that contains your media, and pick each library inside it
during setup. Changing a library or the Downloads folder in **Settings → Library** takes
effect straight away: the next import, grab and disk-guard check use the new folder, and
qBittorrent's save path follows. Files already imported stay where they are.

To mount a different folder later, run the install line again with `--media /new/folder`
([Install → Changing things later](install.md#changing-things-later)).

> ⚠ Never mount media at `/data`. That path holds Arrmada's own database.

### Hardlinks

For hardlinks, the library and the download folder must sit under **one** mount: one share
(say `/mnt/user/data`) with `media/movies`, `media/tv` and `torrents` inside, mounted once at
`/storage` in both Arrmada and qBittorrent. Then a finished download appears in your library
instantly, takes no extra space, and keeps seeding. The installer sets it up this way.

On Unraid, `/mnt/user` merges disks: a hardlink only works when the download and the library
land on the same physical disk; otherwise the import falls back to a copy (the log says
"imported by COPY"). If a share suddenly refuses new folders ("permission denied" on a
library that used to work), one disk's copy of the folder is owned by root — fix it through
the share path: `chown 99:100` and `chmod 775` on the library parent folders.

### Other layouts

For read-only trials or unusual layouts, see
[docker-compose.override.example.yml](../docker-compose.override.example.yml).

## GPU

If your server has `/dev/dri` (Intel or AMD graphics), the installer passes it through, and
Convert and Whisper subtitles use it. Intel Arc and other Gen12+ chips are used through VAAPI
(HEVC, H.264 and AV1 encoding); `docker exec Arrmada-app vainfo` shows what the container sees.

## Convert's scratch folder

Convert encodes into a scratch folder before swapping the result in. The installer puts it at
`transcode/` next to the database; a fast SSD or NVMe pool is better than the array
(`--transcode /mnt/cache/transcode`, with the share set to the pool only so the mover skips
it). Settings → Convert can also point it somewhere else and shows the free space.

## Recycle bin

Deleted and replaced files go to a hidden `.arrmada-recycle` folder at the top of the
library folder they came from (Movies, TV, and so on), so a delete is a quick move on the
same drive rather than a copy. Each of those folders has a `.plexignore` and an `.ignore`, so
neither Plex nor Jellyfin lists what's in it, and Arrmada's own library scans skip hidden folders. Restore, size and
age limits are in Settings → Downloads → Recycle bin; the size limit counts every bin together.

Older versions kept one shared bin in the app's library volume. It stays listed (marked
"Legacy") until you empty it, and only takes files that sit outside every library folder.

`ARRMADA_RECYCLE_DIR` in `.env` is now just an override: set it to a folder to use that one
bin for everything, or to `off` to delete files straight away.

## Connecting Jellyfin

Jellyfin is connected in the app, on its card in Settings → Connections (next to Plex's):

1. **Server URL** — the address Arrmada reaches Jellyfin on, for example
   `http://192.168.1.10:8096`, or `http://jellyfin:8096` when both run in the same Docker
   network.
2. **API key** — make one in Jellyfin under **Dashboard → API Keys → +**, and paste it. Test
   checks it before you save; once saved it's never shown again.
3. **Public address for Watch links** — what every requester's Jellyfin button opens. It
   starts as the server URL; if that only works at home (a `192.168.…` address or a
   container name), set the address you reach Jellyfin on from outside, like
   `https://jellyfin.example.com`.

The rest is in Settings → Media servers, Jellyfin's group:

- **Watch monitoring** — off until you switch it on. Arrmada then asks Jellyfin what's playing
  every few seconds and records each play in Insights; Jellyfin's history in Insights starts
  then.
- **Library updates** — on by default once connected. If a line says "whole library", add a
  path mapping from Arrmada's folder to the same folder as Jellyfin sees it.
- **Sign-in** — let people sign in with their Jellyfin username and password, and optionally
  Quick Connect (switch it on in Jellyfin too, under Dashboard → General). Passwords go
  straight to Jellyfin and are never kept.
- **Disconnect** — forgets the address and key; the choices above stay for a reconnect.

If you keep several versions of a movie (say 4K and 1080p), Jellyfin shows them as one title
only when each file name starts with the movie folder's name — `Dune (2021)/Dune (2021) -
2160p.mkv` — which Arrmada's default movie naming does. If you've changed the naming in
Settings → Naming & metadata so file names start differently, Jellyfin lists each version
as its own movie.

The first-run setup's **Media server** step offers the same: Plex, Jellyfin, both, or later.

## Reverse proxies and tunnels

Give Arrmada its own hostname (for example `arrmada.example.com`) and proxy it from the root.
Serving it under a path such as `example.com/arrmada` isn't supported: the old
`ARRMADA_BASE_URL` setting never worked with the web app and has been removed. If your `.env`
still sets it, it's ignored and the app runs at the root.

Behind Cloudflare, uploads over 100 MB are refused, so restore big backups from your home
network.
