#!/bin/sh
# jellyfin-it.sh — run internal/jellyfin's live tests against a real, throwaway Jellyfin.
#
# Everything it builds is synthetic and temporary: 10-second ffmpeg test-pattern clips named
# after real titles (with NFO files carrying their TMDB/TVDB/IMDb ids, so matching works with
# every internet metadata source switched off), a pinned jellyfin/jellyfin container that only
# listens on 127.0.0.1, a first-run setup done over the API, an API key, a test user and Quick
# Connect. It never reads or mounts a real library. All of it is removed on exit, pass or fail.
#
# The media lives in a Docker volume rather than a host folder: Jellyfin, the ffmpeg container
# and the Go test container all mount it at /media, so a folder the tests add is at the same
# path Jellyfin sees, and no host-path translation is needed (Docker Desktop on Windows mangles
# bind-mounted temp paths). The Go tests run in golang:1.25 on the rig's own Docker network and
# reach Jellyfin by container name.
#
# Usage (from anywhere in the repo; needs docker and curl):
#   scripts/jellyfin-it.sh                  # set up, run `go test -run IT` on internal/jellyfin,
#                                           # internal/jellyfinsvc and internal/insights
#   scripts/jellyfin-it.sh -run IT_Sessions # extra arguments go to `go test`
#   JELLYFIN_IT_PKGS=./internal/jellyfinsvc/... scripts/jellyfin-it.sh   # only these packages
#   JELLYFIN_IT_HOLD=1 scripts/jellyfin-it.sh
#       set up, then keep the server running until the printed hold file is deleted; the
#       connection details are in an env file (mode 600) next to it, never printed.
#
# Overridable images: JF_IMAGE, FF_IMAGE (anything with ffmpeg + libx264/libx265 as the
# entrypoint-free `ffmpeg` command), GO_IMAGE.

set -eu

JF_IMAGE=${JF_IMAGE:-jellyfin/jellyfin:12.2.20261005-225228}
FF_IMAGE=${FF_IMAGE:-arrmada:sycl}
GO_IMAGE=${GO_IMAGE:-golang:1.25}

# Git Bash would rewrite /media and friends into Windows paths before docker sees them.
MSYS_NO_PATHCONV=1
export MSYS_NO_PATHCONV

root=$(cd "$(dirname "$0")/.." && pwd)
src=$root
if command -v cygpath >/dev/null 2>&1; then
	src=$(cygpath -m "$root")
fi

tag=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
name=arrmada-tests-jellyfin-$tag
net=$name-net
vol=$name-media
work=$(mktemp -d)

cleanup() {
	status=$?
	docker rm -f "$name-go" "$name-ff" "$name" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
	docker volume rm "$vol" >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'jellyfin-it: %s\n' "$*" >&2
	exit 1
}

secret() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }

# ---- synthetic media -------------------------------------------------------------------

say "building synthetic media in volume $vol"
docker network create "$net" >/dev/null
docker volume create "$vol" >/dev/null
docker run --rm -i --name "$name-ff" --cpus=3 --entrypoint sh -v "$vol:/media" "$FF_IMAGE" -s <<'GEN'
set -eu
clip() { # clip <seconds> <codec args...> <out>
	s=$1
	shift
	ffmpeg -nostdin -hide_banner -loglevel error -y \
		-f lavfi -i "testsrc2=size=320x240:rate=24:duration=$s" \
		-f lavfi -i "sine=frequency=440:duration=$s" -shortest -c:a aac "$@"
}
clip 10 -c:v libx264 -preset ultrafast -pix_fmt yuv420p /tmp/h264.mkv
# Three minutes, so a transcode of it runs long enough to be seen in /Sessions.
clip 180 -c:v libx265 -preset ultrafast -pix_fmt yuv420p10le -x265-params log-level=error:pools=none /tmp/hevc10.mkv

put() { # put <clip> <path under /media>
	mkdir -p "$(dirname "/media/$2")"
	cp "/tmp/$1.mkv" "/media/$2"
}
movie_nfo() { # movie_nfo <folder> <title> <year> <tmdb> <imdb>
	cat >"/media/Movies/$1/movie.nfo" <<NFO
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<movie>
  <title>$2</title>
  <year>$3</year>
  <uniqueid type="tmdb" default="true">$4</uniqueid>
  <uniqueid type="imdb">$5</uniqueid>
</movie>
NFO
}

