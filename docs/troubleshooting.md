# Troubleshooting

**The installer says Docker or Compose is missing.**
On Unraid, switch Docker on in Settings → Docker and install the **Docker Compose Manager**
plugin from Apps. Elsewhere, install [Docker Engine](https://docs.docker.com/engine/install/)
and the `docker-compose-plugin` package. "Can't talk to Docker" means the daemon isn't
running or your user isn't allowed to use it — run the installer as root (`… | sudo sh`).

**Arrmada didn't start.**
The installer and updater print the last lines of the log. The full log is in Arrmada's folder:
`docker compose logs arrmada-app`. A failed *update* goes back to the version that was
running by itself ([Updating](updating.md#when-an-update-goes-wrong)).

<a id="the-build-fails-or-gets-killed"></a>
**The build fails or gets killed.**
Compiling the image yourself (`--channel main`, `docker compose up -d --build`, or `update.sh`
with `ARRMADA_CHANNEL=main`) takes 30–60 minutes and about **2 GB of memory free** while it
runs; 4 GB is comfortable. If a compiler is still killed, the build ends with
`✗ This machine ran out of memory building Arrmada` and what to do.

Versions up to v0.1.0 needed far more — about 7 GB on a four-core machine — and on a smaller
one Docker printed only the last line, for example

```
failed to solve: process "/bin/sh -c CGO_ENABLED=0 go build ..." did not complete successfully: exit code 1
```

with the real cause (`signal: killed`, `Killed signal terminated program cc1plus`,
`cannot allocate memory`) further up the log.

Either way you don't need to compile: run the published image. With the installer, run the
install line without `--channel main` (or `--build`), or put `ARRMADA_CHANNEL=stable` in
`.env` and run `sh update.sh`. With a checkout and no `.env`, plain `docker compose up -d`
already downloads it. To compile on a small machine anyway, add swap, or pass the build
argument `ARRMADA_BUILD_JOBS=1`.

On ARM, `no matching manifest for linux/arm64` means an image from before ARM was published
(v0.2.0 or older); the installer compiles instead, and the next release downloads. On a
32-bit OS (an older Raspberry Pi OS, even on a 64-bit Pi) nothing runs: install the 64-bit OS.
See [Install → ARM](install.md#arm).

**I can't open the web page.**
Use the address the installer printed — it may not be 7878 if something else had that port
(see `ARRMADA_PORT` in `.env`). On another device, use the server's LAN address, not
`localhost`.

**I forgot the admin password.**
`docker exec -it Arrmada-app arrmada reset-password you@example.com` — see
[Backups → Locked out?](backups.md#locked-out).

**I lost the phone with my two-factor codes.**
Use one of your recovery codes on the sign-in page (**Use a recovery code**). Without those,
an admin can reset it in Settings → People & access, or run
`docker exec -it Arrmada-app arrmada reset-2fa you@example.com` — see
[Backups → Locked out?](backups.md#locked-out).

**Every two-factor code is refused, though the app shows it.**
Codes depend on the time. Arrmada accepts the code for the current 30 seconds and the ones
either side, so the server's clock and the phone's may differ by about 45 seconds at most.
Check the host's clock is set automatically (NTP: `timedatectl` on Linux shows
"System clock synchronized: yes") and the phone's time is set automatically. After several
wrong codes the sign-in page asks you to wait a little before trying again; that count is
kept in memory, so restarting Arrmada clears it.

<a id="qbittorrent-login"></a>
**qBittorrent's page asks for a username and password.**
Arrmada gave it a password ([Configuration → qBittorrent's login](configuration.md#qbittorrents-login)).
An admin finds it in Settings → Connections → the qBittorrent card → **Show its login**.

**The qBittorrent card says "invalid username or password", or the log says qBittorrent asks
for a password Arrmada doesn't have.**
The password was changed in qBittorrent, or Arrmada's data folder was replaced without
`qbittorrent-login.json`. If you know the password, enter it on the card (**Edit**). If not,
let Arrmada set a new one: in Arrmada's folder run

```sh
docker compose stop arrmada-qbittorrent
docker compose run --rm --no-deps arrmada-qbittorrent-init sh /seed/reopen.sh
docker compose start arrmada-qbittorrent
docker compose restart arrmada-app
```

That switches login-free access back on for a moment, for the Docker network Arrmada and
qBittorrent share and nothing else (never your home network); at its start Arrmada gives
qBittorrent a password again (the saved one, or a new one) and switches it off. Torrents
and settings are kept.

**Settings → Status warns "The bundled qBittorrent lets anything on … in without a password".**
Its subnet whitelist covers whole networks. If it's the one it was first installed with
(`10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16`), Arrmada replaces it with a password at its
start, and the log says why it couldn't (often qBittorrent hadn't started yet: restart
Arrmada). If you set it yourself, narrow it in qBittorrent (Tools → Options → WebUI) or
switch it off.

**qBittorrent's page doesn't open from another computer.**
A new install publishes it on the server itself only
([Configuration → qBittorrent's own page](configuration.md#qbittorrents-own-page)).

**Downloads fill up `docker.img` on Unraid.**
The install is using managed storage. Mount your share instead: run the install line again
with `--media /mnt/user/data` (your share), then pick each library in Settings → Library.

**Imports copy instead of hardlinking.**
Downloads and libraries must be under one mounted folder, and on Unraid on the same disk
([Configuration → Hardlinks](configuration.md#hardlinks)).

**"Permission denied" on a library folder that used to work (Unraid).**
One disk's copy of the folder is owned by root. Through the share path: `chown 99:100` and
`chmod 775` on the library parent folders.

**Schedules run at the wrong time.**
Set `TZ` in `.env` to your Region/City timezone and run `sh update.sh`.

**The update check says a new release is out.**
Run the install line again (or `sh update.sh`). Settings → Status has the release notes; the
check reads GitHub's public list of releases every 12 hours, sends nothing about your server
but Arrmada's version,
and can be switched off on the same card.

**Something else?**
[Open an issue](https://github.com/tristenlammi/Arrmada/issues). The **Logs** page can download
a support bundle (logs, status and settings with credentials, people and book names taken
out) to attach.
