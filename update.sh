#!/usr/bin/env sh
# Arrmada updater — pull the latest code and rebuild in place.
#
#   ./update.sh                       back up the database, pull, rebuild and restart the app
#   ./update.sh --force-local         skip the pull and rebuild the code that's here
#   ./update.sh --rollback            go back to the build that was running before the last update
#   ./update.sh --rollback --with-db  ...and put back the database from before that update
#
# Your .env, database, and media volumes are preserved. Run it any time to upgrade
# or to apply changes you made to .env. Only the app image is rebuilt; the companions
# (qBittorrent, FlareSolverr, and Prowlarr if you opted in) keep running untouched.
# The build that was running before is kept as the image arrmada:previous, so
# --rollback has something to go back to — and if the new build doesn't start, the
# script goes back to it by itself.
#
# An install made by the one-line installer without git (it has .arrmada-source) gets its
# newer files as a download instead of a git pull; everything else is the same.
#
# Pull mode (opt-in): with ARRMADA_IMAGE set in .env to a published image, for example
#   ARRMADA_IMAGE=ghcr.io/tristenlammi/arrmada:stable
# the update downloads that image instead of compiling, which takes minutes rather than
# the better part of an hour. Everything else (the backup, arrmada:previous, --rollback)
# works the same. --force-local still compiles the code in this folder.
set -eu
cd "$(dirname "$0")"

say() { printf '%s\n' "$*"; }

usage() {
  say "Usage: ./update.sh [--force-local] [--no-backup] [-y]"
  say "       ./update.sh --rollback [--with-db] [-y]"
  say ""
  say "  (no options)    back up the database, pull the latest code, rebuild the app and restart it"
  say "                  (with ARRMADA_IMAGE in .env: download that published image instead of rebuilding)"
  say "  --force-local   don't pull; rebuild the code that's in this folder (even with ARRMADA_IMAGE set)"
  say "  --no-backup     don't take the pre-update database backup"
  say "  --rollback      start the build that ran before the last update again"
  say "  --with-db       with --rollback: also put back the database backup taken before that update"
  say "  -y, --yes       don't ask for confirmation (also: ARRMADA_UPDATE_FORCE=1)"
}

PULLED=""
FORCE_LOCAL=""
ROLLBACK=""
WITH_DB=""
NO_BACKUP=""
YES=""
# Set while the script goes back on its own after an update that didn't start.
AUTO_ROLLBACK=""
for _arg in "$@"; do
  case "$_arg" in
    --pulled) PULLED=1 ;;
    --force-local) FORCE_LOCAL=1 ;;
    --rollback) ROLLBACK=1 ;;
    --with-db) WITH_DB=1 ;;
    --no-backup) NO_BACKUP=1 ;;
    -y | --yes) YES=1 ;;
    -h | --help) usage; exit 0 ;;
    *) say "✗ Unknown option: $_arg" >&2; usage >&2; exit 2 ;;
  esac
done
# For unattended runs that pass no options: the same as -y.
if [ "${ARRMADA_UPDATE_FORCE:-}" = 1 ]; then
  YES=1
fi
if [ -n "$WITH_DB" ] && [ -z "$ROLLBACK" ]; then
  say "✗ --with-db only goes with --rollback." >&2; usage >&2; exit 2
fi

# The user the app runs as. Commands run inside the container use it too, so any file
# they write in the data folder is the app's, not root's.
RUN_UID=$(grep -E '^ARRMADA_PUID=' .env 2>/dev/null | cut -d= -f2 | tr -d '\r" ')
RUN_GID=$(grep -E '^ARRMADA_PGID=' .env 2>/dev/null | cut -d= -f2 | tr -d '\r" ')
RUN_AS="${RUN_UID:-1000}:${RUN_GID:-1000}"

# Where update.sh notes the name of the database backup it took before the last update,
# which is the one --rollback --with-db puts back.
PREVIOUS_DB_FILE=.arrmada-previous-db

# app_answers -> 0 if the app answers its health check right now. It asks from inside
# the container, so it works whatever host port was picked and needs no curl on the host.
app_answers() {
  docker exec Arrmada-app curl -fsS -o /dev/null "http://127.0.0.1:7878/api/health" >/dev/null 2>&1
}

# health_json prints the app's health answer (one line of JSON), or nothing.
health_json() {
  docker exec Arrmada-app curl -fsS "http://127.0.0.1:7878/api/health" 2>/dev/null || true
}

# json_str KEY reads a string field from one line of JSON on stdin (no jq on Unraid).
json_str() {
  sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"
}

# json_num KEY reads a whole-number field from one line of JSON on stdin.
json_num() {
  sed -n "s/.*\"$1\":\([0-9][0-9]*\).*/\1/p"
}

# Where an install downloaded without git (install.sh writes .arrmada-source then) gets
# its newer files. Overridable for forks and the installer's own tests.
REPO="tristenlammi/Arrmada"
API_URL="${ARRMADA_API_URL:-https://api.github.com/repos/$REPO}"
TARBALL_BASE="${ARRMADA_TARBALL_BASE:-https://codeload.github.com/$REPO/tar.gz}"

