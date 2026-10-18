# Arrmada on Unraid

There are two ways to run Arrmada on Unraid:

- **The Community Apps template** (this page). One container, managed from Unraid's Docker
  page like any other app, with **qBittorrent and FlareSolverr built in**: Arrmada starts
  them, connects to them and keeps their settings in step, so there's no download client to
  set up. Already have your own qBittorrent, SABnzbd or FlareSolverr? Switch the built-in
  ones off and use yours.
- **The Compose install** ([Install](install.md)). The same app with qBittorrent and
  FlareSolverr as containers of their own, through the Docker Compose Manager plugin, plus
  the optional Prowlarr and SABnzbd. Its folders are lines in the stack's `.env`
  (`ARRMADA_DATA_HOST`, and `ARRMADA_MEDIA_HOST` for media in a share rather than in
  `docker.img`), so the compose file itself never needs editing
  ([Configuration](configuration.md#media-in-a-folder-of-your-own-env-only)).

Both keep the same kind of database, but there's no switch from one to the other: choose one.

## Your share layout

Arrmada imports a finished download by hardlinking it into your library: no copy, no extra
space, and the torrent keeps seeding. A hardlink only works inside one mount, so keep your
libraries and your downloads in **one share**, given to Arrmada at `/storage`. For example,
a share called `data`:

```text
/mnt/user/data
├── media
│   ├── movies
│   ├── tv
│   ├── ebooks
│   └── audiobooks
├── downloads         ← the built-in qBittorrent saves here (or torrents/, your choice)
└── usenet            ← SABnzbd, if you use your own
```

`/mnt/user` merges your array disks, so a hardlink also needs the download and the library
copy to land on the same disk. When they don't, the import copies instead and the log says
"imported by COPY". Setting the share's allocation method or split level so a folder stays
on one disk helps.

> ⚠ Never point anything at `/data` inside the container except appdata. That's where
> Arrmada keeps its database (and the built-in qBittorrent its settings).

## Install from Community Apps

1. Open the **Apps** tab, search for **Arrmada**, and click **Install**.
2. Fill in **Media and downloads** with your share (`/mnt/user/data` in the example above).
   Everything else can stay as it is:

   | Field | Default | What it is |
   |---|---|---|
   | Web UI port | 7880 | The web page. Not 7878, because Radarr usually has that |
   | Torrent port (TCP) and (UDP) | 56881 | The built-in qBittorrent's incoming port. Forward it on your router (TCP and UDP) to seed well |
   | Audiobook server port | 13379 | For listening apps (Lissen and others). Off until you switch it on in the app |
   | App data | `/mnt/user/appdata/arrmada` | The database, its backups and settings, and the built-in qBittorrent's (`/data` inside) |
   | Media and downloads | — | Your share, at `/storage` inside |
   | qBittorrent web UI port *(more settings)* | 8091 | Optional: qBittorrent's own page, if you want to look inside it |
   | Transcode folder *(more settings)* | — | Optional scratch space for Convert. A cache pool is better than the array (`/mnt/cache/transcode`) |
   | Built-in qBittorrent / FlareSolverr *(more settings)* | true | `false` to use your own instead ([below](#using-your-own-download-client-or-flaresolverr)) |
   | Torrent port setting *(more settings)* | 56881 | The port qBittorrent listens on: always the same number as the torrent port entries |
   | qBittorrent web UI host port *(more settings)* | 8091 | The same number as the qBittorrent web UI port, so Arrmada links the right page |
   | PUID / PGID *(more settings)* | 99 / 100 | Unraid's `nobody:users`, which own your shares. The built-in qBittorrent runs as them too |
   | Starting library / downloads folder *(more settings)* | `/storage`, `/storage/downloads` | Only used until you pick the folders in the app |
   | FlareSolverr URL *(more settings)* | — | Blank uses the built-in one. Your own FlareSolverr's address if you switched that off |

3. Click **Apply**, then **WebUI** on Arrmada's icon in the Docker tab.

Your timezone comes from Unraid itself (Settings → Date and Time), so schedules such as
Convert's overnight window run on your clock. API keys and passwords never go in the
template: you enter them in the app.

**A port that's taken.** If Unraid says a port "is already allocated", another container
has it: pick another number. For the torrent port, change it in **all three places** (both
sides of the TCP entry, both sides of the UDP entry, and **Torrent port setting**):
qBittorrent tells other peers the port it listens on and can't know about a different
number on the server side.

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

Open the web UI from your home network and create your admin account (it can't be created
from outside, so nobody else can claim a new install). Setup then walks you through:

1. **Your TMDB key** (free, [themoviedb.org](https://www.themoviedb.org/settings/api)).
2. **Your folders.** Arrmada looks inside `/storage` and fills in folders named like your
   libraries (`/storage/media/movies`, `/storage/media/tv`, `/storage/downloads` in the
   example). Check them, or pick others with **Browse**. The built-in qBittorrent saves to
   the Downloads folder you pick.
3. **Your media server** (Plex, Jellyfin or both) and **indexers**.

The Dashboard's **Getting started** list ticks itself off as you go. **Connect a download
client** is already done: the built-in qBittorrent registers itself on first start.

## The built-in qBittorrent and FlareSolverr

They run inside Arrmada's container and start and stop with it: qBittorrent as the same
user (PUID/PGID), FlareSolverr as a user of its own that can't read Arrmada's appdata (its
browser opens tracker pages). The container log shows their output with `[qbittorrent]` and `[flaresolverr]` in front
(Docker tab → Arrmada's icon → **Logs**); `[aio]` lines are the part that starts them.

- **qBittorrent** shows up in **Settings → Connections** as *qBittorrent (bundled)*.
  Arrmada sets its incoming port, its save folder and its queue limits, and files each
  download under its own categories. Its settings and torrent list live in appdata
  (`qbittorrent/`), so they survive updates.
- **qBittorrent's own page** is on the qBittorrent web UI port (8091:
  `http://<server>:8091`). Its login was made by Arrmada on first start: open the
  qBittorrent card in Settings → Connections and choose **Show its login** (admins only).
  Nobody gets in without it, Arrmada included: it signs in with the same login. If you
  change the password in qBittorrent's own settings, enter the new one on the qBittorrent
  card too (**Edit**), or Arrmada can't reach it.
- **FlareSolverr** answers only inside the container, and Arrmada's FlareSolverr setting
  points at it unless you set your own. It's used for trackers behind Cloudflare (TorrentLeech,
  1337x) and TheXEM.

Unraid servers are x86-64, which has both. (The all-in-one image's 64-bit ARM build has no
FlareSolverr, because the Chromium it needs is x86-64 only; it says so in the log.)

## Using your own download client or FlareSolverr

Edit Arrmada, switch on **Show more settings**, and set **Built-in qBittorrent** (or
**Built-in FlareSolverr**) to `false`. After **Apply** it doesn't start, and:

- **qBittorrent off:** its client in Settings → Connections is switched off (not deleted),
  so nothing new is sent to it. Its settings and torrents stay in appdata: set it back to
  `true` and it picks up where it left off, client switched back on. You can remove the two
  torrent port entries if you like. Then add your own client (next section).
- **FlareSolverr off:** put your own FlareSolverr's address in **FlareSolverr URL**, or in
  Settings → Connections → FlareSolverr, for example `http://192.168.1.10:8191`.

### Connecting your own download client

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
  [Usenet](configuration.md#usenet-sabnzbd) for categories and indexers. SABnzbd works next
  to the built-in qBittorrent too: you don't have to switch that off to add it.

Arrmada sends each download to the Downloads folder you picked, and creates its own
categories (`arrmada`, `arrmada-tv`, `arrmada-books`, `arrmada-music`).

**Prowlarr:** Settings → Connections → Prowlarr, with its address and API key (Prowlarr →
Settings → General), then **Sync now**.

## GPU (Intel or AMD)

Convert and Whisper subtitles use Intel or AMD graphics through `/dev/dri`. Check that the
server has it: `ls /dev/dri` in the Unraid terminal should list `renderD128`. Then edit
Arrmada, switch on **Advanced View** (top right), and add this to **Extra Parameters**,
after what's already there:

```text
--device=/dev/dri
```

The template doesn't add the device by itself: on a server without it, Docker would refuse
to start the container. `docker exec Arrmada /usr/lib/jellyfin-ffmpeg/vainfo` shows what the
container can see. Intel and AMD cards are both recognised; [GPU](gpu.md#intel-and-amd-graphics)
has the details.

An NVIDIA card can do Convert's encoding (NVENC) instead, through the Nvidia-Driver plugin
and `--runtime=nvidia`; AI subtitles stay on the CPU there. [GPU](gpu.md#nvidia-on-unraid)
has the steps, and how to see which encoders Arrmada found.

## Updating

The template runs `ghcr.io/tristenlammi/arrmada:stable-aio`, the latest published release
with qBittorrent and FlareSolverr built in. When a new one is out, Arrmada's **Settings →
Status → Updates** says so, and Unraid's Docker tab shows **update ready** after **Check for
Updates** (it can take a few minutes longer than the Updates card, while the all-in-one
image is built). Click it (or **Update All**) to pull the new image; the CA Auto Update
plugin can do it on a schedule. Torrents keep their state across an update: qBittorrent
saves it when the container stops.

Before an update changes the database, Arrmada copies it to `backups/` in its appdata, and it
takes a nightly backup too ([Backups](backups.md)). To go back a version, edit the container
and set **Repository** to that release's all-in-one tag, for example
`ghcr.io/tristenlammi/arrmada:0.3.0-aio` (releases before the all-in-one image only have the
plain tag, `ghcr.io/tristenlammi/arrmada:0.2.0`, which has no qBittorrent inside). An older
version won't start on a database a newer one has already upgraded: restore the backup from
before the update first ([Backups](backups.md)).

**Installed from an earlier version of this template?** It ran `:stable`, the app on its own,
and keeps doing so: Unraid never changes an installed container's template. Nothing is
wrong with that. To switch to the built-in qBittorrent and FlareSolverr, edit Arrmada, set
**Repository** to `ghcr.io/tristenlammi/arrmada:stable-aio`, and add the two torrent ports
(`56881`, TCP and UDP); or keep your own client and set **Built-in qBittorrent** to `false`.

## Troubleshooting

- **The container won't start: "port is already allocated".** Something else has that host
  port. Edit Arrmada and change it ([A port that's taken](#install-from-community-apps)).
- **Torrents download but hardly upload.** The torrent port isn't reachable from outside:
  forward it (TCP and UDP) on your router to the Unraid server, and check the number is the
  same in all three places.
- **"permission denied" on a library folder.** One disk's copy of a share folder is owned by
  root. Fix it through the share path: `chown -R 99:100` and `chmod -R u+rwX,g+rwX` on the
  library's parent folders.
- **Downloads finish but never import** (your own client). Arrmada can't see where the client
  saved them. The client's card in Settings → Connections names the path; give the client
  `/storage` too, or add the path mapping it suggests.
- **qBittorrent's page doesn't take the login.** If you changed the password in qBittorrent,
  use yours. If it's lost: stop Arrmada, delete `qbittorrent/qBittorrent/config/qBittorrent.conf`
  in its appdata, and start it again. A new config and login are made, and Arrmada uses the
  new login by itself; the torrent list (`qbittorrent/qBittorrent/data`) is kept, but
  qBittorrent's other settings go back to Arrmada's defaults.
- **The qBittorrent card says "invalid username or password".** The password was changed in
  qBittorrent: enter it on the card (**Edit**).
- More in [Troubleshooting](troubleshooting.md).
