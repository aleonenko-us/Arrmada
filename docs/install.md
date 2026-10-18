# Installing Arrmada

## What you need

- A 64-bit Linux server with **Docker** and **Docker Compose v2**: Intel or AMD (x86-64), or
  64-bit ARM (arm64: a Raspberry Pi 4 or 5, an ARM NAS). See [ARM](#arm) for what differs
  there. The image also runs on a Mac with Apple Silicon through Docker Desktop (with the
  compose file, see [Docker Compose without the installer](#docker-compose-without-the-installer));
  the one-line installer is for Linux.
  - **Unraid:** Docker is built in (Settings → Docker). Install the **Docker Compose Manager**
    plugin from the Apps tab for Compose.
  - **Other Linux:** [Docker Engine](https://docs.docker.com/engine/install/) and its
    `docker-compose-plugin` package.
- A folder that holds your media and downloads (optional — Arrmada can manage its own storage,
  but you'll usually want your own share).
- A free [TMDB API key](https://www.themoviedb.org/settings/api), entered in the app during setup.

## The one-line install

On the server, as root (on Unraid, open the terminal from the top-right of the web UI):

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh
```

If you're not root, put `sudo` before `sh`.

The installer checks Docker, then asks two things — press **Enter** to accept each suggestion:

1. **Where Arrmada keeps its files.** The default is `/mnt/user/appdata/arrmada` on Unraid,
   `~/arrmada` (or `/opt/arrmada` as root) elsewhere. The database goes in `data/` inside it.
2. **Where your media lives.** Give the folder that *contains* your libraries and downloads
   (for example `/mnt/user/data` with `media/movies`, `media/tv` and `torrents` inside). It's
   mounted at `/storage` in the app; you pick each library folder inside it later, in the
   browser. Keeping libraries and downloads under one folder lets imports
   [hardlink](configuration.md#hardlinks) instead of copying.

It works out everything else and shows a summary before it starts:

| | How it's chosen |
|---|---|
| Ports | Free ones, so nothing clashes with Radarr (7878), qBittorrent (8080) and friends |
| User | Whoever owns your media folder (99:100 on Unraid) |
| Timezone | Your server's own (asked only if it can't be found) |
| GPU | `/dev/dri` is passed through when your server has one (NVIDIA: see [GPU](gpu.md#nvidia)) |
| Convert's scratch folder | `transcode/` next to the database |
| The app | The latest release, downloaded from `ghcr.io/tristenlammi/arrmada:stable` — the stable channel, which updates follow too (compiled on the server only with `--channel main` or if the download fails — that takes 30–60 minutes and [a few GB of memory](troubleshooting.md#the-build-fails-or-gets-killed)) |

When it's done it prints the address, e.g. `http://192.168.1.10:7878`.

## Finish in the browser

Open that address and create your admin account.

> The admin account can only be created from your home network (or the server itself), so a
> fresh install that's already reachable from the internet can't be claimed by a stranger.
> Installing on a remote server? Open the page through an SSH tunnel
> (`ssh -L 7878:localhost:7878 you@server`, then `http://localhost:7878`), or allow it for
> once with `ARRMADA_ALLOW_REMOTE_SETUP`
> ([Configuration](configuration.md#env-settings)) and take it out again once you're in.

A short setup then walks you through:

1. **Metadata** — paste your TMDB key (checked before it's saved). Other keys are optional.
2. **Folders** — pick each library (Movies, TV, Downloads, and optionally Books and Music)
   with a folder browser. Folders that look right are filled in for you.
3. **Plex** — sign in with Plex to connect your server, and optionally let family sign in
   with their Plex accounts.
4. **Indexers** — sync them from Prowlarr, or add a tracker and test it.
5. **Finish** — a checklist of anything left, which stays on the Dashboard until it's done.

You can skip it and come back from **Settings → System → Run setup again**. The bundled
qBittorrent connects itself — there's nothing to set up there.

## Options

Pass options after `sh -s --`:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh -s -- --media /mnt/user/data -y
```

| Option | What it does |
|---|---|
| `--dir DIR` | Where Arrmada keeps its files |
| `--media DIR` | The folder holding your libraries and downloads (mounted at `/storage`) |
| `--no-media` | Use Arrmada's own managed storage instead (not recommended on Unraid: it lives in `docker.img`) |
| `--transcode DIR` | Convert's scratch folder — a fast SSD pool, not the array (`none` to use the container) |
| `--tz ZONE` | Timezone, e.g. `Europe/London` ([list](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones)) |
| `--puid N --pgid N` | Run as this user and group |
| `--with-prowlarr` | Also run the optional Prowlarr indexer manager |
| `--with-sabnzbd` | Also run SABnzbd, for Usenet. Arrmada connects to it by itself; you add your own Usenet provider in SABnzbd and your Newznab indexers in Arrmada ([Usenet](configuration.md#usenet-sabnzbd)) |
| `--channel main` | Follow main instead of the releases: the newest code, compiled on your server at install and on every update (for developers; see [Following main](updating.md#following-main-developers)). `--channel stable` is the default |
| `--build` | The same as `--channel main` |
| `--image REF` | On the stable channel, run a specific published image, e.g. `ghcr.io/tristenlammi/arrmada:0.18.0` |
| `-y`, `--yes` | Don't ask anything; take the suggestions and the options given |

## Changing things later

Run the same line again with an option, on the server that has Arrmada:

- `--media /new/folder` — mount a different media folder (or add one to an install that
  started on managed storage). The old mounts are kept in `docker-compose.override.yml.bak`.
- `--transcode /mnt/cache/transcode` — move Convert's scratch folder.
- `ARRMADA_MEDIA_HOST=/mnt/user/arrmada-media` in `.env`, then `sh update.sh` — move
  Arrmada's own managed media (`/media`: its downloads and library, a Docker volume until
  now) to a folder on the host. qBittorrent and SABnzbd follow it
  ([Configuration](configuration.md#media-in-a-folder-of-your-own-env-only)).
- `--with-prowlarr` — add Prowlarr.
- `--with-sabnzbd` — add SABnzbd for Usenet. Nothing else is restarted, and it's
  added to whatever `COMPOSE_PROFILES` already lists (`prowlarr,sabnzbd`, say). An install
  whose files are older than the bundled SABnzbd is told to run `sh update.sh` first.

With no option it [updates](updating.md) instead. Ports, timezone and the run-as user live
in `.env` in Arrmada's folder: change a line there, then run `sh update.sh`.

## From a git checkout

Prefer git? This is how the project started, and it keeps working:

```sh
git clone https://github.com/tristenlammi/Arrmada
cd Arrmada
./install.sh
```

It installs into the checkout itself (the same questions, minus the folder) and
`./update.sh` pulls and updates. Run `./install.sh` again and it updates too.

## Unraid without Compose: the template

[templates/arrmada.xml](../templates/arrmada.xml) runs Arrmada as one container from the
published all-in-one image (`stable-aio`), with qBittorrent and FlareSolverr inside it, set
up and connected like the Compose install's; either can be switched off to use your own. It
keeps the database in `/mnt/user/appdata/arrmada` (`/data`), runs as 99:100, puts the web
page on port 7880, takes your media share at `/storage`, and publishes the torrent port
56881 (TCP and UDP). Then pick each library folder and the Downloads folder in the app.
[Arrmada on Unraid](unraid.md) has the details: installing it, the built-in qBittorrent and
using your own, the GPU, and updating.

## What gets installed

| Container | What it is |
|---|---|
| `Arrmada-app` | Arrmada itself |
| `Arrmada-qbittorrent` | The download client, already wired up |
| `Arrmada-flaresolverr` | Gets past Cloudflare on trackers that use it (no port published) |
| `Arrmada-prowlarr` | Only with `--with-prowlarr` |
| `Arrmada-sabnzbd` | Only with `--with-sabnzbd`: the Usenet downloader, already wired up (`Arrmada-sabnzbd-init` writes its first settings once) |

Arrmada's folder holds `docker-compose.yml`, your settings in `.env`, the mounts in
`docker-compose.override.yml`, the scripts, and `data/` (the database and its backups).

> ⚠ Never mount media at `/data` inside the container — that's where Arrmada keeps its
> database. The installer always mounts media at `/storage`.

## Docker Compose without the installer

One file, no git. In an empty folder on the server (e.g. `/mnt/user/appdata/arrmada`):

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/docker-compose.example.yml -o docker-compose.yml
```

The file reads its settings from a `.env` next to it, so you don't edit the file itself and
a newer copy can replace it without losing anything. Put your two folders there (full
paths), plus any other line you want to change from its default:

```sh
ARRMADA_DATA_HOST=/mnt/user/appdata/arrmada/data
ARRMADA_STORAGE_HOST=/mnt/user/data
```

| Setting | Default | What it is |
|---|---|---|
| `ARRMADA_DATA_HOST` | `/path/to/arrmada/data` | Where the database lives (a full path, e.g. `/mnt/user/appdata/arrmada/data` on Unraid). Its own folder — never your media |
| `ARRMADA_STORAGE_HOST` | `/path/to/your/media` | The folder that holds your libraries **and** downloads, so imports hardlink. It's `/storage` inside; never mount media at `/data` |
| `ARRMADA_MEDIA_HOST` | the `arrmada-media` volume | Optional: a host folder for `/media`, Arrmada's own downloads and library ([Configuration](configuration.md#media-in-a-folder-of-your-own-env-only)) |
| `ARRMADA_PUID` / `ARRMADA_PGID` | `1000` | The user and group that own your media folder (`ls -ln` shows the numbers; Unraid: 99 / 100) |
| `TZ` | `Etc/UTC` | Your timezone, e.g. `Europe/London` — schedules such as overnight encoding run on it. The same name `docker-compose.yml` and the installer use (not `ARRMADA_`-prefixed); like every variable here, one exported in the shell that runs `docker compose` wins over `.env` |
| `ARRMADA_PORT` | `7878` | The web page's port; change it if something (Radarr) already has 7878 |
| `ARRMADA_AUDIOBOOK_PORT` | `13379` | The audiobook server's port, for listening apps |
| `ARRMADA_QBIT_PORT` | `6881` | BitTorrent's port; forward it on your router (TCP and UDP) |

The two folders have to be set: their defaults are placeholders, kept only so a stack that
has been running on them keeps its database. (You can also write your paths into the file
in place of those defaults, the way it used to be done.)

Then `docker compose up -d`, open `http://<server>:7878`, create your admin account and
follow the setup (TMDB key, library folders under `/storage`, Plex, indexers). The bundled
qBittorrent is already connected; FlareSolverr runs beside it; Prowlarr and SABnzbd (for
Usenet) are in the file, commented out, if you want them. For an Intel or AMD GPU,
uncomment the `devices` line; for NVIDIA, see [GPU](gpu.md#nvidia).

Update with `docker compose pull && docker compose up -d`. This way has no pre-update backup
and no automatic roll-back (that's `update.sh`); the app still copies its database to
`<data>/backups` before any upgrade of its tables, and backs up nightly.

### Using Portainer

The same file works as a Portainer stack:

1. **Stacks → Add stack**, give it a name, and either pick **Web editor** and paste
   [`docker-compose.example.yml`](../docker-compose.example.yml), or pick **Repository** with
   `https://github.com/tristenlammi/Arrmada` and compose path `docker-compose.example.yml`.
2. Under **Environment variables**, add the settings from the table above: at least
   `ARRMADA_DATA_HOST` and `ARRMADA_STORAGE_HOST`. (**Load variables from .env file** takes a
   `.env` you already have.) Keep the folders full paths (`/mnt/user/…`): a stack has no
   folder of its own, so a relative path like `./data` would end up inside Portainer's own
   volume, hidden, and gone with the stack. With the variables, a **Repository** stack works
   too: it can't edit the file, and doesn't need to.
3. **Deploy the stack**, then open `http://<server>:7878`.

A Repository stack set up before these variables existed ran on the file's placeholder
folders (`/path/to/arrmada/data` and `/path/to/your/media` on the host), and it keeps them:
with no variables set, the file mounts exactly what it did. To move it to real folders, stop
the stack, move `/path/to/arrmada/data` to the new place first (it holds the database), then
set the variables and redeploy.

To update, use the stack's **Pull and redeploy** (re-pull the image). Like plain
`docker compose`, that skips `update.sh`'s pre-update backup and roll-back; the app still
copies its database to `<data>/backups` before any upgrade of its tables.

### From a git checkout

A checkout runs the published image the same way, with its database and media in Docker
volumes:

```sh
git clone https://github.com/tristenlammi/Arrmada
cd Arrmada
docker compose up -d
```

To change ports, the run-as user or where the database (`ARRMADA_DATA_HOST`) and media
(`ARRMADA_MEDIA_HOST`) live there, put the settings from
[Configuration](configuration.md) in a `.env` next to `docker-compose.yml`; if that `.env` sets
`ARRMADA_PORT`, also add `ARRMADA_APP_IMAGE=ghcr.io/tristenlammi/arrmada:stable` (see
[Updating](updating.md#upgrading-from-v010-or-earlier) for why). Update with
`git pull && docker compose pull && docker compose up -d`, or run `./install.sh` once to get
`update.sh`'s backups and roll-back (it keeps the database volume).

### ARM

The published image covers `linux/amd64` and `linux/arm64` under the same tags, and Docker
picks the right one by itself. The one-liner and the steps above are the same on ARM.

- **Raspberry Pi:** a Pi 4 or 5 with a **64-bit** OS (Raspberry Pi OS 64-bit, Ubuntu,
  DietPi 64-bit). A 32-bit OS can't run it, even on a 64-bit Pi; the installer says so.
  4 GB of memory or more is comfortable.
- **No GPU acceleration on ARM.** Convert encodes on the CPU (the same x265 and SVT-AV1 as
  on Intel/AMD), and AI subtitles (whisper) run on the CPU too: on a Pi that's slow, so
  subtitles from OpenSubtitles or the file itself are the better source there. Intel Quick
  Sync/VAAPI and the Intel oneAPI subtitle build are x86-64 only; the ARM image doesn't
  carry them and never offers them. The installer doesn't pass `/dev/dri` through on ARM.
- Releases up to v0.2.0 were published for x86-64 only. On ARM, those fail with
  `no matching manifest for linux/arm64`, and the installer compiles instead (see below).

### Compiling it yourself

`docker compose up -d --build` compiles the code in the folder instead (30–60 minutes the
first time: the subtitle and GPU tooling are built from source too). It needs **about
2 GB of memory free for the build** (4 GB is comfortable): the big compiles take turns and
use as many parallel jobs as the free memory allows. That's only for compiling it yourself;
the published image needs none of it. On ARM the build is smaller (no GPU tooling) but a
Raspberry Pi's cores are slower, so expect it to take longer. If a compiler is still killed, the
build says so plainly (see
[Troubleshooting](troubleshooting.md#the-build-fails-or-gets-killed)). Put
`ARRMADA_APP_IMAGE=arrmada:dev` in `.env` first, so your build is kept under its own name
rather than the published image's.

## Komodo, Portainer and other tools

The installer writes a normal Compose project, so tools that run `docker compose up -d` in
that folder work: `.env` names the image (`ARRMADA_APP_IMAGE=arrmada:dev`, the one `update.sh`
builds or downloads). If such a tool also does your updates, mirror what `update.sh` does —
see [Updating → Other deploy tools](updating.md#other-deploy-tools).