# fetch URL -> the body on stdout (curl or wget, whichever is here).
fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1"; else wget -qO- "$1"; fi
}

# source_get KEY -> a value from .arrmada-source.
source_get() {
  sed -n "s/^$1=//p" .arrmada-source 2>/dev/null | head -n 1
}

# under PATH NAME -> 0 if host folder PATH is this folder's NAME or inside it.
under() {
  case "$1" in
    "") return 1 ;;
    /*) _abs="$1" ;;
    *) _abs="$(pwd)/${1#./}" ;;
  esac
  case "${_abs%/}/" in "$(pwd)/$2/"*) return 0 ;; esac
  return 1
}

# refresh_download: an install downloaded without git gets the newer files the same way —
# the branch's newest download, unpacked over this folder. Only what the download
# contains is replaced: .env, the override, the data and transcode folders and anything
# else of yours are never touched.
refresh_download() {
  _branch=$(source_get branch)
  _branch=${_branch:-main}
  _json=$(fetch "$API_URL/commits/$_branch" 2>/dev/null || true)
  _sha=$(printf '%s\n' "$_json" | sed -n 's/^  "sha": "\([0-9a-f]\{40\}\)".*/\1/p' | head -n 1)
  _date=$(printf '%s\n' "$_json" | sed -n 's/.*"date": *"\([0-9]\{4\}\)-\([0-9]\{2\}\)-\([0-9]\{2\}\)T.*/\1.\2.\3/p' | sed -n '2p')
  _short=$(printf '%s' "$_sha" | cut -c1-7)
  if [ -n "$_short" ] && [ "$_short" = "$(source_get commit)" ]; then
    say "The files here are already the newest ($_short)."
    return 0
  fi
  say "Downloading the latest files…"
  # Unpacked next to the install (same disk, so each swap is a rename), under a name the
  # loop below never touches.
  _tmp=$(mktemp -d ./.arrmada-download.XXXXXX)
  _url="$TARBALL_BASE/${_sha:-refs/heads/$_branch}"
  if ! fetch "$_url" | tar -xz -C "$_tmp"; then
    rm -rf "$_tmp"
    say "" >&2
    say "✗ Couldn't download $_url. Nothing was changed; Arrmada is still running the version it was." >&2
    say "  Check this server can reach github.com, then run update.sh again." >&2
    exit 1
  fi
  _top=$(find "$_tmp" -mindepth 1 -maxdepth 1 -type d | head -n 1)
  if [ -z "$_top" ] || [ ! -f "$_top/docker-compose.yml" ] || [ ! -f "$_top/update.sh" ]; then
    rm -rf "$_tmp"
    say "✗ The download from $_url didn't contain Arrmada. Nothing was changed." >&2
    exit 1
  fi
  _data=$(grep -E '^ARRMADA_DATA_HOST=' .env | tail -n 1 | cut -d= -f2- | tr -d '\r"'"'"' ')
  _media=$(grep -E '^ARRMADA_STORAGE_HOST=' .env | tail -n 1 | cut -d= -f2- | tr -d '\r"'"'"' ')
  for _src in "$_top"/* "$_top"/.[!.]*; do
    [ -e "$_src" ] || continue
    _name=$(basename "$_src")
    case "$_name" in
      .env | .env.* | docker-compose.override.yml* | data | transcode | .arrmada-* | .git) continue ;;
    esac
    if under "$_data" "$_name" || under "$_media" "$_name"; then
      continue
    fi
    rm -rf "./$_name"
    mv "$_src" "./$_name"
  done
  rm -rf "$_tmp"
  {
    say "# Written by install.sh: this install was downloaded, not cloned with git."
    say "# update.sh downloads the newer files from here."
    say "branch=$_branch"
    say "commit=$_short"
    say "date=$_date"
  } > .arrmada-source
}

# image_id REF -> the image's ID, or nothing if there's no such image.
image_id() {
  docker image inspect -f '{{.Id}}' "$1" 2>/dev/null || true
}

# image_label REF LABEL -> the label's value on an image, or nothing.
image_label() {
  docker image inspect -f "{{index .Config.Labels \"$2\"}}" "$1" 2>/dev/null || true
}

# confirm QUESTION -> 0 to go ahead. Only asks on a terminal: -y, and unattended runs
# (cron, a pipe), go ahead without asking.
confirm() {
  if [ -n "$YES" ] || [ ! -t 0 ]; then
    return 0
  fi
  printf '%s [y/N] ' "$1" >&2
  read -r _answer || _answer=""
  case "$_answer" in
    y | Y | yes | YES | Yes) return 0 ;;
  esac
  return 1
}

# wait_healthy -> 0 once the app answers its health check, 1 if it crashed, keeps
# restarting (Docker's restart policy brings a crashing build straight back, so it never
# reads as stopped), or never came up.
wait_healthy() {
  _i=0
  _restarts0=""
  printf 'Waiting for Arrmada to start' >&2
  while [ "$_i" -lt 90 ]; do
    _state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' Arrmada-app 2>/dev/null || echo "missing 0")
    case "$_state" in
      exited* | dead*) printf '\n' >&2; return 1 ;;
    esac
    # Counted from here: a container that wasn't replaced keeps its count, and every
    # restart from the app's own Settings adds one.
    _restarts="${_state#* }"
    case "$_restarts" in "" | *[!0-9]*) _restarts=0 ;; esac
    [ -z "$_restarts0" ] && _restarts0="$_restarts"
    if [ "$((_restarts - _restarts0))" -ge 3 ]; then
      printf '\n' >&2; return 1
    fi
    if app_answers; then
      printf ' ready\n' >&2; return 0
    fi
    printf '.' >&2
    _i=$((_i + 1)); sleep 2
  done
  printf '\n' >&2
  # Still running, just not answering yet (a big database upgrade can take a while):
  # told apart from a crash, so nothing is stopped in the middle of it.
  return 2
}

# show_log prints the last lines of the app's log.
show_log() {
  docker compose logs --tail 40 arrmada-app >&2 || true
}

# fail_start prints why the app didn't come up and exits non-zero.
fail_start() {
  say ""
  say "✗ Arrmada didn't start. The last lines of its log:" >&2
  show_log
  say "" >&2
  say "  Fix what the log says, then run the script again. Full log: docker compose logs arrmada-app" >&2
  if docker image inspect arrmada:previous >/dev/null 2>&1; then
    say "  To go back to the build that was running before: ./update.sh --rollback" >&2
  fi
  exit 1
}

if ! docker compose version >/dev/null 2>&1; then
  say "✗ 'docker compose' (v2) isn't available." >&2
  exit 1
fi
if [ ! -f .env ]; then
  say "✗ No .env found — run ./install.sh first." >&2
  say "  (Started with a plain 'docker compose up -d'? Update with: git pull && docker compose pull && docker compose up -d)" >&2
  exit 1
fi

# env_get KEY -> KEY's value in .env, or nothing.
env_get() {
  grep -E "^$1=" .env 2>/dev/null | tail -n 1 | cut -d= -f2- | tr -d '\r"'"'"' ' || true
}

# ── which image compose runs ────────────────────────────────────────────────────
# docker-compose.yml runs ARRMADA_APP_IMAGE, and without it the published release, so a
# plain `docker compose up -d` in a fresh checkout downloads rather than compiles. This
# script works differently: every build or download it makes is the local image
# arrmada:dev (the one before kept as arrmada:previous, for --rollback), so compose has to
# run that name. It's exported for every compose call below, whatever .env says, and
# written to .env once so a hand-run `docker compose up -d` agrees.
#
# An install from before the published default gets the line now. One that built or
# downloaded through these scripts has an arrmada:dev, and carries on exactly as it did
# (compiling here, unless ARRMADA_IMAGE says otherwise). One with no arrmada:dev and no
# ARRMADA_IMAGE has only ever run `docker compose up -d` here, which means the published
# image, so its updates download that too rather than start compiling.
DEFAULT_IMAGE="ghcr.io/tristenlammi/arrmada:stable"
_app_image=$(env_get ARRMADA_APP_IMAGE)
if [ -z "$_app_image" ]; then
  if [ -z "$(env_get ARRMADA_IMAGE)" ] && [ -z "$FORCE_LOCAL" ] && [ -z "$ROLLBACK" ] &&
    ! docker image inspect arrmada:dev >/dev/null 2>&1; then
    {
      say ""
      say "# The published image update.sh downloads (delete the line to compile here instead)."
      say "ARRMADA_IMAGE=$DEFAULT_IMAGE"
    } >> .env
    say "Added ARRMADA_IMAGE=$DEFAULT_IMAGE to .env: this install runs the published image, so updates download it."
  fi
  {
    say ""
    say "# The image docker compose runs: update.sh's own build or download (keep this line)."
    say "ARRMADA_APP_IMAGE=arrmada:dev"
  } >> .env
  say "Added ARRMADA_APP_IMAGE=arrmada:dev to .env (the image update.sh builds or downloads)."
elif [ "$_app_image" != arrmada:dev ]; then
  say "✗ ARRMADA_APP_IMAGE=$_app_image in .env, but update.sh only runs its own arrmada:dev image." >&2
  say "  To have updates download a published image, set ARRMADA_IMAGE=$_app_image and" >&2
  say "  ARRMADA_APP_IMAGE=arrmada:dev, then run ./update.sh again. Or leave .env as it is" >&2
  say "  and update with: docker compose pull && docker compose up -d" >&2
  exit 2
fi
ARRMADA_APP_IMAGE=arrmada:dev
export ARRMADA_APP_IMAGE

# Pull mode: the published image named by ARRMADA_IMAGE in .env, or nothing to compile
# here as always. --force-local means "build what's here", so it turns pull mode off for
# this run. The script's own local tags can't be the source: they're what it manages.
# --rollback never pulls, so it doesn't read it.
PULL_IMAGE=""
if [ -z "$FORCE_LOCAL" ] && [ -z "$ROLLBACK" ]; then
  PULL_IMAGE=$(grep -E '^ARRMADA_IMAGE=' .env 2>/dev/null | tail -n 1 | cut -d= -f2- | tr -d '\r"'"'"' ' || true)
fi
case "$PULL_IMAGE" in
  arrmada | arrmada:*)
    say "✗ ARRMADA_IMAGE=$PULL_IMAGE in .env names one of this script's own local images." >&2
    say "  Set it to a published image (e.g. ghcr.io/tristenlammi/arrmada:stable), or remove the line to build here." >&2
    exit 2 ;;
esac

# port_in_use PORT -> 0 if something already listens on PORT on this host.
port_in_use() {
  if command -v ss >/dev/null 2>&1; then
    ss -ltnH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
  else
    return 1
  fi
}

# ── rollback ───────────────────────────────────────────────────────────────────
# start_app_or_go_back CURRENT_ID -> starts whatever arrmada:dev is now and waits for
# it. If it doesn't come up, the build it replaced (kept as arrmada:rolled-back) is put
# back and started, so a failed rollback never leaves Arrmada down; then it exits 1.
start_app_or_go_back() {
  if docker compose up -d --no-build --no-deps --force-recreate arrmada-app && wait_healthy; then
    return 0
  fi
  say "" >&2
  say "✗ The previous build didn't start. The last lines of its log:" >&2
  show_log
  if [ -n "$1" ]; then
    say "" >&2
    say "  Starting the build you rolled back from again…" >&2
    docker image tag arrmada:rolled-back arrmada:dev
    if docker compose up -d --no-build --no-deps --force-recreate arrmada-app && wait_healthy; then
      say "  Arrmada is back on the build it was running." >&2
    else
      say "  ✗ That didn't start either. Full log: docker compose logs arrmada-app" >&2
    fi
  fi
  exit 1
}

# manual_db_steps BACKUP prints how to put a backup back by hand.
manual_db_steps() {
  say "  To put the database back by hand:" >&2
  say "    1. docker compose stop arrmada-app" >&2
  say "    2. In the data folder (ARRMADA_DATA_HOST in .env, or the arrmada-data volume), copy" >&2
  say "       backups/$1 over arrmada.db, and delete arrmada.db-wal and arrmada.db-shm." >&2
  say "    3. ./update.sh --rollback" >&2
}

# Puts the build that ran before the last update back: arrmada:previous becomes
# arrmada:dev again, and the build being replaced is kept as arrmada:rolled-back so
# going forward again is one update away. Nothing is pulled or rebuilt.
rollback() {
  _prev=$(image_id arrmada:previous)
  if [ -z "$_prev" ]; then
    say "✗ There's no earlier build to go back to (no arrmada:previous image)." >&2
    say "  One is kept from the first update made with this version of update.sh." >&2
    exit 1
  fi
  _cur=$(image_id arrmada:dev)
  if [ "$_prev" = "$_cur" ]; then
    say "Arrmada is already running the previous build. To go forward again, run ./update.sh"
    exit 0
  fi
  _prev_commit=$(image_label arrmada:previous org.arrmada.commit)

  _backup=""
  if [ -n "$WITH_DB" ]; then
    _backup=$(cat "$PREVIOUS_DB_FILE" 2>/dev/null || true)
    if [ -z "$_backup" ]; then
      say "✗ No database backup was recorded for the previous build (the last update didn't take one)." >&2
      say "  The automatic copies taken before each schema change are in <data>/backups (arrmada-pre-migrate-*)." >&2
      manual_db_steps "arrmada-pre-migrate-<the one from before the update>.db"
      exit 1
    fi
    # The build being rolled back to is the one that boots on the restored database,
    # so it has to be the one that understands `arrmada restore`.
    if [ "$(image_label arrmada:previous org.arrmada.restore)" != "1" ]; then
      say "✗ The previous build can't restore a database itself, so nothing was changed." >&2
      manual_db_steps "$_backup"
      exit 1
    fi
    say "This goes back to the previous build${_prev_commit:+ (commit $_prev_commit)} AND puts back the"
    say "database as it was before the last update ($_backup)."
    say "Everything Arrmada recorded since then is lost: requests, watch and listening history,"
    say "settings changes. Torrents grabbed since keep running in qBittorrent, but Arrmada won't know them."
    if ! confirm "Roll back the build and the database?"; then
      say "Nothing was changed."
      exit 0
    fi
  fi

  # A build from before this script could ask it about the database (no org.arrmada.cli
  # label: every build before the backup/rollback tooling) starts on whatever database it
  # finds. It has no newer-schema check, so on a database the update upgraded it runs old
  # code against tables it doesn't know. Say so before anything changes: the database has
  # to be put back by hand, since such a build can't restore one either.
  _legacy=""
  if [ -z "$_backup" ] && [ "$(image_label arrmada:previous org.arrmada.cli)" != "1" ]; then
    # Going back on its own after a failed update, the script can't ask, so it doesn't.
    if [ -n "$AUTO_ROLLBACK" ]; then
      say "✗ The previous build${_prev_commit:+ (commit $_prev_commit)} is too old to go back to automatically" >&2
      say "  (it can't check the database). To go back by hand:" >&2
      manual_db_steps "arrmada-pre-migrate-<the newest one>.db"
      exit 1
    fi
    _legacy=1
    say "! The previous build${_prev_commit:+ (commit $_prev_commit)} is older than update.sh's database checks: it starts"
    say "  on the database as it is now, even if the update upgraded it, and can damage an upgraded one."
    say "  If the update upgraded the database (<data>/backups has an arrmada-pre-migrate-* copy from it)"
    say "  and you haven't put that copy back yet, answer no and do this instead:"
    manual_db_steps "arrmada-pre-migrate-<the newest one>.db"
    if ! confirm "Start the previous build on the database as it is now?"; then
      say "Nothing was changed."
      exit 0
    fi
  fi

  say "Rolling back to the build that ran before the last update…"
  # Tagged first, so the build being left can never be deleted as untagged meanwhile.
  if [ -n "$_cur" ]; then
    docker image tag "$_cur" arrmada:rolled-back
  fi

  # Without --with-db, ask the previous build itself, before anything is stopped,
  # whether it can run on the database as it is now: if this update upgraded it, that
  # build refuses to start on it, so stop here instead. Only a build labelled with the
  # CLI is asked (an older one would start a server); one too old to know the command
  # answers with a usage error, and then the start itself is the check. The one-off
  # container runs whatever arrmada:dev names, hence the brief retag.
  _schema_ok=""
  if [ -z "$_backup" ] && [ "$(image_label arrmada:previous org.arrmada.cli)" = "1" ]; then
    docker image tag arrmada:previous arrmada:dev
    _rc=0
    _check=$(docker compose run --rm --no-deps -T --entrypoint /usr/local/bin/arrmada arrmada-app schema 2>&1 </dev/null) || _rc=$?
    if [ -n "$_cur" ]; then
      docker image tag "$_cur" arrmada:dev
    fi
    [ "$_rc" = 0 ] && _schema_ok=1
    if [ "$_rc" = 3 ]; then
      docker image rm arrmada:rolled-back >/dev/null 2>&1 || true
      say "" >&2
      say "$_check" | sed 's/^/  /' >&2
      say "" >&2
      say "✗ The previous build can't run on the database as it is now: the update upgraded it." >&2
      say "  Nothing was changed. To go back together with the database from before the update" >&2
      say "  (everything recorded since is lost): ./update.sh --rollback --with-db" >&2
      # An automatic go-back (a failed update) tells its caller why, to offer exactly that.
      if [ -n "$AUTO_ROLLBACK" ]; then exit 3; fi
      exit 1
    fi
  fi

  if [ -n "$_backup" ]; then
    docker compose stop arrmada-app
    docker image tag arrmada:previous arrmada:dev
    # Staged with the build that will boot on it: the swap happens as it starts.
    if ! docker compose run --rm --no-deps -T arrmada-app restore "$_backup" </dev/null; then
      say "✗ Restoring $_backup failed (see above)." >&2
      if [ -n "$_cur" ]; then
        docker image tag arrmada:rolled-back arrmada:dev
      fi
      say "  Starting the build that was running again…" >&2
      docker compose up -d --no-build --no-deps --force-recreate arrmada-app
      wait_healthy || say "  ✗ It didn't come back. Full log: docker compose logs arrmada-app" >&2
      exit 1
    fi
  else
    docker image tag arrmada:previous arrmada:dev
  fi
  start_app_or_go_back "$_cur"

  _commit=$(health_json | json_str commit)
  say ""
  say "✓ Rolled back. Arrmada is running commit ${_commit:-unknown} again."
  if [ -n "$_backup" ]; then
    say "  The database is back to $_backup; everything recorded after it is gone."
  elif [ -n "$_legacy" ]; then
    # --rollback --with-db and System → Backups can't help a build this old (see above).
    say "  If the update upgraded the database and you haven't put it back yet: docker compose stop arrmada-app,"
    say "  copy <data>/backups/arrmada-pre-migrate-<the newest one>.db over <data>/arrmada.db, delete"
    say "  arrmada.db-wal and arrmada.db-shm, then docker compose up -d --no-build --no-deps arrmada-app."
  elif [ -z "$_schema_ok" ]; then
    say "  If this update ran a database migration, also restore the pre-update backup"
    say "  (./update.sh --rollback --with-db, or System → Backups)."
  fi
  say "  The build you left is kept as arrmada:rolled-back; ./update.sh goes forward again."
}

if [ -n "$ROLLBACK" ]; then
  rollback
  exit 0
fi

# ── long conversion? ───────────────────────────────────────────────────────────
# Restarting the app abandons a conversion in flight, and a 4K encode can take a day.
# Asked before anything changes (the pull included), and only once: a copy of this
# script re-run after the pull (--pulled) has already been through it. Only asks on a
# terminal; -y, ARRMADA_UPDATE_FORCE=1 and unattended runs print the warning and go on.
# The app reports this only to callers inside its own container.
if [ -z "$PULLED" ]; then
  _health=$(health_json)
  _sec=$(printf '%s\n' "$_health" | json_num convert_running_sec)
  if [ -n "$_sec" ] && [ "$_sec" -gt 7200 ]; then
    _pct=$(printf '%s\n' "$_health" | json_num convert_progress)
    say "! A conversion has been running for $((_sec / 3600))h $(((_sec % 3600) / 60))m (${_pct:-0}%). Updating now restarts it from the beginning."
    if ! confirm "Update anyway?"; then
      say "Not updating; nothing was changed. Run ./update.sh again once it's done (or with -y to go ahead)."
      exit 0
    fi
  fi
fi

# ── pull latest (skip cleanly if this isn't a git checkout) ─────────────────────
# If the pull changed this script, run the new one: it may know about settings or steps
# this older copy doesn't, and they should apply now rather than on the next update.
# Pull mode pulls too: nothing is compiled from it, but it keeps this script and
# docker-compose.yml current.
if [ -z "$PULLED" ]; then
  if [ -n "$FORCE_LOCAL" ]; then
    say "Not pulling (--force-local) — building the code that's here."
  elif [ -d .git ] && command -v git >/dev/null 2>&1; then
    say "Pulling latest changes…"
    # The commit this checkout was on before the pull: the code that's running now, if the
    # last update went through. Kept for reference (git checkout it to rebuild that code).
    git rev-parse --short HEAD > .arrmada-previous 2>/dev/null || true
    _before=$(cksum < update.sh)
    if ! git pull --ff-only; then
      say "" >&2
      say "✗ git pull failed (see above). Nothing was rebuilt; Arrmada is still running the version it was." >&2
      say "  Fix the checkout ('git status' shows local changes or a diverged branch), then run ./update.sh again," >&2
      say "  or run ./update.sh --force-local to rebuild the code that's here without pulling." >&2
      exit 1
    fi
    if [ "$(cksum < update.sh)" != "$_before" ]; then
      exec sh ./update.sh --pulled "$@"
    fi
  elif [ -f .arrmada-source ]; then
    _before=$(cksum < update.sh)
    refresh_download
    if [ "$(cksum < update.sh)" != "$_before" ]; then
      exec sh ./update.sh --pulled "$@"
    fi
  elif [ -n "$PULL_IMAGE" ]; then
    say "Not a git checkout — the scripts here stay as they are."
  else
    say "Not a git checkout — building the code that's here."
  fi
fi

# Settings added since this install: give each a free value once, never overwrite.
if ! grep -qE '^ARRMADA_AUDIOBOOK_PORT=' .env; then
  AUDIOPORT=""
  for p in 13379 13380 13381 13382 18379; do
    if ! port_in_use "$p"; then AUDIOPORT=$p; break; fi
  done
  [ -z "$AUDIOPORT" ] && AUDIOPORT=18379
  {
    say ""
    say "# Audiobook server for listening apps (off until switched on in Services → Audiobooks)."
    say "ARRMADA_AUDIOBOOK_PORT=$AUDIOPORT"
  } >> .env
  say "Added ARRMADA_AUDIOBOOK_PORT=$AUDIOPORT to .env (the audiobook server's port)."
fi

# The build that's running now, if it's answering: the rollback target once the new
# build replaces it. A crash-looping build is no rollback target, so then the last good
# one stays arrmada:previous.
RUNNING_IMAGE=""
if app_answers; then
  RUNNING_IMAGE=$(docker inspect -f '{{.Image}}' Arrmada-app 2>/dev/null || true)
fi

# ── pre-update database backup ─────────────────────────────────────────────────
# Taken by the running app's own binary (`arrmada backup`), the same checked copy the
# app makes nightly, while it keeps running. Only an image labelled org.arrmada.cli is
# ever given arguments: an older binary ignores them and starts a second full server.
PRE_UPDATE_BACKUP=""
if [ -n "$NO_BACKUP" ]; then
  say "  ! Not backing up the database first (--no-backup)."
elif [ -z "$RUNNING_IMAGE" ]; then
  say "  ! Arrmada isn't running, so there's no pre-update backup (it still snapshots the database before any schema change)."
elif [ "$(docker inspect -f '{{index .Config.Labels "org.arrmada.cli"}}' Arrmada-app 2>/dev/null || true)" != "1" ]; then
  say "  ! This build can't take a pre-update backup; continuing (it still snapshots the database before any schema change)."
else
  say "Backing up the database…"
  if _path=$(docker exec -u "$RUN_AS" Arrmada-app arrmada backup --kind pre-update); then
    PRE_UPDATE_BACKUP=$(basename "$_path")
    say "  Saved $_path"
  else
    say "" >&2
    say "✗ The pre-update backup failed (see above), so nothing was changed." >&2
    say "  Free some space in the data folder and run ./update.sh again," >&2
    say "  or run ./update.sh --no-backup to update without one." >&2
    exit 1
  fi
fi

# Stamp the build with what it was built from, so the Dashboard and /api/health say
# which code is running (docker-compose.yml passes these as build args). A tagged
# release reads as its tag; anything else as the date of its commit, with the short
# commit beside it — a bare hash says nothing about how old the build is. Every git
# call is guarded so a copy that isn't a git checkout still builds. A published image
# was stamped by the release build, so pull mode reads its labels afterwards instead.
if [ -z "$PULL_IMAGE" ]; then
  if [ ! -d .git ] && [ -f .arrmada-source ]; then
    # Downloaded without git: the download recorded which commit it is.
    ARRMADA_COMMIT=$(source_get commit)
    ARRMADA_VERSION=$(source_get date)
    ARRMADA_COMMIT=${ARRMADA_COMMIT:-unknown}
    ARRMADA_VERSION=${ARRMADA_VERSION:-dev}
  else
    ARRMADA_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
    ARRMADA_VERSION=$(git describe --tags --exact-match 2>/dev/null || git log -1 --format=%cd --date=format:%Y.%m.%d 2>/dev/null || echo dev)
    if [ "$ARRMADA_COMMIT" != unknown ] && ! git diff --quiet HEAD -- 2>/dev/null; then
      ARRMADA_VERSION="$ARRMADA_VERSION-dirty"
    fi
  fi
  export ARRMADA_VERSION ARRMADA_COMMIT
fi

# Keep the build that's running as arrmada:previous, for --rollback. It has to be
# tagged before the build: once the new build takes arrmada:dev and the old container
# is replaced, an untagged image can be deleted on the spot (Docker's containerd image
# store does). The rollback target it replaces is held as arrmada:previous-old until
# it's clear the build really replaced the running one; a failed or identical build
# puts it back. Tagged images are never pruned.
OLD_PREVIOUS=$(image_id arrmada:previous)
if [ -n "$RUNNING_IMAGE" ]; then
  if [ -n "$OLD_PREVIOUS" ] && [ "$OLD_PREVIOUS" != "$RUNNING_IMAGE" ]; then
    docker image tag "$OLD_PREVIOUS" arrmada:previous-old
  fi
  docker image tag "$RUNNING_IMAGE" arrmada:previous
fi

# Settings that were removed: the app never sees them now, so say so here.
if grep -qE '^ARRMADA_BASE_URL=.*[^/[:space:]"'"'"']' .env; then
  say "  ! ARRMADA_BASE_URL in .env is no longer supported and is ignored — Arrmada is served"
  say "    at the root of its own hostname. Point your reverse proxy at the root, then delete the line."
fi

# Rebuild + recreate ONLY the app. --no-deps leaves the companions (qBittorrent,
# Prowlarr, FlareSolverr) running and, crucially, does NOT re-run the one-shot media /
# qBittorrent init containers — those only need to run once, at install. An update
# only changes the app image, so nothing else needs touching.
#
# In pull mode the published image is downloaded and tagged arrmada:dev, the name
# docker-compose.yml runs, so the rollback tags and every step after this one work on
# it exactly as they do on a local build. A failed pull leaves arrmada:dev untouched.
UP_OK=1
PULL_FAILED=""
if [ -n "$PULL_IMAGE" ]; then
  say "Downloading $PULL_IMAGE and restarting Arrmada…"
  if docker pull "$PULL_IMAGE"; then
    if ! docker image tag "$PULL_IMAGE" arrmada:dev || ! docker compose up -d --no-build --no-deps arrmada-app; then
      UP_OK=""
    fi
  else
    UP_OK=""
    PULL_FAILED=1
  fi
else
  say "Rebuilding and restarting Arrmada…"
  docker compose up -d --build --no-deps arrmada-app || UP_OK=""
fi
# Whether the pull brought nothing new (already on the newest image).
SAME_IMAGE=""
if [ -n "$RUNNING_IMAGE" ] && [ "$(image_id arrmada:dev)" = "$RUNNING_IMAGE" ]; then
  SAME_IMAGE=1
fi

if [ -n "$RUNNING_IMAGE" ]; then
  if [ "$(image_id arrmada:dev)" != "$RUNNING_IMAGE" ]; then
    # Replaced (whether or not the new build starts: one that won't is exactly when
    # --rollback is needed). The backup just taken belongs with it.
    if [ -n "$PRE_UPDATE_BACKUP" ]; then
      printf '%s\n' "$PRE_UPDATE_BACKUP" > "$PREVIOUS_DB_FILE"
    else
      rm -f "$PREVIOUS_DB_FILE"
    fi
  elif [ -n "$OLD_PREVIOUS" ]; then
    # Nothing was replaced: the older rollback target stays the rollback target.
    docker image tag "$OLD_PREVIOUS" arrmada:previous
  else
    docker image rm arrmada:previous >/dev/null 2>&1 || true
  fi
  docker image rm arrmada:previous-old >/dev/null 2>&1 || true
fi

if [ -n "$PULL_FAILED" ]; then
  say "" >&2
  say "✗ Couldn't download $PULL_IMAGE (see above). Nothing was replaced." >&2
  say "  Check the name in ARRMADA_IMAGE and that this server can reach ghcr.io. If the package" >&2
  say "  is private, sign this server in once with 'docker login ghcr.io' and a token of its own" >&2
  say "  that can only read packages. To compile here instead: ./update.sh --force-local" >&2
  exit 1
fi
# update_failed: the new version replaced the running one and didn't start. Go back to
# the one that was running (kept as arrmada:previous a moment ago) rather than leave
# Arrmada down. The rollback checks the database first: if the new version already
# upgraded it, going back also needs the backup taken at the start of this update, which
# loses whatever happened in the last few minutes, so that is only ever done when someone
# at the terminal says yes.
update_failed() {
  say "" >&2
  say "✗ The new version of Arrmada didn't start. The last lines of its log:" >&2
  show_log
  if [ -z "$RUNNING_IMAGE" ] || [ "$(image_id arrmada:previous)" != "$RUNNING_IMAGE" ] || [ "$(image_id arrmada:dev)" = "$RUNNING_IMAGE" ]; then
    say "" >&2
    say "  There's no earlier running version to go back to. Fix what the log says, then run the" >&2
    say "  script again. Full log: docker compose logs arrmada-app" >&2
    exit 1
  fi
  say "" >&2
  say "Going back to the version that was running before the update…" >&2
  AUTO_ROLLBACK=1
  _rc=0
  ( rollback ) || _rc=$?
  if [ "$_rc" = 3 ] && [ -f "$PREVIOUS_DB_FILE" ]; then
    _answer=""
    if [ -z "$YES" ] && [ -t 0 ]; then
      say "" >&2
      say "The new version had already upgraded the database. Going back needs the backup taken at" >&2
      say "the start of this update ($(cat "$PREVIOUS_DB_FILE")); anything Arrmada recorded since then is lost." >&2
      printf 'Put it back and start the previous version? [y/N] ' >&2
      read -r _answer || _answer=""
    fi
    case "$_answer" in
      y | Y | yes | YES | Yes)
        _rc=0
        ( WITH_DB=1; YES=1; rollback ) || _rc=$? ;;
      *)
        say "  Arrmada is down. Either go back with the database: ./update.sh --rollback --with-db" >&2
        say "  or fix what the log says and run ./update.sh again." >&2
        exit 1 ;;
    esac
  fi
  say "" >&2
  if [ "$_rc" = 0 ]; then
    say "✗ The update didn't go through, so Arrmada is back on the version it was running." >&2
    say "  Nothing else was changed. The new version's log is above; please report it at" >&2
    say "  https://github.com/$REPO/issues" >&2
  else
    say "✗ The update didn't go through, and going back didn't work either (see above)." >&2
    say "  Full log: docker compose logs arrmada-app" >&2
  fi
  exit 1
}

if [ -z "$UP_OK" ]; then
  if [ -n "$RUNNING_IMAGE" ] && [ "$(image_id arrmada:dev)" = "$RUNNING_IMAGE" ] && app_answers; then
    say "" >&2
    say "✗ The build failed (see above). Nothing was replaced; Arrmada is still running the version it was." >&2
    exit 1
  fi
  update_failed
fi
_waited=0
wait_healthy || _waited=$?
if [ "$_waited" = 2 ]; then
  # Not crashed, only slow: going back now would stop it in the middle of whatever it's
  # doing (an upgrade of the database's tables, say), so leave it running and say how to
  # follow it.
  say "" >&2
  say "! The new version is running but hasn't answered for 3 minutes. It may still be upgrading" >&2
  say "  the database. Follow it with: docker compose logs -f arrmada-app" >&2
  say "  If it doesn't come up, go back with: ./update.sh --rollback" >&2
  exit 1
elif [ "$_waited" != 0 ]; then
  update_failed
fi

# A build kept by an earlier --rollback is superseded by this one.
docker image rm arrmada:rolled-back >/dev/null 2>&1 || true
# Reclaim space from older builds (harmless if nothing to prune). arrmada:previous is
# tagged, so it stays.
docker image prune -f >/dev/null 2>&1 || true

WEBPORT=$(grep -E '^ARRMADA_PORT=' .env | cut -d= -f2)
HOSTIP=$(hostname -I 2>/dev/null | awk '{print $1}')
# Not every hostname knows -I (Unraid's, busybox's); the route to the internet names it too.
if [ -z "$HOSTIP" ] && command -v ip >/dev/null 2>&1; then
  HOSTIP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')
fi
say ""
if [ -n "$PULL_IMAGE" ]; then
  # What the release build stamped on the image (the same labels --rollback reads).
  ARRMADA_VERSION=$(image_label arrmada:dev org.arrmada.version)
  ARRMADA_COMMIT=$(image_label arrmada:dev org.arrmada.commit)
  if [ -n "$SAME_IMAGE" ]; then
    say "✓ Arrmada is already on the newest $PULL_IMAGE: ${ARRMADA_VERSION:-unknown} (${ARRMADA_COMMIT:-unknown}).  Open http://${HOSTIP:-localhost}:${WEBPORT:-7878}"
  else
    say "✓ Arrmada updated to ${ARRMADA_VERSION:-unknown} (${ARRMADA_COMMIT:-unknown}) from $PULL_IMAGE.  Open http://${HOSTIP:-localhost}:${WEBPORT:-7878}"
  fi
else
  say "✓ Arrmada updated to ${ARRMADA_VERSION} (${ARRMADA_COMMIT}).  Open http://${HOSTIP:-localhost}:${WEBPORT:-7878}"
fi
say "  A database snapshot is taken automatically before any schema change (<data>/backups, newest 5 kept)."
if docker image inspect arrmada:previous >/dev/null 2>&1; then
  say "  The build that ran before is kept as arrmada:previous: ./update.sh --rollback goes back to it."
fi
