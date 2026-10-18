package health

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BundledQbitCheck warns while the Compose bundle's qBittorrent lets whole networks in
// without a password: its subnet whitelist names a /16 or more beyond the Docker network
// Arrmada is on. The first Compose config's (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
// covers every home network, so any device on the LAN (or a page in someone's browser
// there) could add torrents or change qBittorrent's settings. Arrmada replaces that one
// with a password by itself at start-up, so it showing here means that didn't work; a
// broad whitelist set in qBittorrent is the owner's to narrow. exposure asks qBittorrent
// without signing in (see download.Service.BundledQbitExposure): broad is empty when all
// is well, oldSeed says it's the first config's. An unreachable qBittorrent is the
// download clients check's to report, so it isn't repeated here.
func BundledQbitCheck(exposure func(ctx context.Context) (broad []string, oldSeed bool, err error)) Check {
	return Check{
		Key: "qbittorrent-whitelist", Name: "qBittorrent's login", Category: CategoryDownloads,
		Interval: 10 * time.Minute, Timeout: 20 * time.Second,
		Run: func(ctx context.Context) []Finding {
			broad, oldSeed, err := exposure(ctx)
			if err != nil || len(broad) == 0 {
				return nil
			}
			msg := fmt.Sprintf("The bundled qBittorrent lets anything on %s in without a password (its subnet whitelist), "+
				"so any device on your network can add torrents or change its settings.", strings.Join(broad, ", "))
			if oldSeed {
				msg += " That's the whitelist it was first installed with: Arrmada replaces it with a password at its start, " +
					"and the log says why it couldn't (restart Arrmada to try again)."
			} else {
				msg += " Narrow it to the devices that need it in qBittorrent (Tools → Options → WebUI), or switch it off."
			}
			return []Finding{{Key: "qbittorrent.whitelist", Level: LevelWarning, Message: msg, Fix: FixDownloadClients}}
		},
	}
}