# NFO-matched movies, one per folder-id style Arrmada and Jellyfin use.
put h264 "Movies/The Matrix (1999) {tmdb-603}/The Matrix (1999).mkv"
movie_nfo "The Matrix (1999) {tmdb-603}" "The Matrix" 1999 603 tt0133093
put hevc10 "Movies/Inception (2010) [tmdbid-27205]/Inception (2010).mkv"
movie_nfo "Inception (2010) [tmdbid-27205]" "Inception" 2010 27205 tt1375666

# Arrmada's multi-track naming: two quality versions and an edition track in one folder.
d="Movies/Dune (2021) {tmdb-438631}"
put hevc10 "$d/Dune (2021) - 2160p.mkv"
put h264 "$d/Dune (2021) - 1080p.mkv"
put h264 "$d/Dune (2021) {edition-Extended} - 1080p.mkv"
movie_nfo "Dune (2021) {tmdb-438631}" "Dune" 2021 438631 tt1160419

# Which multi-file layouts Jellyfin folds into one movie with versions (no NFO: only the
# names carry the ids). TestIT_NamingGrouping pins the answer for each folder.
put h264 "Movies/Heat (1995) {tmdb-949}/Heat (1995) - 2160p.mkv"
put h264 "Movies/Heat (1995) {tmdb-949}/Heat (1995) - 1080p.mkv"
put h264 "Movies/Memento (2000)/Memento (2000) - 2160p.mkv"
put h264 "Movies/Memento (2000)/Memento (2000) - 1080p.mkv"
put h264 "Movies/Arrival (2016)/Arrival (2016) - 2160p.mkv"
put h264 "Movies/Arrival (2016)/Arrival (2016) - 1080p.mkv"
put h264 "Movies/Arrival (2016)/Arrival (2016) {edition-Extended} - 1080p.mkv"
put h264 "Movies/Zodiac (2007)/Zodiac (2007) - 2160p.mkv"
put h264 "Movies/Zodiac (2007)/Zodiac (2007) - 1080p {edition-Director's Cut}.mkv"
put h264 "Movies/Gravity (2013) {tmdb-49047}/Gravity (2013) {tmdb-49047} - 2160p.mkv"
put h264 "Movies/Gravity (2013) {tmdb-49047}/Gravity (2013) {tmdb-49047} - 1080p.mkv"
put h264 "Movies/Tenet (2020) {tmdb-577922}/Tenet (2020) {tmdb-577922} - 2160p.mkv"
put h264 "Movies/Tenet (2020) {tmdb-577922}/Tenet (2020) {tmdb-577922} - 1080p.mkv"
movie_nfo "Tenet (2020) {tmdb-577922}" "Tenet" 2020 577922 tt6723592
put h264 "Movies/Jaws (1975) {tmdb-578}/Jaws (1975).mkv"
put h264 "Movies/Alien (1979) [tmdbid-348]/Alien (1979).mkv"

# Hidden: a dot folder (Arrmada's recycle bin) and a folder with a .ignore marker.
put h264 "Movies/.arrmada-recycle/Recycled (2004)/Recycled (2004).mkv"
put h264 "Movies/Ignored (2003)/Ignored (2003).mkv"
: >"/media/Movies/Ignored (2003)/.ignore"

s="TV/Breaking Bad (2008) {tvdb-81189}"
put h264 "$s/Season 01/Breaking Bad - S01E01.mkv"
cat >"/media/$s/tvshow.nfo" <<NFO
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<tvshow>
  <title>Breaking Bad</title>
  <year>2008</year>
  <uniqueid type="tvdb" default="true">81189</uniqueid>
  <uniqueid type="tmdb">1396</uniqueid>
  <uniqueid type="imdb">tt0903747</uniqueid>
</tvshow>
NFO
cat >"/media/$s/Season 01/Breaking Bad - S01E01.nfo" <<NFO
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<episodedetails>
  <title>Pilot</title>
  <season>1</season>
  <episode>1</episode>
  <uniqueid type="tvdb" default="true">349232</uniqueid>
  <uniqueid type="imdb">tt0959621</uniqueid>
</episodedetails>
NFO
GEN

# ---- Jellyfin ----------------------------------------------------------------------------

