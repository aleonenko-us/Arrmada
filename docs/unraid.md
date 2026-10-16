# Arrmada on Unraid

There are two ways to run Arrmada on Unraid:

- **The Community Apps template** (this page). One container, Arrmada itself, managed from
  Unraid's Docker page like any other app. It uses the download client you already run
  (qBittorrent or SABnzbd from Community Apps, say), plus your own Prowlarr and FlareSolverr
  if you have them.
- **The Compose install** ([Install](install.md)). Arrmada plus a qBittorrent and
  FlareSolverr that are already wired up, through the Docker Compose Manager plugin. Pick it
  if you don't run a download client yet.

Both keep the same kind of database, but there's no switch from one to the other: choose one.

## Your share layout

Arrmada imports a finished download by hardlinking it into your library: no copy, no extra
space, and the torrent keeps seeding. A hardlink only works inside one mount, so keep your
libraries and your downloads in **one share** and give that share to Arrmada and to your
download client **at the same path** (`/storage`). For example, a share called `data`:

```text
/mnt/user/data
├── media
│   ├── movies
│   ├── tv
│   ├── ebooks
│   └── audiobooks
├── torrents          ← qBittorrent saves here
└── usenet            ← SABnzbd, if you use it
```

`/mnt/user` merges your array disks, so a hardlink also needs the download and the library
copy to land on the same disk. When they don't, the import copies instead and the log says
"imported by COPY". Setting the share's allocation method or split level so a folder stays
on one disk helps.

> ⚠ Never point anything at `/data` inside the container except appdata. That's where
> Arrmada keeps its database.

## Install from Community Apps

1. Open the **Apps** tab, search for **Arrmada**, and click **Install**.
2. Fill in **Media and downloads** with your share (`/mnt/user/data` in the example above).
   Everything else can stay as it is:

   | Field | Default | What it is |
   |---|---|---|
   | Web UI port | 7880 | The web page. Not 7878, because Radarr usually has that |
   | Audiobook server port | 13379 | For listening apps (Lissen and others). Off until you switch it on in the app |
   | App data | `/mnt/user/appdata/arrmada` | The database, its backups and settings (`/data` inside) |
   | Media and downloads | — | Your share, at `/storage` inside |
   | Transcode folder *(more settings)* | — | Optional scratch space for Convert. A cache pool is better than the array (`/mnt/cache/transcode`) |
   | PUID / PGID *(more settings)* | 99 / 100 | Unraid's `nobody:users`, which own your shares |
   | Starting library / downloads folder *(more settings)* | `/storage`, `/storage/downloads` | Only used until you pick the folders in the app |
   | FlareSolverr URL *(more settings)* | — | Optional. Can be set in the app instead |

3. Click **Apply**, then **WebUI** on Arrmada's icon in the Docker tab.

Your timezone comes from Unraid itself (Settings → Date and Time), so schedules such as
Convert's overnight window run on your clock. API keys and passwords never go in the
template: you enter them in the app.

## Install before it's listed

Until Arrmada shows up in Community Apps, install the same template by hand. In the Unraid
terminal (top right of the web UI):

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/templates/arrmada.xml \
  -o /boot/config/plugins/dockerMan/templates-user/my-Arrmada.xml
```

Then **Docker → Add Container**, choose **Arrmada** from the **Template** list, and carry on
from step 2 above.

## First start

Open the web UI and create your admin account. Setup then walks you through:

1. **Your TMDB key** (free, [themoviedb.org](https://www.themoviedb.org/settings/api)).
2. **Your folders.** Arrmada looks inside `/storage` and fills in folders named like your
   libraries (`/storage/media/movies`, `/storage/media/tv`, `/storage/torrents` in the
   example). Check them, or pick others with **Browse**.
3. **Your media server** (Plex, Jellyfin or both) and **indexers**.

The Dashboard's **Getting started** list ticks itself off as you go, including **Connect a
download client**, which is the next section.

## Connect your download client

Arrmada doesn't come with a download client in this template; it uses yours.

**Give the client the same share at the same path.** Edit the qBittorrent (or SABnzbd)
container and add a path: container path `/storage`, host path `/mnt/user/data`. Set its
download folder inside it (`/storage/torrents`). Now both containers name a file the same
way, and imports hardlink.

If you'd rather keep the client's own layout (the common `/downloads` → `/mnt/user/data/torrents`),
that works too: add a **path mapping** on the client's card in Arrmada (remote `/downloads`,
local `/storage/torrents`). The card suggests it when it sees a path it can't reach.

Then in Arrmada, **Settings → Connections → + Add client**:

- **qBittorrent:** the address of its web UI as Arrmada's container reaches it. On Unraid's
  default bridge network that's the server's IP and the port, `http://192.168.1.10:8080`
  (container names only work between containers on the same custom network). Then
  qBittorrent's web UI username and password.
- **SABnzbd:** its address and its **full API key** (SABnzbd → Config → General). See
  [Usenet](configuration.md#usenet-sabnzbd) for categories and indexers.

Arrmada sends each download to the Downloads folder you picked, and creates its own
categories (`arrmada`, `arrmada-tv`, `arrmada-books`, `arrmada-music`).

**Prowlarr:** Settings → Connections → Prowlarr, with its address and API key (Prowlarr →
Settings → General), then **Sync now**. **FlareSolverr** (for TorrentLeech, 1337x and
other trackers behind Cloudflare): its address in Settings → Connections, for example
`http://192.168.1.10:8191`.

## GPU (Intel or AMD)

Convert and Whisper subtitles use Intel or AMD graphics through `/dev/dri`. Check that the
server has it: `ls /dev/dri` in the Unraid terminal should list `renderD128`. Then edit
Arrmada, switch on **Advanced View** (top right), and add this to **Extra Parameters**,
after what's already there:

```text
--device=/dev/dri
```

The template doesn't add the device by itself: on a server without it, Docker would refuse
to start the container. NVIDIA cards aren't used. `docker exec Arrmada vainfo` shows what the
container can see.

## Updating

The template runs `ghcr.io/tristenlammi/arrmada:stable`, the latest published release.
When a new one is out, Arrmada's **Settings → Status → Updates** says so, and Unraid's
Docker tab shows **update ready** after **Check for Updates**. Click it (or **Update All**)
to pull the new image; the CA Auto Update plugin can do it on a schedule.

Before an update changes the database, Arrmada copies it to `backups/` in its appdata, and it
takes a nightly backup too ([Backups](backups.md)). To go back a version, edit the container
and set **Repository** to that release, for example `ghcr.io/tristenlammi/arrmada:0.2.0`.
An older version won't start on a database a newer one has already upgraded: restore the
backup from before the update first ([Backups](backups.md)).

## Troubleshooting

- **The container won't start: "port is already allocated".** Something else has that host
  port. Edit Arrmada and change the Web UI port (or the audiobook port).
- **"permission denied" on a library folder.** One disk's copy of a share folder is owned by
  root. Fix it through the share path: `chown -R 99:100` and `chmod -R u+rwX,g+rwX` on the
  library's parent folders.
- **Downloads finish but never import.** Arrmada can't see where the client saved them. The
  client's card in Settings → Connections names the path; give the client `/storage` too, or
  add the path mapping it suggests.
- More in [Troubleshooting](troubleshooting.md).
