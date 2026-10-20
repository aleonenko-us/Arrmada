# UPD — One-click and automatic updates

_Part of the [Arrmada roadmap](../../ROADMAP.md). 6 tasks (5 built, 1 open). After each task: priority, size, and the phase it ships in._

**Goal.** Updating Arrmada is one click in Settings → Status, and optionally happens by itself at night. It runs the same `update.sh` as the install line, so the database backup, the `arrmada:previous` rollback image and the automatic go-back are the same. It is opt-in per server, and automatic updates are a second opt-in on top.

**Why.**
- **The Updates card only told people what to type.** When a release was out it showed the install line and nothing else, so updates waited for someone to open a terminal. On a NAS (ZimaOS, Unraid) that's an extra hurdle.
- **The app can't run it itself, by design.** Updating means `docker compose up` on the host, and the app container has no Docker access. Mounting the Docker socket into the app would give the web app root on the server, so that was ruled out.
- **Some people want this and some don't.** Anyone who doesn't gets exactly what they have today: no new container, no socket, no button.

#### Design

## One small container holds the socket
`arrmada-updater` (compose profile `updater`, off by default) is the only thing given `/var/run/docker.sock`. It is Docker's own `docker:27-cli` image running busybox `httpd` with one CGI script, `docker/updater/updater.cgi`, mounted read-only. It publishes no port. The app reaches it at `http://arrmada-updater:8080` on the private Compose network.

| Request | Does |
|---|---|
| `GET /cgi-bin/updater?status` | `{state: idle \| running \| done \| failed, started_at, log}`, the log being the last 30 lines of `update.sh`'s output |
| `POST /cgi-bin/updater?apply` | Starts `update.sh -y` detached and answers 202 at once (the app is restarted part-way through). 409 if one is running |

- **Auth:** every request needs `Authorization: Bearer $ARRMADA_UPDATER_TOKEN`. With no token set it refuses everything (503).
- **The same path:** the install folder is mounted into the updater at the same absolute path it has on the host (`ARRMADA_HOST_DIR`), so `update.sh`'s relative paths and `docker compose` bind mounts mean the same thing to the Docker daemon as to the script.
- **No new image to publish:** the script is a mounted file on a stock image, so there is no extra release artifact.

## The app only asks
`internal/updater` is a small client (`Apply`, `Progress`) built from `ARRMADA_UPDATER_URL` and `ARRMADA_UPDATER_TOKEN`; with either empty there is no client and everything behaves as before. `GET /api/v1/system/status`'s `update` block gains `apply` (`ready`, `needs_setup`, or absent on Unraid) and `auto_update`.

| Route (admin only) | Does |
|---|---|
| `POST /api/v1/system/update/apply` | 202 once the updater has started it. 409 when there's nothing newer, no updater, Unraid, or one already running |
| `GET /api/v1/system/update/progress` | The updater's state and log |
| `PUT /api/v1/system/update/auto {enabled}` | The automatic-updates switch. Turning it on needs the updater (409 without) |

