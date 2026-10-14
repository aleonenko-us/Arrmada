# Installing Arrmada

## What you need

- A 64-bit Intel or AMD (x86-64) server running Linux, with **Docker** and **Docker Compose v2**.
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
| GPU | `/dev/dri` is passed through when your server has one |
| Convert's scratch folder | `transcode/` next to the database |
| The app | Downloaded from `ghcr.io/tristenlammi/arrmada:stable` (compiled on the server only if no release has been published yet — that takes 30–60 minutes) |

When it's done it prints the address, e.g. `http://192.168.1.10:7878`.

## Finish in the browser

Open that address and create your admin account. A short setup then walks you through:

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
| `--image REF` | Run a specific published image, e.g. `ghcr.io/tristenlammi/arrmada:0.18.0` |
| `--build` | Compile the app on your server instead of downloading it |
| `-y`, `--yes` | Don't ask anything; take the suggestions and the options given |

## Changing things later

Run the same line again with an option, on the server that has Arrmada:

- `--media /new/folder` — mount a different media folder (or add one to an install that
  started on managed storage). The old mounts are kept in `docker-compose.override.yml.bak`.
- `--transcode /mnt/cache/transcode` — move Convert's scratch folder.
- `--with-prowlarr` — add Prowlarr.

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

[templates/arrmada.xml](../templates/arrmada.xml) runs the app container alone from the
published `stable` image, for people who already run qBittorrent. It keeps the database in
`/mnt/user/appdata/arrmada` (`/data`), runs as 99:100, and takes your media share at
`/storage`. Mount the same share at `/storage` in qBittorrent too, then pick each library
folder and the Downloads folder in the app. The Compose install above bundles qBittorrent
and FlareSolverr instead.

## What gets installed

| Container | What it is |
|---|---|
| `Arrmada-app` | Arrmada itself |
| `Arrmada-qbittorrent` | The download client, already wired up |
| `Arrmada-flaresolverr` | Gets past Cloudflare on trackers that use it (no port published) |
| `Arrmada-prowlarr` | Only with `--with-prowlarr` |

Arrmada's folder holds `docker-compose.yml`, your settings in `.env`, the mounts in
`docker-compose.override.yml`, the scripts, and `data/` (the database and its backups).

> ⚠ Never mount media at `/data` inside the container — that's where Arrmada keeps its
> database. The installer always mounts media at `/storage`.

## Komodo, Portainer and plain `docker compose`

The installer writes a normal Compose project, so tools that run `docker compose up -d` in
that folder work. If such a tool also does your updates, mirror what `update.sh` does — see
[Updating → Other deploy tools](updating.md#other-deploy-tools).
