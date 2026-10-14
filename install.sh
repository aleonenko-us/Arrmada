#!/usr/bin/env sh
# Arrmada installer. One line, on the server that will run it:
#
#   curl -fsSL https://raw.githubusercontent.com/tristenlammi/Arrmada/main/install.sh | sh
#
# It checks Docker, asks two things (where Arrmada keeps its files, and where your media
# lives — Enter accepts the suggestion), works out the rest (free ports, the user to run
# as, your timezone, a GPU), starts Arrmada and prints the address to open. Everything
# else — the TMDB key, each library folder, Plex, indexers, people — is set up in the
# app's first-run setup, in the browser.
#
# Run the same line again later and it updates Arrmada instead (it finds the install and
# runs its update.sh). From a git checkout, ./install.sh does the same in that folder.
#
# Options (after `| sh -s --`, or after ./install.sh):
#   --dir DIR          where Arrmada keeps its files (default: /mnt/user/appdata/arrmada on
#                      Unraid, ~/arrmada otherwise, /opt/arrmada as root)
#   --media DIR        the folder that holds your libraries and downloads, mounted at /storage
#   --no-media         use Arrmada's own managed storage instead of a media folder
#   --transcode DIR    Convert's scratch folder (default: DIR/transcode; "none" for none)
#   --tz ZONE          timezone, e.g. Europe/London (default: this machine's)
#   --puid N --pgid N  run as this user and group (default: your media folder's owner;
#                      99/100 on Unraid)
#   --with-prowlarr    also run the optional Prowlarr indexer manager
#   --image REF        run this published image (default: ghcr.io/tristenlammi/arrmada:stable)
#   --build            compile the app on this server instead of downloading it
#   -y, --yes          don't ask anything; take the suggestions and the options given
#
# On an existing install, --media / --transcode change its mounts and --with-prowlarr
# adds Prowlarr; with none of them it updates.
#
# Nothing secret is asked or written here: API keys and passwords go in the app.
set -eu

REPO="tristenlammi/Arrmada"
# Overridable for forks (and the installer's own tests); everyone else leaves them be.
REPO_URL="${ARRMADA_REPO_URL:-https://github.com/$REPO}"
BRANCH="${ARRMADA_BRANCH:-main}"
API_URL="${ARRMADA_API_URL:-https://api.github.com/repos/$REPO}"
TARBALL_BASE="${ARRMADA_TARBALL_BASE:-https://codeload.github.com/$REPO/tar.gz}"
DEFAULT_IMAGE="ghcr.io/tristenlammi/arrmada:stable"
# The line people run, printed in messages.
ONE_LINER="curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh"

say() { printf '%s\n' "$*"; }
err() { printf '%s\n' "$*" >&2; }
die() { err "✗ $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# Questions read from the terminal, not stdin: piped into sh, stdin is this script.
TTY=""
tty_init() {
  if [ -z "$YES" ] && (: </dev/tty) 2>/dev/null; then TTY=1; fi
}
# ask "prompt" "default" -> the answer (the default when there's no terminal or it's blank).
ask() {
  _a=""
  if [ -n "$TTY" ]; then
    printf '%s' "$1" >/dev/tty
    IFS= read -r _a </dev/tty || _a=""
  fi
  [ -z "$_a" ] && _a="${2:-}"
  printf '%s' "$_a"
}
# ask_yes "question" -> 0 for yes. Enter is yes; no terminal is yes.
ask_yes() {
  [ -n "$TTY" ] || return 0
  _a=$(ask "$1 [Y/n] " "y")
  case "$_a" in n | N | no | No | NO) return 1 ;; esac
  return 0
}

is_unraid() { [ -f /etc/unraid-version ] || { [ -d /mnt/user ] && [ -d /boot/config ]; }; }