## Automatic updates
Off by default (`auto_update_enabled`). A scheduled task (`auto-update`, every 30 minutes) calls `updater.AutoDecision`, which says yes only when all of these hold: the switch is on, the updater is set up, the install follows the published releases (never `main`, never a dev build, never Unraid), a newer release is known, it has been out at least 24 hours, the server's local hour is 2 to 5, and the release isn't the last one already started (`auto_update_last_tag`, so a failing release isn't retried every half hour). Each reason to wait is logged at debug level.

## The page follows the update
After Update now the card polls the updater (through the app) until the app stops answering, then polls `/api/health` until it returns a different version and reloads. If the app comes back on the same version, the card says so and shows the log, so "finished" is never claimed for an update that changed nothing.

## Task list

<a id="upd-01"></a>
- [x] **UPD-01 · The updater container and its script** — `P1` · `M` · Phase TBD
  - **Files:** `docker/updater/updater.cgi`, `docker-compose.yml`, `docker-compose.example.yml`
  - **Acceptance:** an unauthenticated or wrong-token request gets 401; no token set gets 503; `apply` starts `update.sh` and a second `apply` while it runs gets 409; `status` reports idle, running, done and failed with the log.
  - **Tests:** Go `updater.TestUpdaterCGI` runs the script with a stand-in `update.sh` (all of the above). Also run by hand under real busybox `httpd`.

<a id="upd-02"></a>
- [x] **UPD-02 · `install.sh --with-updater`** — `P1` · `S` · Phase TBD
  - **Approach:** on a fresh install or an existing one, writes `ARRMADA_UPDATER_TOKEN` (random, kept on later runs), `ARRMADA_UPDATER_URL` and `ARRMADA_HOST_DIR` to `.env` and adds `updater` to `COMPOSE_PROFILES`. An install whose files predate the updater is told to update first.
  - **Files:** `install.sh`
  - **Acceptance:** after the option, `docker compose --profile updater up -d` starts the updater and the app has the URL and token.

<a id="upd-03"></a>
- [x] **UPD-03 · The app's client and routes** — `P1` · `M` · Phase TBD
  - **Files:** `internal/updater/`, `internal/httpapi/system_update.go`, `internal/httpapi/server.go`, `internal/updatecheck/updatecheck.go`, `internal/httpapi/testdata/routes.golden`, `cmd/arrmada/main.go`
  - **Tests:** Go `updater` tests (client against a fake updater: accepted, busy, refused token, unreachable, nil client); `httpapi` tests for each route's refusals (no updater, nothing newer, Unraid, busy, unreachable) and the status fields.

<a id="upd-04"></a>
- [x] **UPD-04 · The Updates card** — `P1` · `M` · Phase TBD
  - **Approach:** an 'Update to vX.Y.Z' button, an inline confirmation that says what will happen, a progress view with the log, 'Try again' on failure, the setup line when the updater isn't there, and the automatic-updates switch (disabled with a hint until the updater exists).
  - **Files:** `web/src/components/system/UpdateCheck.tsx`, `web/src/lib/useUpdateRun.ts`, `web/src/lib/updateStatus.ts`, `web/src/lib/api.ts`, `web/src/lib/systemApi.ts`
  - **Tests:** vitest for `canUpdateNow`, `offerUpdaterSetup` and the hint; checked by hand against a local build with a stand-in updater through a full update, including the reload into the new version.

<a id="upd-05"></a>
- [x] **UPD-05 · Automatic updates, off by default** — `P2` · `S` · Phase TBD
  - **Files:** `internal/updater/updater.go` (`AutoDecision`), `cmd/arrmada/main.go` (the `auto-update` task)
  - **Tests:** Go `TestAutoDecision` (table: every reason to wait, the quiet-hours edges at 02:00 and 06:00, the 24-hour hold, an already-started tag, the commit-build channel).

<a id="upd-06"></a>
- [ ] **UPD-06 · Follow-ups not built** — `P3` · `M` · Phase TBD
  - **Roll back from the card.** `update.sh --rollback` exists; the updater could expose it as `POST ?rollback` and the card a 'Go back to vX' button after a finished update.
  - **Refresh the updater itself.** The updater's own container isn't recreated by an update (that would stop it mid-run), so a changed `updater.cgi` only takes effect the next time someone runs the installer or `docker compose up -d arrmada-updater`. A final detached step in `update.sh` could do it.
  - **File ownership.** `update.sh` run by the updater runs as root, so files it writes in the install folder (`.env` backups, downloaded files on a download-based install) can be root-owned. `update.sh` has its own `.env` permission handling (`tighten_env`), but how that behaves when run as root inside the updater hasn't been checked on a real install, and a chown of the folder to `ARRMADA_PUID:ARRMADA_PGID` at the end is the likely fix.
  - **A real-install test.** The updater container has not been run against a real Docker daemon yet: the script and the full flow were tested with a stand-in `update.sh`, and the compose service, the same-path mount and `update.sh` inside `docker:27-cli` (which has no `git`, so a git-checkout install relies on the `apk add` at start) still need one run on a real server.

#### Risks

- **Docker's socket is root on the server.** That is why the updater is a separate container nobody gets by default, why the app never has the socket, and why the page says so before the owner turns it on. Anything on the Compose network that learns the token could ask for an update, which can only run the install folder's own `update.sh`.
- **An update restarts the app.** The page says so before it starts. Downloads in the bundled qBittorrent keep running; imports resume afterwards.
- **A bad release reaching a server on its own.** The 24-hour hold, the night window, the once-per-release rule and `update.sh`'s database backup and automatic go-back are the guards.

#### Out of scope
- Updating the companions (qBittorrent, Prowlarr, SABnzbd) or Docker itself.
- Installs that follow `main`: those compile on the server and can take most of an hour.
- Unraid's Docker manager, which updates the container from its own page.
