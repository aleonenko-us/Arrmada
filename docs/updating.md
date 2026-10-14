# Updating Arrmada

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
4. **Gets the new version** — downloads the published image named by `ARRMADA_IMAGE` in
   `.env`, or compiles the code when there isn't one.
5. **Keeps the version that was running** as the Docker image `arrmada:previous`.
6. **Restarts only the app** and waits until it answers. qBittorrent, FlareSolverr and
   Prowlarr keep running.
7. **Goes back by itself if the new version doesn't start** (see below).

A new version may upgrade the database's tables when it starts. Before any such change the
app takes its own snapshot (`<data>/backups`, newest 5 kept), and refuses to upgrade if it
can't (free some space, or set `ARRMADA_SKIP_MIGRATION_SNAPSHOT=1` in `.env` to upgrade
without one). It also backs up every night after 04:00 local time (newest 7 kept).

The Dashboard and `/api/health` show the version and commit that's running, and Settings →
Status says when a newer release is out.

### Options

| | |
|---|---|
| `-y` | Don't ask (also `ARRMADA_UPDATE_FORCE=1`). A run with no terminal — a scheduled one — doesn't ask either. |
| `--no-backup` | Skip the pre-update backup |
| `--force-local` | Don't pull; compile the code that's in the folder (even with `ARRMADA_IMAGE` set) |
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

New installs download the published image (`ARRMADA_IMAGE=ghcr.io/tristenlammi/arrmada:stable`
in `.env`), which takes minutes. Without that line, `update.sh` compiles the app on your
server, which takes a long time (the subtitle and GPU tooling are built from source).

To switch an existing install to the published image, add the line to `.env`:

```sh
ARRMADA_IMAGE=ghcr.io/tristenlammi/arrmada:stable
```

`stable` is always the newest release; pin one with a version instead
(`ghcr.io/tristenlammi/arrmada:0.18.0`). Delete the line to go back to compiling, or run
`./update.sh --force-local` to compile once without changing `.env`.

## Other deploy tools

If you deploy with Komodo or another tool instead of `update.sh`, mirror these steps there:
pass `ARRMADA_VERSION` (a release tag, or the commit date:
`git log -1 --format=%cd --date=format:%Y.%m.%d`) and `ARRMADA_COMMIT`
(`git rev-parse --short HEAD`) as build environment, run
`docker exec -u <PUID>:<PGID> Arrmada-app arrmada backup --kind pre-update` and tag the
running image `arrmada:previous` before each build. Otherwise none of this protection applies.

To run the published image from such a tool, set the `arrmada-app` service's `image:` to the
published tag in your `docker-compose.override.yml` and let the tool pull it.

`docker exec Arrmada-app arrmada help` lists the maintenance commands built into the app
(`version`, `backup`, …). With no command, `arrmada` is the server.
