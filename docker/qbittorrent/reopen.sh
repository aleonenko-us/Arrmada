#!/bin/sh
# Switches the bundled qBittorrent's login-free access back on, for the Docker network it
# shares with Arrmada and nothing else (never a home network), so Arrmada can sign in and
# give it a password again (the saved one, or a new one) at its next start. Used by
# update.sh --rollback --with-db and docs/troubleshooting.md. Run it with qBittorrent
# stopped, in its init container (which mounts its config and is on the same network):
#   docker compose stop arrmada-qbittorrent
#   docker compose run --rm --no-deps arrmada-qbittorrent-init sh /seed/reopen.sh
#   docker compose start arrmada-qbittorrent
set -e
f=/config/qBittorrent/qBittorrent.conf
[ -f "$f" ] || { echo "No qBittorrent config at $f." >&2; exit 1; }
nets=$(sh /seed/subnets.sh)
[ -n "$nets" ] || { echo "Couldn't tell which Docker network this is, so nothing was changed." >&2; exit 1; }
o=$(stat -c %u:%g "$f")
# Both lines replaced in [Preferences] (added when missing); everything else kept as it is.
awk -v nets="$nets" '
  function put() { print "WebUI\\AuthSubnetWhitelistEnabled=true"; print "WebUI\\AuthSubnetWhitelist=" nets; done = 1 }
  /^\[/ { if (inpref && !done) put(); inpref = ($0 == "[Preferences]"); print; next }
  inpref && /^WebUI\\AuthSubnetWhitelist(Enabled)?=/ { next }
  { print }
  END { if (!done) { if (!inpref) print "[Preferences]"; put() } }
' "$f" > "$f.new"
chown "$o" "$f.new"
mv "$f.new" "$f"
grep -q '^WebUI\\AuthSubnetWhitelistEnabled=true' "$f"
