#!/bin/sh
# One-shot (arrmada-qbittorrent-init): qBittorrent's first config, only when it has none.
# The seed (qBittorrent.conf: downloads path, CSRF protection, the localhost login on),
# plus login-free access from the Docker network Arrmada and qBittorrent share, and only
# that network, so Arrmada can sign in to give qBittorrent a password of its own when it
# first starts. Arrmada then switches that access off. An existing config is left alone.
set -e
d=/config/qBittorrent
f="$d/qBittorrent.conf"
mkdir -p "$d"
[ -f "$f" ] && exit 0
nets=$(sh /seed/subnets.sh)
cp /seed/qBittorrent.conf "$f.new"
if [ -n "$nets" ]; then
  # The seed ends with its [Preferences] section, so these land in it.
  printf 'WebUI\\AuthSubnetWhitelistEnabled=true\nWebUI\\AuthSubnetWhitelist=%s\n' "$nets" >> "$f.new"
else
  echo "qBittorrent's first config: couldn't tell which Docker network this is, so it has no login-free access for Arrmada; enter its login on its card in Settings → Connections (docs/troubleshooting.md#qbittorrent-login)." >&2
fi
mv "$f.new" "$f"
