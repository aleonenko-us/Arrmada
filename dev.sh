#!/bin/sh
# Run Arrmada locally for development: the built binary, the data folder in .dev-data, :7979.
#   ./dev.sh build   rebuild the web UI and the binary
#   ./dev.sh run     start it (default)
# .dev-data holds a copy of the NAS configuration with every live link switched off
# (download clients, indexers, Plex, Prowlarr, queued messages), so it can't act on the
# real setup. Never point this at the NAS's own data folder.
set -eu
cd "$(dirname "$0")"
case "${1:-run}" in
  build) (cd web && npm run build) && go build -o bin/arrmada ./cmd/arrmada ;;
  run)   mkdir -p .dev-data
         ARRMADA_DATA_DIR="$PWD/.dev-data" ARRMADA_PORT="${ARRMADA_PORT:-7979}" exec ./bin/arrmada ;;
  *)     echo "usage: ./dev.sh [build|run]" >&2; exit 2 ;;
esac
