<div align="center">

<img src="web/public/icon-512.png" alt="" width="96" height="96">

# Arrmada

**Your whole media server in one app — movies, TV, books and requests, without the `*arr` stack.**

[Install](#install) · [Features](docs/features.md) · [Docs](#documentation) · [Issues](https://github.com/tristenlammi/Arrmada/issues)

<img src="docs/images/series-detail.png" alt="A TV series in Arrmada: seasons, episodes and what it's searching for" width="900">

</div>

Arrmada finds, downloads and organises your movies, TV, books and audiobooks, lets your
household request what they want to watch, and keeps your Plex or Jellyfin library tidy — with one
database and one web page instead of half a dozen apps. qBittorrent comes with it, already
set up.

> **Early days:** Arrmada is under active development (v0.x). Expect rough edges, and keep
> backups.

## What it does

- 🎬 **Movies and TV** — search, download, import, rename and upgrade, the way Radarr and Sonarr do. Anime numbering included.
- 📚 **Books and audiobooks** — ebooks and audiobooks with series tracking, plus a built-in audiobook server for listening apps like Lissen.
- 🙋 **Requests** — family and friends browse and request titles, Overseerr-style, and get told when they're ready. Sign in with Plex or Jellyfin.
- 💬 **Subtitles** — pulls them out of the file, downloads them, or writes them with local AI.
- 🗜️ **Convert** — shrinks wasteful video overnight to HEVC or AV1, checking every result against the original.
- 📺 **Plex and Jellyfin** — connect either or both: Watch buttons, library updates after every change, and Insights — who's watching what, history and stats, Tautulli-style.
- 🛟 **Safety nets** — hardlinked imports, a recycle bin, nightly backups and one-step rollbacks.

[See everything it does →](docs/features.md)

## Install

You need a Linux server with Docker and Docker Compose (on Unraid: the **Docker Compose
Manager** plugin). On the server, run:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh
```

It asks where to keep Arrmada's files and where your media lives (press Enter to accept the
suggestions), then starts everything. Open the address it prints —
**`http://<your-server>:7878`** — create your admin account, and a short setup walks you
through the rest in the browser: your free TMDB key, your library folders, your media server (Plex, Jellyfin or both) and indexers.

[Install guide, options and Unraid notes →](docs/install.md)

### Prefer Docker Compose?

In an empty folder on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/docker-compose.example.yml -o docker-compose.yml
```

Edit the lines marked `←` (your media folder, the user and group, your timezone, the port),
then run `docker compose up -d`, open **`http://<your-server>:7878`** and follow the setup.
To update: `docker compose pull && docker compose up -d`. That skips the installer's
pre-update backup and automatic roll-back; the app still copies its database before any
upgrade of it. [More →](docs/install.md#docker-compose-without-the-installer)

## Update

Run the same line again:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh
```

It backs up the database, updates, and checks Arrmada comes back — if the new version doesn't
start, it goes back to the one you had. (In Arrmada's folder, `sh update.sh` does the same.)
[More about updating →](docs/updating.md)

## Screenshots

<table>
<tr>
<td><img src="docs/images/discover.png" alt="Discover: requests on the way and titles ready to watch"></td>
<td><img src="docs/images/movies.png" alt="The movie library"></td>
</tr>
<tr>
<td><img src="docs/images/requests.png" alt="Requests waiting for approval and on their way"></td>
<td><img src="docs/images/activity.png" alt="The download queue"></td>
</tr>
<tr>
<td><img src="docs/images/quality-profile.png" alt="Describing the file you want in a quality profile"></td>
<td><img src="docs/images/setup-wizard.png" alt="The first-run setup"></td>
</tr>
</table>

<p align="center">
<img src="docs/images/phone-discover.png" alt="Discover on a phone" width="260">
&nbsp;&nbsp;
<img src="docs/images/phone-requests.png" alt="Requests on a phone" width="260">
</p>

## Requirements

- A 64-bit Intel or AMD (x86-64) server running Linux — Unraid, Debian, Ubuntu and the like.
- Docker with Compose v2.
- A free [TMDB API key](https://www.themoviedb.org/settings/api) (entered in the app).
- Optional: Intel or AMD graphics for faster Convert and AI subtitles, and a Plex or Jellyfin server.

## Documentation

| | |
|---|---|
| [Install](docs/install.md) | The one-liner, its options, Unraid, the git checkout way |
| [Updating](docs/updating.md) | Updates, rollbacks, published images |
| [Configuration](docs/configuration.md) | Ports, `.env`, media folders and hardlinks, GPU, recycle bin, reverse proxies |
| [Backups](docs/backups.md) | Backups, restoring, a forgotten admin password |
| [Troubleshooting](docs/troubleshooting.md) | Common problems and fixes |
| [Features](docs/features.md) | Everything Arrmada does |
| [Development](docs/development.md) | Building and testing it yourself |
| [Releasing](docs/releasing.md) | For maintainers |

## Contributing

Bug reports and ideas are welcome in [Issues](https://github.com/tristenlammi/Arrmada/issues).
For code, see [Development](docs/development.md) — please open an issue before a big change.

## License

[MIT](LICENSE) © 2026 Tristen Lammi
