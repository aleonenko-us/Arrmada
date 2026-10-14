#!/bin/sh
# usenet-it.sh — run the usenet live tests against a real, throwaway SABnzbd, offline.
#
# Nothing leaves the machine: the "Usenet provider" is the fake news server in the Go test
# process (internal/download/nntptest), which SABnzbd reaches over the rig's own Docker
# network. The rig is a pinned SABnzbd container, a downloads volume shared with the Go
# tests (SABnzbd sees it at /downloads, the tests at /local, so path mappings are real), and
# a fixtures volume of releases made with SABnzbd's own par2 and 7z: a 52 MB file with a
# par2 set (repair/) and a password-protected 7z (pw/). The tests then run in golang:1.25 on
# the same network:
#
#   TestSABnzbdLive            (download)   the client: test, categories, add, queue, history,
#                                           remove, the host_whitelist and wrong-key messages
#   TestUsenetLifecycleLive    (automation) grab → import → clean-up; missing articles → blocklist
#   TestUsenetPar2RepairLive   (automation) ~3% of the articles gone, par2 repairs it → imported
#   TestUsenetPasswordedLive   (automation) a passworded 7z → failed, bad release, searched again
#   TestUsenetSABRestartLive   (automation) SABnzbd restarts mid-download → nothing condemned
#
# The API key SABnzbd generated is read inside its container and handed to the tests in
# their environment; it's never printed. Everything is removed on exit, pass or fail, and
# only the rig's own containers, network and volumes are touched (all named
# arrmada-tests-usenet-<random>).
#
# Usage (from anywhere in the repo; needs docker):
#   scripts/usenet-it.sh                         # every test above
#   scripts/usenet-it.sh -run TestUsenetSABRestartLive   # extra arguments go to `go test`
#
# Overridable images: SAB_IMAGE (keep it on the version docker-compose.yml pins), GO_IMAGE.

set -eu

SAB_IMAGE=${SAB_IMAGE:-lscr.io/linuxserver/sabnzbd:5.1.3}
GO_IMAGE=${GO_IMAGE:-golang:1.25}
TESTS='^(TestSABnzbdLive|TestUsenetLifecycleLive|TestUsenetPar2RepairLive|TestUsenetPasswordedLive|TestUsenetSABRestartLive)$'

# Git Bash would rewrite /downloads and friends into Windows paths before docker sees them.
MSYS_NO_PATHCONV=1
export MSYS_NO_PATHCONV

root=$(cd "$(dirname "$0")/.." && pwd)
src=$root
if command -v cygpath >/dev/null 2>&1; then
	src=$(cygpath -m "$root")
fi

tag=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
name=arrmada-tests-usenet-$tag
net=$name-net
dl=$name-downloads
fix=$name-fixtures

cleanup() {
	status=$?
	docker rm -f "$name-go" "$name-fix" "$name" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
	docker volume rm "$dl" "$fix" >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'usenet-it: %s\n' "$*" >&2
	exit 1
}

docker network create "$net" >/dev/null
docker volume create "$dl" >/dev/null
docker volume create "$fix" >/dev/null
# SABnzbd runs as 1000 and writes the downloads volume; the tests (root) read and tidy it.
docker run --rm -v "$dl:/d" alpine:3.20 chown 1000:1000 /d

# ---- releases made with SABnzbd's own tools ----------------------------------------------

say "making the fixtures with $SAB_IMAGE's par2 and 7z"
docker run --rm -i --name "$name-fix" --cpus=2 --entrypoint sh -v "$fix:/fix" "$SAB_IMAGE" -s <<'FIX'
set -eu
mkdir -p /fix/repair /fix/pw
cd /fix/repair
f=Show.S01E01.1080p.WEB-DL-REPAIR
# 52 MiB: above the importer's 50 MB sample floor.
head -c 54525952 /dev/urandom >"$f.mkv"
par2 create -q -q -r10 -n4 "$f.par2" "$f.mkv" >/dev/null
cd /fix/pw
f=Show.S01E02.1080p.WEB-DL-PW
head -c 2097152 /dev/urandom >"$f.mkv"
pw=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
7z a -bd -bso0 -bsp0 -p"$pw" -mhe=on "$f.7z" "$f.mkv"
rm "$f.mkv"
chmod -R a+rX /fix
ls -l /fix/repair /fix/pw >&2
FIX

