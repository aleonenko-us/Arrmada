#!/bin/sh
# Prints the IPv4 subnets of the Docker networks this container is on, comma-separated
# ("172.22.0.0/16"): the routes the kernel made for its own interfaces. Run in a container
# on the Compose project's network (arrmada-qbittorrent-init is), it names the network
# Arrmada and qBittorrent share, and nothing beyond it.
ip -4 route show 2>/dev/null | awk '/ scope link / && $1 ~ /\// { printf "%s%s", sep, $1; sep = ", " }'