# detect_tz -> this machine's timezone name (e.g. Australia/Brisbane), or nothing.
detect_tz() {
  _t="${TZ:-}"
  [ -z "$_t" ] && [ -r /etc/timezone ] && _t=$(head -n1 /etc/timezone 2>/dev/null)
  # Unraid keeps it in its flash config (Settings → Date and Time).
  [ -z "$_t" ] && [ -r /boot/config/ident.cfg ] && _t=$(sed -n 's/^timeZone="*\([^"]*\)"*.*/\1/p' /boot/config/ident.cfg 2>/dev/null | head -n1)
  [ -z "$_t" ] && [ -L /etc/localtime ] && _t=$(readlink /etc/localtime 2>/dev/null | sed 's#.*/zoneinfo/##')
  if valid_tz "$_t"; then printf '%s' "$_t"; fi
}
# valid_tz NAME -> 0 if NAME is a timezone this machine knows (any Region/City name, if it
# ships no zone files to check against — the app carries its own copy of the database).
valid_tz() {
  case "$1" in "" | /* | *..* | *" "*) return 1 ;; esac
  [ -d /usr/share/zoneinfo ] || return 0
  [ -f "/usr/share/zoneinfo/$1" ]
}
# tz_fix_case NAME -> the correctly capitalised zone ("australia/brisbane" →
# "Australia/Brisbane"), or nothing if there's no such zone.
tz_fix_case() {
  [ -d /usr/share/zoneinfo ] || return 0
  find /usr/share/zoneinfo -type f -ipath "/usr/share/zoneinfo/$1" 2>/dev/null | grep -v '/right/\|/posix/' | head -n1 | sed 's#^/usr/share/zoneinfo/##'
}

# port_in_use PORT -> 0 if something already listens on PORT on this host.
port_in_use() {
  if have ss; then
    ss -ltnH 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
  elif have netstat; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${1}\$"
  else
    return 1 # can't check — assume free
  fi
}
# free_port PREFERRED... -> the first preferred port that's free, else one from 18000 up.
free_port() {
  for _p in "$@"; do port_in_use "$_p" || { printf '%s' "$_p"; return; }; done
  _p=18000
  while port_in_use "$_p"; do _p=$((_p + 1)); done
  printf '%s' "$_p"
}
# rand_port -> a random high port (for BitTorrent) that isn't in use.
rand_port() {
  _p=$(awk 'BEGIN { srand(); print int(20000 + rand() * 40000) }')
  while port_in_use "$_p"; do _p=$((_p + 1)); done
  printf '%s' "$_p"
}

# host_ip -> this server's LAN address, for the "open this" line.
host_ip() {
  _ip=$(hostname -I 2>/dev/null | awk '{print $1}')
  if [ -z "$_ip" ] && have ip; then
    _ip=$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')
  fi
  printf '%s' "${_ip:-localhost}"
}

# fetch URL -> the body on stdout (curl or wget, whichever is here).
fetch() {
  if have curl; then curl -fsSL "$1"; else wget -qO- "$1"; fi
}

# env_get KEY -> KEY's value in ./.env, or nothing.
env_get() {
  grep -E "^$1=" .env 2>/dev/null | tail -n 1 | cut -d= -f2- | tr -d '\r"'"'" || true
}

# wait_healthy -> 0 once the app answers its health check; 1 if it crashed, keeps
# restarting, or never came up. It asks from inside the container, so it works whatever
# host port was picked and needs nothing on the host.
wait_healthy() {
  _i=0
  _restarts0=""
  printf 'Waiting for Arrmada to start' >&2
  while [ "$_i" -lt 150 ]; do
    _state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' Arrmada-app 2>/dev/null || echo "missing 0")
    case "$_state" in
      exited* | dead*) printf '\n' >&2; return 1 ;;
    esac
    # Docker's restart policy brings a crashing app straight back, so it never reads as
    # stopped: three restarts while waiting is a crash loop.
    _restarts="${_state#* }"
    case "$_restarts" in "" | *[!0-9]*) _restarts=0 ;; esac
    [ -z "$_restarts0" ] && _restarts0="$_restarts"
    if [ "$((_restarts - _restarts0))" -ge 3 ]; then printf '\n' >&2; return 1; fi
    if docker exec Arrmada-app curl -fsS -o /dev/null "http://127.0.0.1:7878/api/health" 2>/dev/null; then
      printf ' ready\n' >&2; return 0
    fi
    printf '.' >&2
    _i=$((_i + 1)); sleep 2
  done
  printf '\n' >&2
  return 1
}

# fail_start prints why the app didn't come up and exits non-zero.
fail_start() {
  err ""
  err "✗ Arrmada didn't start. The last lines of its log:"
  docker compose logs --tail 40 arrmada-app >&2 </dev/null || true
  err ""
  err "  Fix what the log says, then run the installer again. Full log (in $(pwd)):"
  err "    docker compose logs arrmada-app"
  exit 1
}

usage() {
  say "Install Arrmada, or update the install that's already here."
  say ""
  say "  $ONE_LINER"
  say "  $ONE_LINER -s -- [options]"
  say ""
  say "  --dir DIR          where Arrmada keeps its files (Unraid: /mnt/user/appdata/arrmada)"
  say "  --media DIR        the folder holding your libraries and downloads (mounted at /storage)"
  say "  --no-media         use Arrmada's own managed storage instead"
  say "  --transcode DIR    Convert's scratch folder (default DIR/transcode; \"none\" for none)"
  say "  --tz ZONE          timezone, e.g. Europe/London (default: this machine's)"
  say "  --puid N --pgid N  run as this user and group (default: the media folder's owner)"
  say "  --with-prowlarr    also run the optional Prowlarr indexer manager"
  say "  --image REF        run this published image (default: $DEFAULT_IMAGE)"
  say "  --build            compile the app on this server instead of downloading it"
  say "  -y, --yes          don't ask anything; take the suggestions and the options given"
  say ""
  say "On an existing install, --media / --transcode change its mounts, --with-prowlarr adds"
  say "Prowlarr, and with none of them it updates (the same as its update.sh)."
}

# ── where an install already is ─────────────────────────────────────────────────
# is_install DIR -> 0 if DIR holds an Arrmada install (its .env and compose file).
is_install() { [ -f "$1/.env" ] && [ -f "$1/docker-compose.yml" ]; }

# existing_dir -> the folder of the Arrmada already on this server, or nothing. The
# running container says where it was started from; the usual folders are checked too.
existing_dir() {
  _wd=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' Arrmada-app 2>/dev/null || true)
  if [ -n "$_wd" ] && is_install "$_wd"; then printf '%s' "$_wd"; return; fi
  for _d in /mnt/user/appdata/arrmada /mnt/user/appdata/Arrmada "${HOME:-/root}/arrmada" /opt/arrmada; do
    if is_install "$_d"; then printf '%s' "$_d"; return; fi
  done
}

# default_dir -> where a new install goes.
default_dir() {
  if is_unraid && [ -d /mnt/user/appdata ]; then
    say /mnt/user/appdata/arrmada
  elif [ "$(id -u)" = 0 ]; then
    say /opt/arrmada
  else
    say "${HOME:-/root}/arrmada"
  fi
}

# ── getting the files ───────────────────────────────────────────────────────────
# get_files DIR: the compose file, scripts and source, with git when it's here (update.sh
# then pulls), else as a download (update.sh downloads the newer one the same way).
get_files() {
  _dir="$1"
  if have git; then
    say "Downloading Arrmada (git clone)…"
    git clone --quiet --branch "$BRANCH" "$REPO_URL" "$_dir" </dev/null || die "Couldn't download Arrmada from $REPO_URL."
    return
  fi
  have tar || die "This server has neither git nor tar, so the files can't be unpacked. Install either, then run the installer again."
  say "Downloading Arrmada…"
  # The exact commit, so a build from source says which code it is (the update check
  # compares it with main). Without an answer it takes the branch as it is.
  _json=$(fetch "$API_URL/commits/$BRANCH" 2>/dev/null || true)
  _sha=$(printf '%s\n' "$_json" | sed -n 's/^  "sha": "\([0-9a-f]\{40\}\)".*/\1/p' | head -n 1)
  # The second date is the committer's, which is what a clone's build is stamped with.
  _date=$(printf '%s\n' "$_json" | sed -n 's/.*"date": *"\([0-9]\{4\}\)-\([0-9]\{2\}\)-\([0-9]\{2\}\)T.*/\1.\2.\3/p' | sed -n '2p')
  _url="$TARBALL_BASE/${_sha:-refs/heads/$BRANCH}"
  _tmp=$(mktemp -d)
  if ! fetch "$_url" | tar -xz -C "$_tmp"; then
    rm -rf "$_tmp"; die "Couldn't download $_url."
  fi
  _top=$(find "$_tmp" -mindepth 1 -maxdepth 1 -type d | head -n 1)
  if [ -z "$_top" ] || [ ! -f "$_top/docker-compose.yml" ]; then
    rm -rf "$_tmp"; die "The download from $_url didn't contain Arrmada."
  fi
  mkdir -p "$_dir"
  # cp -R of "dir/." brings the hidden files too (.dockerignore, .gitattributes…).
  cp -R "$_top/." "$_dir/"
  rm -rf "$_tmp"
  # Where update.sh gets the next version from, and what this one is.
  {
    say "# Written by install.sh: this install was downloaded, not cloned with git."
    say "# update.sh downloads the newer files from here."
    say "branch=$BRANCH"
    say "commit=$(printf '%s' "$_sha" | cut -c1-7)"
    say "date=$_date"
  } > "$_dir/.arrmada-source"
}

