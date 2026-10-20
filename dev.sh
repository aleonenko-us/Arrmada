#!/bin/sh
# Run Arrmada locally for development. One app, one port (7979).
#   ./dev.sh build         rebuild the web UI and the binary
#   ./dev.sh run           (re)start it on the NAS configuration copy in .dev-data
#   ./dev.sh run --fresh   (re)start it as an EMPTY install, to see first-run onboarding
#   ./dev.sh run --resume  (re)start the empty install where it was left (not wiped)
#   ./dev.sh stop          stop it
# run rebuilds first, so what's on the port is always the code in the checkout.
# Starting always replaces whatever is already on the port, so there is never a second copy.
# .dev-data holds a copy of the NAS configuration with every live link switched off
# (download clients, indexers, Plex, Prowlarr, queued messages), so it can't act on the
# real setup. .dev-fresh is wiped on every --fresh. Never point this at the NAS's data.
set -eu
cd "$(dirname "$0")"
PORT="${ARRMADA_PORT:-7979}"

stop_app() {
  pid=$(ss -ltnp 2>/dev/null | grep ":$PORT " | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | head -1)
  if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; sleep 2; fi
}

case "${1:-run}" in
  build) (cd web && npm run build) && go build -o bin/arrmada ./cmd/arrmada ;;
  stop)  stop_app ;;
  run)   (cd web && npm run build) && go build -o bin/arrmada ./cmd/arrmada
         stop_app
         dir=.dev-data
         case "${2:-}" in
           --fresh)  dir=.dev-fresh; rm -rf "$dir" ;;
           --resume) dir=.dev-fresh ;;
         esac
         mkdir -p "$dir"
         ARRMADA_DATA_DIR="$PWD/$dir" ARRMADA_PORT="$PORT" exec ./bin/arrmada ;;
  *)     echo "usage: ./dev.sh [build|run [--fresh]|stop]" >&2; exit 2 ;;
esac
