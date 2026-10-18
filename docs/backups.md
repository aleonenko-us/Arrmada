# Backups and restoring

Arrmada copies its database into `<data>/backups` — on a default install,
`/mnt/user/appdata/arrmada/data/backups` on Unraid or `~/arrmada/data/backups` elsewhere:

| Kind | When | Kept |
|---|---|---|
| Nightly | once a day after 04:00 server time (changeable), or at once after a long downtime | newest 7 (changeable) |
| Before update | before any schema change | newest 5 |
| Pre-update | when `update.sh` runs | newest 3 |
| Manual | **Back up now** | newest 10 |
| Before restore, Before user delete, Uploaded | automatically | newest 3 each |

Admins manage them in **Settings → System → Backups**: see every copy with its size and
schema version, take one now, change the nightly schedule, delete one, or **Download** it as a
`.db.gz` (decompress it with `gunzip` to get a plain SQLite file). The copies sit on the same
disk as the database, so they cover a bad update, corruption or a mistake, not a failed disk;
download one now and then to keep a copy somewhere else. Backups contain your API keys, the
Plex token and password hashes, so keep downloaded copies somewhere private.

For the same reason the database (`arrmada.db` and its `-wal`/`-shm`) and every backup are
readable only by the user Arrmada runs as (`ARRMADA_PUID` in `.env`): files are `0600` and the
`backups` folder `0700`, and an install from before this is tightened the next time Arrmada
starts. `update.sh` isn't affected (its backups and restores run inside the container). To copy
a backup off the server by hand, do it as that user or with `sudo`, or use **Download**.

## Restoring

**Restore** on a row puts that backup back (type `RESTORE` to confirm). The backup is checked
first (a damaged file, or one from a newer Arrmada, is refused), then staged, and swapped in
the next time Arrmada starts — never while it's running. Inside Docker the app restarts itself
straight away; otherwise restart the container (`docker restart Arrmada-app`), or cancel the
staged restore on the card. The database it replaces is kept as a "Before restore" backup, and
if the swap fails at start-up Arrmada starts on the database as it was and the card says why.
Everything since the backup was taken is lost: requests, watch history, listening places,
users and settings.

**Restore from file…** takes a `.db`, or a `.db.gz` from Download (up to 4 GB). It's checked
the same way, listed as "Uploaded", and restored with that row's Restore. Behind Cloudflare,
request bodies over 100 MB are refused, so upload big backups from your home network.

### When Arrmada won't start

The same restore works from the command line, in Arrmada's folder, without the web UI. It
only stages the backup; the swap happens when Arrmada next starts:

```sh
docker compose run --rm --no-deps arrmada-app backups          # list them
docker compose run --rm --no-deps arrmada-app restore arrmada-nightly-20261008T040012Z.db
docker compose up -d arrmada-app
```

If the container is running, `docker exec Arrmada-app arrmada restore <name>` followed by
`docker restart Arrmada-app` does the same. `restore` also takes a path to a `.db` or `.db.gz`
the container can see (it's copied into the backups folder first), and
`restore --cancel` drops a staged restore that hasn't run.

## Locked out?

If you've forgotten the admin password, set a new one from the server:

```sh
docker exec -it Arrmada-app arrmada reset-password you@example.com
```

It prints a new password once, in your terminal only (never in a log), and signs that
account out everywhere. To choose the password yourself, pipe it in with
`--password-stdin`. A name that matches no account lists the admin accounts. Anyone with
shell access to the server can do this, the same as they could edit the database.

Lost the phone with your authenticator app, and the recovery codes too? Turn two-factor
sign-in off for the account, then sign in with the password and set it up again:

```sh
docker exec -it Arrmada-app arrmada reset-2fa you@example.com
```

The password and the account's sessions stay as they are. An admin can do the same for
anyone else in Settings → People & access.

If the server requires two-factor for admins (Settings → People & access → **Require
two-factor for admins**), signing in will ask you to set it up again straight away. Without
an authenticator app to hand, switch that requirement off as well and restart Arrmada:

```sh
docker exec -it Arrmada-app arrmada reset-2fa you@example.com --policy-off
docker restart Arrmada-app
```

**Keep backups private.** A backup is a copy of the whole database, so it holds what the
database holds: the keys and passwords of your connections and every account's two-factor
secret. Store and share it as carefully as you would those.