# ── the compose override (mounts) ───────────────────────────────────────────────
OVERRIDE_MARK="# Auto-generated by install.sh"
# write_override MEDIA TRANSCODE GPU -> docker-compose.override.yml with just the pieces
# that apply, so it's always valid YAML. Media goes to /storage, never /data (the database).
write_override() {
  _m="$1"; _t="$2"; _g="$3"
  if [ -z "$_m" ] && [ -z "$_t" ] && [ -z "$_g" ]; then
    rm -f docker-compose.override.yml
    return
  fi
  {
    say "$OVERRIDE_MARK — merged into docker-compose.yml."
    say "# Change it with the installer (--media / --transcode), or edit it and run update.sh."
    say "services:"
    say "  arrmada-app:"
    if [ -n "$_m" ] || [ -n "$_t" ]; then
      say "    volumes:"
      [ -n "$_m" ] && say "      - \"$_m:/storage\""
      [ -n "$_t" ] && say "      - \"$_t:/transcode\""
    fi
    if [ -n "$_g" ]; then
      say "    devices:"
      say "      - /dev/dri:/dev/dri"
    fi
    if [ -n "$_m" ]; then
      say "  arrmada-qbittorrent:"
      say "    volumes:"
      say "      - \"$_m:/storage\""
    fi
  } > docker-compose.override.yml
}

