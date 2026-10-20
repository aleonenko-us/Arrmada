#!/bin/sh
# Arrmada's updater: the one place that may touch Docker, so the app itself never can.
#
# It runs in its own small container (the arrmada-updater service, compose profile
# "updater", started by `install.sh --with-updater`) and answers two requests from the
# app over the private compose network. Nothing is published on the host.
#
#   GET  /cgi-bin/updater?status   what the last (or running) update is doing
#   POST /cgi-bin/updater?apply    run update.sh -y in the install folder, in the background
#
# Every request needs "Authorization: Bearer $ARRMADA_UPDATER_TOKEN"; with no token set
# it refuses everything. The update itself is update.sh, unchanged: it backs up the
# database, brings the new version, restarts the app and goes back by itself if the new
# version doesn't start. This script only starts it and reports how it's going.
#
# The install folder is mounted at the same path it has on the host (ARRMADA_HOST_DIR), so
# update.sh's relative paths mean the same thing to the Docker daemon as they do to us.

DIR="${ARRMADA_HOST_DIR:-}"
STATE="$DIR/.arrmada-updater"
LOG="$STATE/update.log"
RC="$STATE/exit-code"
PIDF="$STATE/pid"
STARTED="$STATE/started-at"

# reply STATUS-LINE JSON-BODY
reply() {
  printf 'Status: %s\r\nContent-Type: application/json\r\nCache-Control: no-store\r\n\r\n%s\n' "$1" "$2"
  exit 0
}

# json_escape reads stdin and prints it as the inside of a JSON string.
json_escape() {
  # Drop control characters other than newline, escape backslash and quote, join lines
  # with \n.
  tr -d '\000-\010\013-\037' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | awk 'BEGIN { ORS = "" } { if (NR > 1) print "\\n"; print }'
}

running() {
  [ -f "$PIDF" ] && kill -0 "$(cat "$PIDF" 2>/dev/null)" 2>/dev/null
}

# state prints idle | running | done | failed.
state() {
  if running; then echo running; return; fi
  if [ -f "$RC" ]; then
    if [ "$(cat "$RC")" = 0 ]; then echo done; else echo failed; fi
    return
  fi
  echo idle
}

status_json() {
  _s=$(state)
  _tail=""
  [ -f "$LOG" ] && _tail=$(tail -n 30 "$LOG" | json_escape)
  _since=""
  [ -f "$STARTED" ] && _since=$(cat "$STARTED")
  printf '{"state":"%s","started_at":"%s","log":"%s"}' "$_s" "$_since" "$_tail"
}

# --- who is asking ---------------------------------------------------------------------
if [ -z "${ARRMADA_UPDATER_TOKEN:-}" ] || [ -z "$DIR" ] || [ ! -d "$DIR" ]; then
  reply "503 Service Unavailable" '{"error":"the updater isn'"'"'t set up (no token or install folder)"}'
fi
if [ "${HTTP_AUTHORIZATION:-}" != "Bearer $ARRMADA_UPDATER_TOKEN" ]; then
  reply "401 Unauthorized" '{"error":"unauthorized"}'
fi
mkdir -p "$STATE" 2>/dev/null

case "${REQUEST_METHOD:-GET}:${QUERY_STRING:-}" in
  GET:status)
    reply "200 OK" "$(status_json)"
    ;;
  POST:apply)
    if running; then
      reply "409 Conflict" '{"error":"an update is already running","state":"running"}'
    fi
    if [ ! -f "$DIR/update.sh" ]; then
      reply "500 Internal Server Error" '{"error":"update.sh isn'"'"'t in the install folder"}'
    fi
    rm -f "$RC"
    date -u '+%Y-%m-%dT%H:%M:%SZ' > "$STARTED"
    : > "$LOG"
    # Detached, with its output in the log: the request must be able to finish while the
    # update (which restarts the app) carries on.
    (
      cd "$DIR" || exit 1
      ARRMADA_UPDATE_FORCE=1 sh ./update.sh -y >> "$LOG" 2>&1 < /dev/null
      echo $? > "$RC"
    ) > /dev/null 2>&1 < /dev/null &
    echo $! > "$PIDF"
    reply "202 Accepted" '{"state":"running"}'
    ;;
  *)
    reply "404 Not Found" '{"error":"not found"}'
    ;;
esac
