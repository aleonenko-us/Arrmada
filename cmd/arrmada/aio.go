package main

import (
	"context"
	"fmt"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
)

// ensureBundledQbit registers the bundled qBittorrent at boot. The Compose bundle's needs
// no login (its private-network whitelist lets Arrmada in); the Unraid all-in-one image's
// does, and the login it made on first start is in cfg.QbittorrentLogin.
func ensureBundledQbit(ctx context.Context, downloads *download.Service, cfg config.Config) error {
	if cfg.QbittorrentLogin == "" {
		return downloads.EnsureBundled(ctx, cfg.QbittorrentURL)
	}
	login, err := aio.ReadLogin(cfg.QbittorrentLogin)
	if err != nil {
		return fmt.Errorf("the built-in qBittorrent's login: %w", err)
	}
	return downloads.EnsureBundledLogin(ctx, cfg.QbittorrentURL, login.Username, login.Password)
}