# override_value TARGET -> the host folder the generated override mounts at TARGET.
override_value() {
  sed -n "s#^      - \"\\(.*\\):$1\"\$#\\1#p" docker-compose.override.yml 2>/dev/null | head -n 1
}

# ── the image ───────────────────────────────────────────────────────────────────
# pull_image REF -> 0 once REF is downloaded and tagged arrmada:dev (the name
# docker-compose.yml runs, as update.sh does it).
pull_image() {
  say "Downloading the Arrmada image ($1)…"
  docker pull "$1" </dev/null && docker image tag "$1" arrmada:dev
}

# stamp_build: what a build from source is stamped with (Dashboard, update check) — a
# release tag, or the commit's date with the short commit, as update.sh does it.
stamp_build() {
  if [ -d .git ] && have git; then
    ARRMADA_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
    ARRMADA_VERSION=$(git describe --tags --exact-match 2>/dev/null || git log -1 --format=%cd --date=format:%Y.%m.%d 2>/dev/null || echo dev)
  else
    ARRMADA_COMMIT=$(sed -n 's/^commit=//p' .arrmada-source 2>/dev/null | head -n 1)
    ARRMADA_VERSION=$(sed -n 's/^date=//p' .arrmada-source 2>/dev/null | head -n 1)
    ARRMADA_COMMIT=${ARRMADA_COMMIT:-unknown}
    ARRMADA_VERSION=${ARRMADA_VERSION:-dev}
  fi
  export ARRMADA_VERSION ARRMADA_COMMIT
}

