# Updating Arrmada

Installed from Unraid's Community Apps template? Update from Unraid's Docker tab instead;
nothing on this page applies ([Arrmada on Unraid → Updating](unraid.md#updating)).

## The one line

Run the install line again on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh
```

It finds your install and updates it. Or run the updater in Arrmada's folder directly —
it's the same thing:

```sh
sh /mnt/user/appdata/arrmada/update.sh      # wherever Arrmada is installed
./update.sh                                 # from inside the folder (git checkouts too)
```

## One-click updates (optional)

Settings → Status → Updates can install a new release for you: an **Update to vX.Y.Z** button,
and an **Install updates automatically** switch. It does exactly what the install line does:
backs up the database, brings the release, restarts the app, and goes back to the version it
had if the new one doesn't start. The page follows it and reloads into the new version.

It is off until you ask for it, because it needs one thing the app never has: access to Docker.
Arrmada itself never touches Docker. A small separate container (`arrmada-updater`) holds
Docker's socket, which is root on the server, and does nothing but run `update.sh` when the app
asks. It publishes no port: the app reaches it on the private Compose network with a random
token that `install.sh` writes to `.env`. Turn it on once, on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh -s -- --with-updater
```

If it says the install's files are older than the updater, run the plain install line first (it
updates), then this one again. To turn it off, remove `updater` from `COMPOSE_PROFILES` in
`.env` and run `docker compose rm -sf arrmada-updater`.

**Automatic updates** are a separate switch, off by default, and only available with the
updater. When on, Arrmada installs a new release between 2 and 6 am (the server's time), at
least a day after it was published, so a release that is pulled or fixed quickly never reaches
you on its own. It only follows published releases, never `main`, and it tries each release once:
if an update fails, the next release is a new try. Updates the app starts are written to the log
(`update: started from the web`, `automatic update: started`).

Not for Unraid's own Docker manager (it updates the container from its Docker page) or for an
install that follows `main`.

## Stable or main

`ARRMADA_CHANNEL` in `.env` says what an update brings:

- **`stable`** (every new install) — the latest published release, downloaded from
  `ghcr.io/tristenlammi/arrmada:stable` (or the image `ARRMADA_IMAGE` names). It takes
  minutes, and it never goes backwards: a server running newer code than the latest release
  keeps it until a release includes it, and a release never starts on a database a newer
  version has already upgraded.
- **`main`** — the newest code on `main`, compiled on the server on every update. See
  [Following main](#following-main-developers).

An install from before the setting gets it on its next update, by itself. One that already
downloads the published image (`ARRMADA_IMAGE` in `.env`) gets `ARRMADA_CHANNEL=stable`. One
that compiles here moves to the published releases if the latest release includes the code
it's running: the update downloads the release, takes the usual backup, starts it, and then
writes `ARRMADA_CHANNEL=stable` and `ARRMADA_IMAGE=…:stable` to `.env` (the `.env` from before
is kept as `.env.bak-<date>`). If it's running newer code than the latest release — it was
compiled from `main` since then — nothing changes: it says so, keeps the build it has, and
every update looks again. Nothing ever puts an install on `main` by itself.

## Following main (developers)

Add one line to `.env` in Arrmada's folder:

```sh
ARRMADA_CHANNEL=main
```

From then on `./update.sh` pulls `main` and compiles it (the better part of an hour each
time, and [a few GB of memory](troubleshooting.md#the-build-fails-or-gets-killed)), with the
same backup and roll-back as always. Settings → Status then says how many commits behind
`main` the server is. Delete the line or set it to `stable` to go back to the releases: the
next update moves there once a release includes the code you're running.

## Upgrading from v0.1.0 or earlier

Newer versions of `docker-compose.yml` run the image named by `ARRMADA_APP_IMAGE` in `.env`,
and a plain `docker compose up -d` in a folder without that line downloads the published image
instead of compiling. `update.sh` and the install line add the line by themselves the first
time they run, before anything is rebuilt:

```sh
ARRMADA_APP_IMAGE=arrmada:dev
```

So if you update with `update.sh` or the install line, there's nothing to do: the line is
added, the install gets its channel as [above](#stable-or-main), and `--rollback` works as
before.

**If something else deploys Arrmada** — Komodo, Portainer, or a script running
`git pull && docker compose up -d --build` — add that line to `.env` in Arrmada's folder
**before the next pull**. (If the tool writes `.env` itself, add it to the tool's environment
for the stack instead.) Without it the deploy stops with

```
unable to get image 'ARRMADA_APP_IMAGE is missing from .env, see docs/updating.md (…) -- ghcr.io/tristenlammi/arrmada:stable': … invalid reference format
```

and leaves the running version alone: an install that has always compiled is never switched
to the published image without you choosing it. Add the line and deploy again. (That check
looks for `ARRMADA_PORT`, which every version of the installer has written to `.env`. A `.env`
written by hand without it gets the published image on such a deploy.)

## qBittorrent now asks for a password

Until October 2026 the bundled qBittorrent let anything on the private networks Docker uses
in without a password, and on most home networks that included your own computers. That was
how Arrmada connected to it, but it also let any container (FlareSolverr's browser, which
opens tracker websites, among them) add torrents or change settings.

The first time the new version starts, it gives qBittorrent a password of its own, checks it
can sign in with it, and only then switches the login-free access off. It also turns on
qBittorrent's CSRF protection. Torrents and settings are kept, and there's nothing to do. If
you had already entered qBittorrent's real login on its card in Arrmada, that password is
kept.

What you'll notice: **qBittorrent's own page now asks for a login.** An admin finds it in
Settings → Connections → the qBittorrent card → **Show its login**
([Configuration → qBittorrent's login](configuration.md#qbittorrents-login)). If anything
gets in the way, Arrmada leaves qBittorrent as it was, says why in the log, and tries again
at its next start.

`./update.sh --rollback` afterwards is fine: the older version signs in with the login on
the card. Going back *with the database* is different, because the older database doesn't
have that login: `./update.sh --rollback --with-db`, or saying yes when an update that
didn't start offers to put the database back. Then `update.sh` switches qBittorrent's
login-free access back on for the Docker network it shares with Arrmada (that network only,
never your home network), so the older version can reach it, and says so; your next update
gives it its password again. If it can't, it prints what to do:
enter the login on the card by hand, which you can read with

```sh
docker exec Arrmada-app cat /data/qbittorrent-login.json
```

or follow [Troubleshooting](troubleshooting.md#qbittorrent-login).

## What an update does

1. **Checks for a long conversion.** Restarting abandons a conversion in progress, so if one
   has been running for over two hours it says so and asks first.
2. **Gets the newest files** — `git pull` for a git checkout, a download from GitHub
   otherwise. Your `.env`, `docker-compose.override.yml`, database and media are never
   touched. If the pull fails (local edits, or a branch that has diverged) it stops without
   changing anything; `git status` shows what's in the way.
3. **Backs up the database** with the running app's own `arrmada backup --kind pre-update`
   (`<data>/backups/arrmada-pre-update-*.db`, newest 3 kept). If the backup fails, nothing
   changes.
4. **Gets the new version** — on the stable channel, downloads the latest release (first,
   before anything else changes) and checks it isn't a step back for this server or its
   database; on main, compiles the code.
5. **Keeps the version that was running** as the Docker image `arrmada:previous`.
6. **Restarts only the app** and waits until it answers. qBittorrent, FlareSolverr and
   Prowlarr keep running. The one exception is `ARRMADA_MEDIA_HOST` in `.env`: with it, the
   media folders are made first (and a folder overlapping the database's is refused before
   anything restarts), and when the media folder changed, qBittorrent and SABnzbd are
   restarted on the new one too, so they keep seeing what the app sees.
7. **Goes back by itself if the new version doesn't start** (see below).

A new version may upgrade the database's tables when it starts. Before any such change the
app takes its own snapshot (`<data>/backups`, newest 5 kept), and refuses to upgrade if it
can't (free some space, or set `ARRMADA_SKIP_MIGRATION_SNAPSHOT=1` in `.env` to upgrade
without one). It also backs up every night after 04:00 local time (newest 7 kept).

The Dashboard and `/api/health` show the version and commit that's running, and Settings →
Status says when a newer release is out (on main: how far behind `main` the server is).

### Options

| | |
|---|---|
| `-y` | Don't ask (also `ARRMADA_UPDATE_FORCE=1`). A run with no terminal — a scheduled one — doesn't ask either. |
| `--no-backup` | Skip the pre-update backup |
| `--force-local` | Don't pull; compile the code that's in the folder (even on the stable channel) |
| `--rollback` | Go back to the version that ran before the last update |
| `--rollback --with-db` | …and put back the database from before that update |

## When an update goes wrong

If the new version crashes or never answers, `update.sh` goes back to the version that was
running a moment ago, by itself, and tells you so — Arrmada isn't left down.

The one case it can't fix alone: the new version upgraded the database and *then* failed.
The previous version can't run on the upgraded database, so going back means also putting
back the backup taken at the start of the update — which loses anything Arrmada recorded in
those few minutes. At a terminal it asks you first. Unattended, it leaves Arrmada stopped and
prints the command:

```sh
./update.sh --rollback --with-db
```

### Rolling back by hand

`./update.sh --rollback` starts the previous version again in a few seconds (the one you
leave is kept as `arrmada:rolled-back`, and the next `./update.sh` goes forward again). If the
previous version doesn't come up, the script starts the newer one again rather than leave
Arrmada down. Keeping the old image costs one extra image of disk, several GB with the
subtitle and GPU tooling.

An older version won't start on a database a newer one has upgraded: it stops with a message
saying so, because running old code on tables it doesn't know can damage them. So before it
stops anything, `--rollback` asks the previous version whether it can run on the database as
it is, and if the update changed the database it stops and says to use
`./update.sh --rollback --with-db`. That also puts back the pre-update backup, which means
everything Arrmada recorded since the update is lost, so it asks first (`-y` skips the
question). If the previous version can't restore a backup itself, the script changes nothing
and prints the steps to do it by hand. `ARRMADA_ALLOW_NEWER_SCHEMA=1` in `.env` starts an
older version on a newer database anyway; it's a last resort.

The first rollback after updating an install from before this tooling is the exception: that
version has no such check and can't restore a backup, and the update couldn't take a
pre-update backup with it. `--rollback` warns about this before it changes anything. Put the
database back by hand first: stop the app (`docker compose stop arrmada-app`), copy the newest
`<data>/backups/arrmada-pre-migrate-*.db` (taken by the new version just before it upgraded
the tables) over `<data>/arrmada.db`, delete `arrmada.db-wal` and `arrmada.db-shm` next to
it, then run `./update.sh --rollback`.

## Published image or compiled here

On the stable channel `update.sh` downloads `ARRMADA_IMAGE`, which is
`ghcr.io/tristenlammi/arrmada:stable` (always the newest release) unless you pin one with a
version (`ghcr.io/tristenlammi/arrmada:0.18.0`). On main it compiles the app on your server,
which takes a long time (the subtitle and GPU tooling are built from source).
`./update.sh --force-local` compiles once without changing `.env`. Either way
`ARRMADA_APP_IMAGE` stays `arrmada:dev`: that's the local name `update.sh` gives whatever it
built or downloaded, and the one `--rollback` swaps.

Compiling needs a few GB of free memory (see
[Troubleshooting](troubleshooting.md#the-build-fails-or-gets-killed)); the published image
needs none.

## Plain `docker compose`

Installed with the [one-file compose](install.md#docker-compose-without-the-installer)?
Update in its folder with:

```sh
docker compose pull && docker compose up -d
```

From a git checkout started with just `docker compose up -d` (no installer, no `.env`), pull
the code first: `git pull && docker compose pull && docker compose up -d`.

There's no pre-update backup or automatic roll-back this way (the app still snapshots the
database before any schema change). In a checkout you can get them by running `./install.sh`
in the folder once: it keeps the database where it is, writes `.env`, and from then on
`./update.sh` updates.

## Other deploy tools

If you deploy with Komodo or another tool instead of `update.sh`, keep
`ARRMADA_APP_IMAGE=arrmada:dev` in `.env` (see
[Upgrading from v0.1.0 or earlier](#upgrading-from-v010-or-earlier)) and mirror these steps
there: pass `ARRMADA_VERSION` (a release tag, or the commit date:
`git log -1 --format=%cd --date=format:%Y.%m.%d`) and `ARRMADA_COMMIT`
(`git rev-parse --short HEAD`) as build environment, run
`docker exec -u <PUID>:<PGID> Arrmada-app arrmada backup --kind pre-update` and tag the
running image `arrmada:previous` before each build. Otherwise none of this protection applies.

To run the published image from such a tool instead, set
`ARRMADA_APP_IMAGE=ghcr.io/tristenlammi/arrmada:stable` in `.env` and have the tool run
`docker compose pull` before `docker compose up -d`. (`update.sh` refuses to run on that
setting, since it manages `arrmada:dev`; use `ARRMADA_IMAGE` for update.sh's own pull mode.)

`docker exec Arrmada-app arrmada help` lists the maintenance commands built into the app
(`version`, `backup`, …). With no command, `arrmada` is the server.
