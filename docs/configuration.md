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
| SABnzbd           | 8085    | `ARRMADA_SABNZBD_PORT` (only with `--with-sabnzbd`) — its own web page, where your Usenet provider goes |

## qBittorrent's login

The bundled qBittorrent asks for a password, like any other web page on your network. Arrmada
makes one when it first starts (a long random one), signs in with it, and keeps it in its data
folder (`qbittorrent-login.json`, readable only by the user Arrmada runs as). To open
qBittorrent's own page, an admin goes to **Settings → Connections**, opens the qBittorrent
card and clicks **Show its login**; the card links the page too. The login is never in the
client list, the logs or a support bundle.

Nothing else on the Docker network gets in without it, including FlareSolverr's browser,
which opens tracker pages. CSRF protection is on, so another site can't send your browser's
qBittorrent session commands either. If you put qBittorrent's page behind a reverse proxy,
it has to pass the original `Host` header through (most do; plain nginx needs
`proxy_set_header Host $host;`).

You can change the password in qBittorrent (Tools → Options → WebUI). Then enter the new one
on its card in Arrmada (**Edit**), or Arrmada can't get in; **Show its login** keeps showing
the one Arrmada made.

The "Bypass authentication for clients in whitelisted IP subnets" option is yours to use,
with one exception: turned back on with the subnets it was installed with (`10.0.0.0/8`,
`172.16.0.0/12`, `192.168.0.0/16`), Arrmada takes it for its old login-free access and
switches it off again at its next start. Use your own subnets (just your LAN's, say) and
it's left alone. An install that already had its own subnets there before this update is
left alone too.

## `.env` settings

| Setting | What it's for |
|---|---|
| `ARRMADA_CHANNEL` | What updates bring: `stable`, the published releases (the default), or `main`, the newest code compiled on the server ([Updating](updating.md#stable-or-main)) |
| `ARRMADA_IMAGE` | The published image `update.sh` downloads on the stable channel (`ghcr.io/tristenlammi/arrmada:stable` when unset) ([Updating](updating.md#published-image-or-compiled-here)) |
| `ARRMADA_APP_IMAGE` | The image `docker compose` runs. `arrmada:dev` (what the installer writes) is whatever `update.sh` built or downloaded — leave it. Without the line, a plain `docker compose up -d` runs the published image ([Updating](updating.md#upgrading-from-v010-or-earlier)) |
| `ARRMADA_DATA_HOST` | Where the database, its backups and settings live on the host |
| `ARRMADA_STORAGE_HOST` | Your media folder (for reference; the mount itself is in `docker-compose.override.yml`) |
| `ARRMADA_PUID`, `ARRMADA_PGID` | The user and group Arrmada and qBittorrent run as. On Unraid, 99 and 100 |
| `TZ` | Timezone. Schedules — the Convert window especially — run on this clock ([list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones)) |
| `COMPOSE_PROFILES` | The optional companions to run, a comma list: `prowlarr` (Prowlarr), `sabnzbd` (SABnzbd, for Usenet), e.g. `prowlarr,sabnzbd`. `update.sh` never changes it |
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

### More than one Movies or TV folder

Movies and TV can each have extra folders — a Kids or Animation movies folder, an Anime or
Cartoons TV folder, often its own Plex or Jellyfin library. Add them in **Settings → Library →
More Movies and TV folders**. Each can have a rule (Kids, Animation or Anime): a new title goes
to the first folder whose rule matches, otherwise to the main folder, and you can pick another
when adding a title or approving a request. A scan walks every folder and files each title
under the folder it's in. **Move to folder** on a movie or show moves its files (a rename on the
same drive; to another drive each file is copied and checked, then the originals go to the
recycle bin). A folder still holding titles can't be removed. Extra folders must be inside a
folder mounted into the container — under `/storage` with the rest, or added to
`docker-compose.override.yml` (see the example file). Moving from Radarr or Sonarr with several
root folders? **Settings → Import & migrate → Import from Radarr / Sonarr** sets up the same
folders as part of the import (below). With one folder per library, nothing about folders shows
anywhere. A folder on another disk can have its own downloads folder, so its imports hardlink
([Libraries on separate disks](#libraries-on-separate-disks)).

### Moving from Radarr and Sonarr

**Settings → Import & migrate → Import from Radarr / Sonarr** (admins) brings a library over.
Enter the app's address and API key (Settings → General in Radarr or Sonarr; it's used for the
import and never saved) and Arrmada reads it without changing anything there. The preview shows:

- **The titles**: how many are already in Arrmada (films by TMDB id, shows by TMDB or TVDB id),
  how many are new, and any it can't match.
- **Folders**: each root folder and the Arrmada folder its titles go to — matched by path or by
  name, or added on the spot as an extra folder.
- **Where the files are**: Radarr and Sonarr often see your share at another path (`/movies`
  there, `/storage/media/movies` here). Map the start of their paths to Arrmada's and press
  **Check again** until the files are found. A file must be inside one of Arrmada's library
  folders to be used.
- **Quality profiles**: an Arrmada profile for each of theirs — the one of the same name, else
  one reaching the same resolution, else your default.

**Import** runs in the background. Each title is added with its profile, monitoring and minimum
availability (films), or its monitored seasons and episodes, anime type and "monitor new
seasons" (shows); the files already on disk are recorded where they are — nothing is moved or
downloaded again. A title whose files can't be found is skipped and named in the summary (fix
the mapping and run it again), unless you ask for such titles to come over anyway and be
downloaded again. Nothing is searched when you import: wanted titles join the regular missing
searches ten at a time per run, so a big library doesn't start hundreds of downloads at once.
Running the import again — or for a second instance, like Sonarr and Sonarr-anime — adds only
what's new; a title already in Arrmada keeps its settings and only gets files it was missing.
Tags, blocklists, history and download-client settings aren't brought over.

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

### Libraries on separate disks

With Movies on one disk and TV on another, one Downloads folder means one of them always
imports by copying, and the copy takes the space twice while the torrent seeds. Give that
library folder a downloads folder **on its own disk** instead: in **Settings → Library → More
Movies and TV folders**, use **Download to a folder on this disk…** on the folder (the main
Movies and TV folders have it too; with one folder per library, open it with **Libraries on
different disks?** first). Torrents for titles in that folder then save there and import as
hardlinks. Anything without one keeps using the Downloads folder.

1. Make a downloads folder on each disk, next to its library — for example
   `/mnt/disk1/media/movies` with `/mnt/disk1/torrents`, and `/mnt/disk2/media/tv` with
   `/mnt/disk2/torrents`.
2. Mount both disks into Arrmada's container (the installer's `--media` folder, or extra
   mounts in `docker-compose.override.yml`).
3. **If qBittorrent runs in its own container, mount the same folders into it at the same
   paths.** Arrmada tells qBittorrent where to save by path; qBittorrent has to see that path,
   and Arrmada has to see the finished files at the same one. (Different paths need a path
   mapping on the qBittorrent card in Settings → Connections.)
4. Pick each downloads folder. It must exist, be writable, and sit outside every library
   folder and Arrmada's data folder. If it isn't on the same filesystem as its library folder
   the save goes through with a warning, and the health panel keeps a note of it — imports from
   it would still copy.

What to expect:

- **Torrents only.** SABnzbd files jobs by category, not by folder, so usenet downloads keep
  going to SABnzbd's own folders. A usenet download is deleted once it's imported, so it never
  takes the space twice for long.
- The disk guard watches every downloads folder and pauses only the downloads saving onto a
  full disk; the health panel checks each one exists, is writable and has free space.
- **Move to folder** moves the title's library files, not a download that's still seeding:
  that keeps seeding where it is (its hardlink keeps the data). New downloads and upgrades for
  the title go to the new folder's downloads folder.
- qBittorrent's own "keep incomplete torrents in" folder, if you've switched it on, is one
  folder for everything: a download sits there until it finishes and is then moved to the
  library's downloads folder. Arrmada's bundled qBittorrent has it off.
- Arrmada adds its torrents with automatic torrent management off, so a qBittorrent whose
  default mode is Automatic still saves them where Arrmada asked, and re-filing one into
  another category never moves its files.
- Clearing or changing a folder's downloads folder (or removing the library folder) doesn't
  strand what's already there: those torrents keep seeding, and they can still be imported
  by hand or from Needs you.

### Other layouts

For read-only trials or unusual layouts, see
[docker-compose.override.example.yml](../docker-compose.override.example.yml).

## GPU

If your server has `/dev/dri` (Intel or AMD graphics), the installer passes it through, and
Convert and Whisper subtitles use it. Intel Arc and other Gen12+ chips are used through VAAPI
(HEVC, H.264 and AV1 encoding); `docker exec Arrmada-app vainfo` shows what the container sees.

On ARM (arm64) there's no GPU acceleration: the image carries no GPU encoders or GPU subtitle
build, Convert and Whisper use the CPU, and the installer doesn't pass `/dev/dri` through.
Passing it through by hand does no harm; Convert tests every encoder at start-up and only
offers the ones that work.

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

## Usenet (SABnzbd)

Arrmada can download from Usenet as well as torrents, through SABnzbd. It's optional, and
it needs two things Arrmada can't give you: **your own Usenet provider** (a paid account —
the news servers the files come from) and **Newznab indexers** (the sites that list what's
on Usenet). Without both, Usenet adds nothing.

There are two ways to have SABnzbd.

**The bundled one.** `install.sh --with-sabnzbd` (on a new install, or later on the one you
have) runs SABnzbd beside Arrmada and Arrmada connects to it by itself, usually within a
minute: its API key is read from SABnzbd's own settings (never written to `.env`, never
shown), Arrmada's categories are there with a folder each, it's in English, every job is
repaired, unpacked and tidied (post-processing 3), and its downloads go under `usenet/` in
your Downloads folder — `usenet/incomplete` while downloading, `usenet/complete` when
done — so an import is a rename on the same drive rather than a copy. That folder follows
your Downloads folder when you change it in Settings → Library, once Arrmada has checked
SABnzbd can see it (the installer mounts your media folder into SABnzbd for that).

What's left is your provider: open `http://<server>:8085` (the port the installer printed;
the SABnzbd card in Settings → Connections has the link) → **Config → Servers**, and add it
there — the first visit opens SABnzbd's setup wizard, which does the same. Leave the
folders it shows as they are. Getting started and the health list say "Add your Usenet
provider in SABnzbd" until there's a news server. Open SABnzbd by the server's IP address:
it only answers to the names in its `host_whitelist` (Config → Special), which starts with
just `arrmada-sabnzbd`; add your server's name there to use that instead.

Removed in Arrmada, the bundled SABnzbd stays removed (even after a restart) until **Restore
bundled SABnzbd** in Settings → Connections. To stop running it at all, take `sabnzbd` out of
`COMPOSE_PROFILES` in `.env` and run `docker compose rm -sf arrmada-sabnzbd`.

**Your own SABnzbd.** In Settings → Connections → **+ Add client**, pick SABnzbd and give
its address (as Arrmada's container reaches it) and its **full API key** (SABnzbd → Config →
General; the NZB key can't read the queue). Arrmada never changes your SABnzbd by itself:
its card shows which of Arrmada's categories are missing, and **Add missing categories**
creates them when you click it. If SABnzbd says "hostname verification failed", add the
name Arrmada uses to its `host_whitelist`. If SABnzbd sees its downloads under a different
path than Arrmada does, add a path mapping on the card. Keep SABnzbd in English: Arrmada
reads its failure reasons, and translated ones can't be told apart.

**Indexers.** Add Newznab indexers in Settings → Connections → **+ Add indexer** (the
Newznab tile appears once a Usenet client exists), or keep them in Prowlarr: its Usenet
indexers come over in the Prowlarr sync once Arrmada has a Usenet client, and adding one
runs the sync straight away.

**Usenet or torrents.** By default the better release wins wherever it is. Settings →
Downloads → **When a release is on both Usenet and torrents** can prefer one network:
the preference only decides between releases that are otherwise as good — it never picks a
worse release, and it never hides the other network's.

**When a download fails.** Arrmada reads SABnzbd's reason:

- Something wrong with the release — articles missing from your provider, damage its
  repair files can't fix, a password, an archive that won't unpack, a duplicate — and the
  release is blocklisted and another one searched for, as with a torrent that can't finish.
- Something wrong where SABnzbd writes — a full disk, a folder it may not write to, a
  missing drive — and nothing is blocklisted (any release would fail the same way). Needs
  you says what to fix; then Retry the download in Activity.
- Nothing downloading for half an hour while jobs wait is almost always the provider or
  your account, so it's never "fixed" by grabbing something else: the health list says to
  check your provider instead.

After an import Arrmada archives the job in SABnzbd's history and deletes what's left of
its folder. Usenet downloads never seed.

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