# ── an install that's already here ──────────────────────────────────────────────
existing_install() {
  _dir="$1"
  cd "$_dir"
  say "Arrmada is already installed in $_dir."

  _changed=""
  if [ -n "$MEDIA_SET" ] || [ -n "$TRANSCODE_SET" ]; then
    if [ -f docker-compose.override.yml ] && ! head -n 1 docker-compose.override.yml | grep -q "^$OVERRIDE_MARK"; then
      die "docker-compose.override.yml here was written by hand, so the installer won't change it. Edit its mounts yourself, then run update.sh."
    fi
    _m=$(override_value /storage); _t=$(override_value /transcode)
    _g=""; grep -q '/dev/dri:/dev/dri' docker-compose.override.yml 2>/dev/null && _g=1
    [ -n "$MEDIA_SET" ] && _m="$MEDIA"
    [ -n "$TRANSCODE_SET" ] && _t="$TRANSCODE"
    if [ -n "$_m" ] && [ ! -d "$_m" ]; then die "$_m doesn't exist. Check the path (it's case-sensitive)."; fi
    if [ -n "$_t" ]; then mkdir -p "$_t" 2>/dev/null || true; fi
    [ -f docker-compose.override.yml ] && cp docker-compose.override.yml docker-compose.override.yml.bak
    write_override "$_m" "$_t" "$_g"
    if [ -n "$MEDIA_SET" ]; then
      grep -v '^ARRMADA_STORAGE_HOST=' .env > .env.tmp || true
      [ -n "$_m" ] && say "ARRMADA_STORAGE_HOST=$_m" >> .env.tmp
      mv .env.tmp .env
    fi
    say "✓ Mounts updated${_m:+: media $_m → /storage}${_t:+, transcode $_t → /transcode}"
    _changed=1
  fi
  if [ -n "$PROWLARR" ] && [ "$(env_get COMPOSE_PROFILES)" != prowlarr ]; then
    if grep -qE '^COMPOSE_PROFILES=' .env; then
      die "COMPOSE_PROFILES is already set in .env; add prowlarr to it yourself, then run: docker compose up -d"
    fi
    { say ""; say "# Optional companions to run (added by install.sh --with-prowlarr)."; say "COMPOSE_PROFILES=prowlarr"; } >> .env
    say "✓ Prowlarr added."
    _changed=1
  fi

  if [ -n "$_changed" ] || ! docker inspect Arrmada-app >/dev/null 2>&1; then
    # New mounts or companions, or nothing running at all: (re)start the whole stack on
    # the image it already has. A stack that has never been built builds or downloads now.
    if ! docker image inspect arrmada:dev >/dev/null 2>&1; then
      _img=$(env_get ARRMADA_IMAGE)
      if [ -n "$_img" ]; then pull_image "$_img" || die "Couldn't download $_img."; else stamp_build; fi
    fi
    say "Starting Arrmada…"
    docker compose up -d </dev/null || fail_start
    wait_healthy || fail_start
    say ""
    say "✓ Arrmada is running.  Open http://$(host_ip):$(env_get ARRMADA_PORT)"
    return
  fi

  # Otherwise: run again means "update". update.sh backs up the database first, keeps the
  # running version for --rollback, and goes back to it if the new one doesn't start.
  say "Updating it (the same as running $_dir/update.sh)…"
  say ""
  set --
  if [ -n "$YES" ]; then set -- -y; fi
  if [ -n "$TTY" ]; then
    exec sh ./update.sh "$@" </dev/tty
  fi
  exec sh ./update.sh "$@" </dev/null
}

