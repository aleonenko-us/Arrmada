#!/bin/sh
# Run Arrmada locally for development: the built binary, a throwaway data folder, :7979.
# Rebuild first with ./dev.sh build. Never points at the NAS or its data.
set -eu
cd "$(dirname "$0")"
case "${1:-run}" in
  build) (cd web && npm run build) && go build -o bin/arrmada ./cmd/arrmada ;;
  run)   mkdir -p .dev-data
         ARRMADA_DATA_DIR="$PWD/.dev-data" ARRMADA_PORT="${ARRMADA_PORT:-7979}" exec ./bin/arrmada ;;
  *)     echo "usage: ./dev.sh [build|run]" >&2; exit 2 ;;
esac