say "starting $JF_IMAGE as $name"
docker run -d --name "$name" --cpus=3 --network "$net" -p 127.0.0.1::8096 \
	-v "$vol:/media:ro" "$JF_IMAGE" >/dev/null
port=$(docker port "$name" 8096/tcp | head -n1 | sed 's/.*://')
[ -n "$port" ] || die "no published port"
J=http://127.0.0.1:$port

# Calls before sign-in still name the client; Jellyfin wants the MediaBrowser header shape.
client='Client="arrmada-it", Device="jellyfin-it.sh", DeviceId="arrmada-it-setup", Version="0.1"'
auth="MediaBrowser $client"

api() { # api METHOD PATH [JSON]
	if [ $# -ge 3 ]; then
		curl -fsS -m 60 -X "$1" -H "Authorization: $auth" -H 'Content-Type: application/json' -d "$3" "$J$2"
	else
		curl -fsS -m 60 -X "$1" -H "Authorization: $auth" "$J$2"
	fi
}
field() { # field NAME < json — first string value of "NAME"
	sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | head -n1
}

say "waiting for Jellyfin on $J"
i=0
until curl -fsS -m 5 "$J/System/Info/Public" 2>/dev/null | grep -q '"StartupWizardCompleted":false'; do
	i=$((i + 1))
	[ "$i" -lt 90 ] || die "Jellyfin did not come up (docker logs $name)"
	sleep 2
done

admin_user=itadmin
admin_pass=$(secret)
test_user=ituser
test_pass=$(secret)

say "first-run setup"
api POST /Startup/Configuration '{"ServerName":"arrmada-it","UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"}'
api GET /Startup/User >/dev/null
api POST /Startup/User "{\"Name\":\"$admin_user\",\"Password\":\"$admin_pass\"}"
api POST /Startup/RemoteAccess '{"EnableRemoteAccess":true,"EnableAutomaticPortMapping":false}'
api POST /Startup/Complete

token=$(api POST /Users/AuthenticateByName "{\"Username\":\"$admin_user\",\"Pw\":\"$admin_pass\"}" | field AccessToken)
[ -n "$token" ] || die "admin sign-in returned no token"
auth="MediaBrowser Token=\"$token\", $client"

say "creating the API key"
api POST "/Auth/Keys?app=arrmada-it"
key=$(api GET /Auth/Keys | sed 's/},{/}\n{/g' | grep '"AppName":"arrmada-it"' | field AccessToken)
[ -n "$key" ] || die "API key not found after creating it"

say "creating the test user and switching Quick Connect on"
api POST /Users/New "{\"Name\":\"$test_user\",\"Password\":\"$test_pass\"}" >/dev/null
api GET /System/Configuration | sed 's/"QuickConnectAvailable":false/"QuickConnectAvailable":true/' >"$work/config.json"
api POST /System/Configuration "$(cat "$work/config.json")"
api GET /QuickConnect/Enabled | grep -q true || die "Quick Connect is still off"

# Local NFOs only: every internet fetcher off (an empty enabled-list per type), no savers,
# no realtime monitor (so a folder only appears when Arrmada reports it), no image extraction.
libopts() { # libopts <path> <types...>
	p=$1
	shift
	t=''
	for ty in "$@"; do
		t="$t${t:+,}{\"Type\":\"$ty\",\"MetadataFetchers\":[],\"MetadataFetcherOrder\":[],\"ImageFetchers\":[],\"ImageFetcherOrder\":[]}"
	done
	printf '{"LibraryOptions":{"EnableRealtimeMonitor":false,"EnableInternetProviders":false,"SaveLocalMetadata":false,"MetadataSavers":[],"EnableTrickplayImageExtraction":false,"ExtractTrickplayImagesDuringLibraryScan":false,"EnableChapterImageExtraction":false,"ExtractChapterImagesDuringLibraryScan":false,"AutomaticRefreshIntervalDays":0,"TypeOptions":[%s],"PathInfos":[{"Path":"%s"}]}}' "$t" "$p"
}

say "adding the Movies and TV libraries"
api POST "/Library/VirtualFolders?name=Movies&collectionType=movies&refreshLibrary=false" "$(libopts /media/Movies Movie)"
api POST "/Library/VirtualFolders?name=TV&collectionType=tvshows&refreshLibrary=false" "$(libopts /media/TV Series Season Episode)"
api POST /Library/Refresh

say "waiting for the scan"
i=0
while :; do
	i=$((i + 1))
	[ "$i" -lt 120 ] || die "the library scan did not finish"
	sleep 2
	n=$(api GET "/Items?recursive=true&includeItemTypes=Movie,Episode&limit=0" | sed -n 's/.*"TotalRecordCount":\([0-9]*\).*/\1/p')
	# 11 movie folders and the episode: at least 12 items however versions are grouped.
	[ "${n:-0}" -ge 12 ] || continue
	api GET /Library/VirtualFolders | grep -q '"RefreshStatus":"Active"' && continue
	break
done
say "scan done: $n movies + episodes"

ARRMADA_JELLYFIN_IT=1
ARRMADA_JELLYFIN_URL=http://$name:8096
ARRMADA_JELLYFIN_KEY=$key
ARRMADA_JELLYFIN_ADMIN_USER=$admin_user
ARRMADA_JELLYFIN_ADMIN_PASSWORD=$admin_pass
ARRMADA_JELLYFIN_USER=$test_user
ARRMADA_JELLYFIN_PASSWORD=$test_pass
ARRMADA_JELLYFIN_MEDIA=/media
export ARRMADA_JELLYFIN_IT ARRMADA_JELLYFIN_URL ARRMADA_JELLYFIN_KEY ARRMADA_JELLYFIN_ADMIN_USER \
	ARRMADA_JELLYFIN_ADMIN_PASSWORD ARRMADA_JELLYFIN_USER ARRMADA_JELLYFIN_PASSWORD ARRMADA_JELLYFIN_MEDIA

if [ "${JELLYFIN_IT_HOLD:-}" = 1 ]; then
	umask 077
	# docker --env-file format; the network and volume let another container join the rig.
	cat >"$work/env" <<ENV
ARRMADA_JELLYFIN_IT=1
ARRMADA_JELLYFIN_URL=$ARRMADA_JELLYFIN_URL
ARRMADA_JELLYFIN_KEY=$key
ARRMADA_JELLYFIN_ADMIN_USER=$admin_user
ARRMADA_JELLYFIN_ADMIN_PASSWORD=$admin_pass
ARRMADA_JELLYFIN_USER=$test_user
ARRMADA_JELLYFIN_PASSWORD=$test_pass
ARRMADA_JELLYFIN_MEDIA=/media
JELLYFIN_HOST_URL=$J
JELLYFIN_IT_NETWORK=$net
JELLYFIN_IT_VOLUME=$vol
ENV
	: >"$work/hold"
	say "holding: Jellyfin at $J (network $net, media volume $vol)"
	say "connection details: $work/env — delete $work/hold to tear down"
	while [ -e "$work/hold" ]; do sleep 3; done
	exit 0
fi

say "running the live tests"
# The media volume is mounted twice: at /media, where Jellyfin sees it, and at /arr, which
# plays Arrmada's own spelling of the same folders so path mappings are tested for real.
# The packages run one after another (-p 1): each reports folders to the same server, and
# Jellyfin's scan wait restarts with every report.
# shellcheck disable=SC2086 # the package list is meant to split
docker run --rm --name "$name-go" --cpus=3 --network "$net" \
	-v "$src:/src" -v arrmada-gomod:/go/pkg/mod -v arrmada-gocache:/root/.cache/go-build \
	-v "$vol:/media" -v "$vol:/arr" -w /src \
	-e ARRMADA_JELLYFIN_IT -e ARRMADA_JELLYFIN_URL -e ARRMADA_JELLYFIN_KEY \
	-e ARRMADA_JELLYFIN_ADMIN_USER -e ARRMADA_JELLYFIN_ADMIN_PASSWORD \
	-e ARRMADA_JELLYFIN_USER -e ARRMADA_JELLYFIN_PASSWORD -e ARRMADA_JELLYFIN_MEDIA \
	-e ARRMADA_JELLYFIN_ARR=/arr \
	"$GO_IMAGE" go test -count=1 -p 1 -timeout 40m -v -run IT "$@" ${JELLYFIN_IT_PKGS:-./internal/jellyfin/... ./internal/jellyfinsvc/... ./internal/insights/...}