# ── a new install ───────────────────────────────────────────────────────────────
new_install() {
  _dir="$1"; _from_checkout="$2"; _reuse=""

  # 1. Where the files go (asked only when piped in; a checkout installs where it is).
  if [ -z "$_from_checkout" ]; then
    if [ -z "$DIR_SET" ]; then
      say "1/2  Where should Arrmada keep its settings and database?"
      _dir=$(ask "     Folder [Enter = $_dir]: " "$_dir")
    fi
    case "$_dir" in /*) ;; *) _dir="$(pwd)/$_dir" ;; esac
    if [ -f "$_dir/arrmada.db" ]; then
      die "$_dir holds an Arrmada database (an older install's data folder). Pick another folder with --dir."
    fi
    # A run that stopped after downloading (no .env yet) carries on with those files.
    if [ -f "$_dir/docker-compose.yml" ] && [ -f "$_dir/update.sh" ]; then
      _reuse=1
    elif [ -d "$_dir" ] && [ -n "$(ls -A "$_dir" 2>/dev/null)" ]; then
      die "$_dir isn't empty. Pick a new or empty folder with --dir."
    fi
    say ""
  fi

  # 2. The media folder: the one thing only you know. It has to be mounted before the
  # container starts; each library folder inside it is picked in the app.
  if [ -z "$MEDIA_SET" ]; then
    _suggest=""
    if is_unraid; then
      for _s in /mnt/user/data /mnt/user/media; do
        if [ -d "$_s" ]; then _suggest=$_s; break; fi
      done
    fi
    _n="2/2"; [ -n "$_from_checkout" ] && _n="1/1"
    say "$_n  Where does your media live? Give the folder that CONTAINS your libraries and"
    say "     downloads (e.g. /mnt/user/data), so imports can hardlink instead of copying."
    say "     It's mounted at /storage; you pick each library inside it in the app."
    if [ -n "$_suggest" ]; then
      MEDIA=$(ask "     Media folder [Enter = $_suggest, \"none\" = managed storage]: " "$_suggest")
    else
      MEDIA=$(ask "     Media folder [Enter = Arrmada's own managed storage]: " "")
    fi
    [ "$MEDIA" = none ] && MEDIA=""
    # On Unraid, managed storage is a Docker volume inside docker.img (20 GB by default),
    # which downloads fill in no time. Say so once before accepting it.
    if [ -z "$MEDIA" ] && is_unraid && [ -n "$TTY" ]; then
      say "     ! On Unraid, managed storage lives inside docker.img, and downloads will fill it."
      say "       Give a share instead (e.g. /mnt/user/data), or press Enter again to use it anyway."
      MEDIA=$(ask "     Media folder [Enter = managed storage]: " "")
    fi
    # A mistyped folder would be created empty (and owned by root) by Docker, and the app
    # would find no media. Ask again rather than guess.
    while [ -n "$MEDIA" ] && [ ! -d "$MEDIA" ]; do
      [ -n "$TTY" ] || die "$MEDIA doesn't exist. Check the path (it's case-sensitive)."
      say "     ! $MEDIA doesn't exist. Check the path (it's case-sensitive)."
      MEDIA=$(ask "     Media folder [Enter = managed storage]: " "")
    done
    say ""
  elif [ -n "$MEDIA" ] && [ ! -d "$MEDIA" ]; then
    die "$MEDIA doesn't exist. Check the path (it's case-sensitive)."
  fi
  # Everything else is worked out, shown, and can be changed later.
  DATA="$_dir/data"
  if [ -z "$TRANSCODE_SET" ]; then TRANSCODE="$_dir/transcode"; fi

  TZONE="$TZ_ARG"
  [ -z "$TZONE" ] && TZONE=$(detect_tz)
  if [ -z "$TZONE" ]; then
    say "What's your timezone? Schedules like overnight encoding run on this clock."
    say "Region/City, e.g. Australia/Brisbane, America/New_York, Europe/London."
    TZONE=$(ask "Timezone [Enter = Etc/UTC]: " "Etc/UTC")
  fi
  if ! valid_tz "$TZONE"; then
    _fixed=$(tz_fix_case "$TZONE")
    if [ -n "$_fixed" ]; then TZONE=$_fixed; else err "! \"$TZONE\" isn't a timezone name; using Etc/UTC (change TZ in .env later)."; TZONE="Etc/UTC"; fi
  fi

  # The user to run as: whoever owns the media folder, so the app can read and write it;
  # else Unraid's nobody/users (99/100), the user who ran sudo, or a generic 1000/1000.
  if [ -z "$PUID" ] || [ -z "$PGID" ]; then
    _owner=""
    [ -n "$MEDIA" ] && _owner=$(stat -c '%u:%g' "$MEDIA" 2>/dev/null || true)
    case "$_owner" in
      "" | "0:0")
        if is_unraid; then _owner="99:100"
        elif [ -n "${SUDO_UID:-}" ] && [ -n "${SUDO_GID:-}" ]; then _owner="$SUDO_UID:$SUDO_GID"
        elif [ "$(id -u)" != 0 ]; then _owner="$(id -u):$(id -g)"
        else _owner="1000:1000"; fi ;;
    esac
    [ -z "$PUID" ] && PUID=${_owner%:*}
    [ -z "$PGID" ] && PGID=${_owner#*:}
  fi

  WEBPORT=$(free_port 7878 7979 8790 8385)
  QBWEB=$(free_port 8080 8081 8082)
  PROWPORT=$(free_port 9696 9697 9698)
  AUDIOPORT=$(free_port 13379 13380 13381 13382)
  BTPORT=$(rand_port)
  GPU=""
  [ -e /dev/dri ] && GPU=1

  # Download, or compile here? The published image takes minutes; compiling takes the
  # better part of an hour. Decided before anything is written, so the summary says which.
  IMAGE_REF="$IMAGE_ARG"
  [ -z "$IMAGE_REF" ] && [ -z "$BUILD" ] && IMAGE_REF="$DEFAULT_IMAGE"

  say "Ready to install Arrmada:"
  say "   • Folder:        $_dir   (database in $DATA)"
  if [ -n "$MEDIA" ]; then
    say "   • Media:         $MEDIA → /storage inside the app"
  else
    say "   • Media:         Arrmada's own managed storage (no media folder)"
  fi
  say "   • Transcoding:   ${TRANSCODE:-inside the container}"
  say "   • Web UI port:   $WEBPORT"
  say "   • Timezone:      $TZONE"
  say "   • Run as:        $PUID:$PGID"
  if [ -n "$GPU" ]; then say "   • GPU:           /dev/dri found → hardware transcoding"; else say "   • GPU:           none found → Convert uses the CPU"; fi
  [ -n "$PROWLARR" ] && say "   • Prowlarr:      yes"
  if [ -n "$IMAGE_REF" ]; then say "   • App:           the published image ($IMAGE_REF)"; else say "   • App:           compiled on this server (--build)"; fi
  say ""
  if ! ask_yes "Go ahead?"; then
    say "Nothing was installed. Run the installer again to change the answers, or see its options with --help."
    exit 0
  fi
  say ""

  if [ -z "$_from_checkout" ] && [ -z "$_reuse" ]; then
    get_files "$_dir"
  fi
  cd "$_dir"

  # The image before .env, so a server that can't download it says so before anything
  # depends on the choice. An explicit --image that won't download is an error; the
  # default one may simply not be published yet, so then it's compiled here instead.
  if [ -n "$IMAGE_REF" ] && ! pull_image "$IMAGE_REF"; then
    if [ -n "$IMAGE_ARG" ]; then
      die "Couldn't download $IMAGE_REF. Check the name, or run without --image."
    fi
    say ""
    say "! The published image isn't available yet, so Arrmada will be compiled on this server."
    say "  That takes 30–60 minutes the first time. Updates switch to the image once it's published."
    IMAGE_REF=""
  fi

  {
    say "# ─── Arrmada settings ─── change a value, then run update.sh to apply ───"
    say "# API keys and passwords are entered in the app, never here."
    say ""
    if [ -n "$IMAGE_REF" ]; then
      say "# The published image update.sh downloads (delete the line to compile here instead)."
      say "ARRMADA_IMAGE=$IMAGE_REF"
      say ""
    elif [ -z "$BUILD" ]; then
      say "# When a release is published, uncomment this so updates download it instead of compiling:"
      say "# ARRMADA_IMAGE=$DEFAULT_IMAGE"
      say ""
    fi
    say "# Host ports (picked free at install so nothing clashes with your other apps)."
    say "ARRMADA_PORT=$WEBPORT"
    say "ARRMADA_QBIT_WEBUI_PORT=$QBWEB"
    say "ARRMADA_PROWLARR_PORT=$PROWPORT"
    say "# Audiobook server for listening apps (off until switched on in the app)."
    say "ARRMADA_AUDIOBOOK_PORT=$AUDIOPORT"
    say "# BitTorrent: forward this port on your router (TCP and UDP)."
    say "ARRMADA_QBIT_PORT=$BTPORT"
    say ""
    say "# Timezone. Schedules (the encode window especially) run in this zone."
    say "# Full list: en.wikipedia.org/wiki/List_of_tz_database_time_zones"
    say "TZ=$TZONE"
    say ""
    say "# The user and group Arrmada and qBittorrent run as."
    say "ARRMADA_PUID=$PUID"
    say "ARRMADA_PGID=$PGID"
    say ""
    say "# The database and settings live here."
    say "ARRMADA_DATA_HOST=$DATA"
    if [ -n "$MEDIA" ]; then
      say ""
      say "# Your media folder, mounted at /storage (see docker-compose.override.yml)."
      say "ARRMADA_STORAGE_HOST=$MEDIA"
    fi
    if [ -n "$PROWLARR" ]; then
      say ""
      say "# Optional companions to run."
      say "COMPOSE_PROFILES=prowlarr"
    fi
  } > .env
  write_override "$MEDIA" "$TRANSCODE" "$GPU"
  mkdir -p "$DATA"
  if [ -n "$TRANSCODE" ]; then mkdir -p "$TRANSCODE" 2>/dev/null || true; fi

  if [ -n "$IMAGE_REF" ]; then
    say "Starting Arrmada…"
    docker compose up -d --no-build </dev/null || fail_start
  else
    stamp_build
    say "Compiling and starting Arrmada… (the first build compiles everything — this takes a while)"
    docker compose up -d --build </dev/null || fail_start
  fi
  wait_healthy || fail_start

  say ""
  say "✓ Arrmada is running.  Open http://$(host_ip):$WEBPORT"
  say ""
  say "  Create your admin account there; the setup then walks you through the rest"
  say "  (TMDB key, library folders, Plex, indexers)."
  say ""
  say "  Update later: run the same install line again, or:  sh $_dir/update.sh"
  say "  BitTorrent port $BTPORT — forward it on your router (TCP and UDP) for faster downloads."
  if [ -z "$PROWLARR" ]; then
    say "  Prowlarr wasn't installed (it's optional). To add it:  sh $_dir/install.sh --with-prowlarr"
  fi
}

main() {
  DIR_ARG=""; DIR_SET=""; MEDIA=""; MEDIA_SET=""; TRANSCODE=""; TRANSCODE_SET=""
  TZ_ARG=""; PUID=""; PGID=""; PROWLARR=""; IMAGE_ARG=""; BUILD=""; YES=""
  _self="$0"
  while [ $# -gt 0 ]; do
    _opt="$1"; _val=""
    case "$_opt" in
      --*=*) _val="${_opt#*=}"; _opt="${_opt%%=*}" ;;
    esac
    case "$_opt" in
      --dir | --media | --transcode | --tz | --puid | --pgid | --image)
        if [ -z "$_val" ]; then
          [ $# -ge 2 ] || die "$_opt needs a value."
          _val="$2"; shift
        fi ;;
    esac
    case "$_opt" in
      --dir) DIR_ARG="${_val%/}"; DIR_SET=1 ;;
      --media) MEDIA="${_val%/}"; MEDIA_SET=1; [ "$MEDIA" = none ] && MEDIA="" ;;
      --no-media) MEDIA=""; MEDIA_SET=1 ;;
      --transcode) TRANSCODE="${_val%/}"; TRANSCODE_SET=1; [ "$TRANSCODE" = none ] && TRANSCODE="" ;;
      --tz) TZ_ARG="$_val" ;;
      --puid) PUID="$_val" ;;
      --pgid) PGID="$_val" ;;
      --with-prowlarr) PROWLARR=1 ;;
      --image) IMAGE_ARG="$_val" ;;
      --build) BUILD=1 ;;
      -y | --yes) YES=1 ;;
      -h | --help) usage; exit 0 ;;
      *) die "Unknown option: $1 (see --help)" ;;
    esac
    shift
  done
  [ -n "$BUILD" ] && [ -n "$IMAGE_ARG" ] && die "--build and --image don't go together."
  tty_init

  say "⛵ Arrmada installer"
  say ""

  # Prerequisites, each with what to do about it.
  case "$(uname -m)" in
    x86_64 | amd64) ;;
    *) die "Arrmada runs on 64-bit Intel/AMD (x86-64) servers; this one is $(uname -m)." ;;
  esac
  if ! have docker; then
    if is_unraid; then die "Docker isn't running. Enable it in Settings → Docker, then run the installer again."; fi
    err "✗ Docker isn't installed. Install Docker Engine (https://docs.docker.com/engine/install/),"
    err "  then run the installer again."
    exit 1
  fi
  if ! docker info >/dev/null 2>&1; then
    err "✗ Can't talk to Docker. Either it isn't running, or this user isn't allowed to use it."
    err "  Run the installer as root (put sudo before sh: … | sudo sh), or add yourself to the"
    err "  docker group, then run it again."
    exit 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    if is_unraid; then
      die "Docker Compose isn't installed. Install the \"Docker Compose Manager\" plugin from the Apps tab, then run the installer again."
    fi
    err "✗ Docker Compose (v2, \"docker compose\") isn't available. Install the docker-compose-plugin"
    err "  package (https://docs.docker.com/compose/install/linux/), then run the installer again."
    exit 1
  fi

  # From a checkout (./install.sh), install right here. Piped in, find or pick a folder.
  _here=""
  if [ -f "$_self" ] && [ -f "$(dirname "$_self")/docker-compose.yml" ] && [ -f "$(dirname "$_self")/Dockerfile" ]; then
    _here=$(cd "$(dirname "$_self")" && pwd)
  fi

  if [ -n "$_here" ]; then
    if [ -n "$DIR_SET" ] && [ "$DIR_ARG" != "$_here" ]; then
      die "This install.sh is in $_here and installs there; --dir is for the one-line install."
    fi
    if [ -f "$_here/.env" ]; then existing_install "$_here"; return; fi
    new_install "$_here" 1
    return
  fi

  if [ -n "$DIR_SET" ]; then
    if is_install "$DIR_ARG"; then existing_install "$DIR_ARG"; return; fi
  else
    _found=$(existing_dir)
    if [ -n "$_found" ]; then existing_install "$_found"; return; fi
  fi
  # A container named Arrmada-app whose folder can't be found (or isn't the --dir given)
  # is still an install: a second one would collide with it.
  if docker inspect Arrmada-app >/dev/null 2>&1; then
    _wd=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' Arrmada-app 2>/dev/null || true)
    err "✗ Arrmada is already installed here (container Arrmada-app${_wd:+, started from $_wd}),"
    err "  but its folder wasn't found. Run update.sh in that folder to update it, or point"
    err "  the installer at it:  $ONE_LINER -s -- --dir /path/to/it"
    exit 1
  fi
  if [ -n "$DIR_SET" ]; then new_install "$DIR_ARG" ""; else new_install "$(default_dir)" ""; fi
}

# Everything runs from here, on the last line: piped into sh, the whole script has been
# read before anything happens, so a download cut short can't run half an installer.
main "$@"