# ---- SABnzbd -----------------------------------------------------------------------------

say "starting $SAB_IMAGE as $name"
docker run -d --name "$name" --cpus=2 --network "$net" -e PUID=1000 -e PGID=1000 -e TZ=Etc/UTC \
	-v "$dl:/downloads" "$SAB_IMAGE" >/dev/null

# Asked from inside its own container, so its API key never leaves it except into the
# tests' environment.
sab() { # sab "mode=…&…" — one API call, answer on stdout
	docker exec -e Q="$1" "$name" sh -c \
		'curl -fsS -m 20 -X POST http://127.0.0.1:8080/api --data "$Q&output=json&apikey=$(sed -n "s/^api_key = //p" /config/sabnzbd.ini)"'
}

say "waiting for SABnzbd"
i=0
until sab "mode=version" 2>/dev/null | grep -q '"version"'; do
	i=$((i + 1))
	[ "$i" -lt 90 ] || die "SABnzbd did not come up (docker logs $name)"
	sleep 2
done
sab "mode=set_config&section=misc&keyword=download_dir&value=/downloads/incomplete" >/dev/null
sab "mode=set_config&section=misc&keyword=complete_dir&value=/downloads/complete" >/dev/null
sab "mode=fullstatus&skip_dashboard=1" | grep -q '"completedir":"/downloads/complete"' || die "SABnzbd didn't take its folders"

ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$name")
[ -n "$ip" ] || die "no address for $name"
ARRMADA_SABNZBD_KEY=$(docker exec "$name" sed -n 's/^api_key = //p' /config/sabnzbd.ini)
[ "${#ARRMADA_SABNZBD_KEY}" -ge 16 ] || die "couldn't read SABnzbd's API key"

ARRMADA_SABNZBD_IT=1
# By address: SABnzbd doesn't check the name of an IP. By container name, it refuses.
ARRMADA_SABNZBD_URL=http://$ip:8080
ARRMADA_SABNZBD_HOSTNAME_URL=http://$name:8080
ARRMADA_SABNZBD_NNTP_HOST=$name-go
ARRMADA_SABNZBD_REMOTE=/downloads
ARRMADA_SABNZBD_LOCAL=/local
ARRMADA_SABNZBD_FIXTURES=/fixtures
export ARRMADA_SABNZBD_IT ARRMADA_SABNZBD_URL ARRMADA_SABNZBD_KEY ARRMADA_SABNZBD_HOSTNAME_URL \
	ARRMADA_SABNZBD_NNTP_HOST ARRMADA_SABNZBD_REMOTE ARRMADA_SABNZBD_LOCAL ARRMADA_SABNZBD_FIXTURES

say "running the usenet live tests"
# One package after the other (-p 1): they share the one SABnzbd.
docker run --rm --name "$name-go" --cpus=3 --network "$net" \
	-v "$src:/src" -v arrmada-gomod:/go/pkg/mod -v arrmada-gocache:/root/.cache/go-build \
	-v "$dl:/local" -v "$fix:/fixtures:ro" -w /src \
	-e ARRMADA_SABNZBD_IT -e ARRMADA_SABNZBD_URL -e ARRMADA_SABNZBD_KEY -e ARRMADA_SABNZBD_HOSTNAME_URL \
	-e ARRMADA_SABNZBD_NNTP_HOST -e ARRMADA_SABNZBD_REMOTE -e ARRMADA_SABNZBD_LOCAL -e ARRMADA_SABNZBD_FIXTURES \
	"$GO_IMAGE" go test -count=1 -p 1 -timeout 40m -v -run "$TESTS" "$@" ./internal/download/ ./internal/automation/
