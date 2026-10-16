package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
)

// ensureBundledQbit registers the bundled qBittorrent at boot, with the login Arrmada made
// for it when there is one (cfg.QbittorrentLogin). The Unraid all-in-one image made its
// login on first start, so it must be there. The Compose bundle's has one once
// secureBundledQbit has given that qBittorrent a password; before then its whitelist lets
// Arrmada in without one.
func ensureBundledQbit(ctx context.Context, downloads *download.Service, cfg config.Config) error {
	login, err := aio.ReadLogin(cfg.QbittorrentLogin)
	switch {
	case err == nil:
		return downloads.EnsureBundledLogin(ctx, cfg.QbittorrentURL, login.Username, login.Password)
	case cfg.AIOQbittorrentURL != "":
		return fmt.Errorf("the built-in qBittorrent's login: %w", err)
	case errors.Is(err, fs.ErrNotExist):
		return downloads.EnsureBundled(ctx, cfg.QbittorrentURL)
	}
	// Unreadable: register it all the same (a login typed on the card still works), and say why.
	if rerr := downloads.EnsureBundled(ctx, cfg.QbittorrentURL); rerr != nil {
		return rerr
	}
	return fmt.Errorf("the bundled qBittorrent's saved login: %w", err)
}

// composeBundleHost is the bundled qBittorrent's name on the Compose install's network.
const composeBundleHost = "arrmada-qbittorrent"

// isComposeBundleQbit says url is the Compose install's own qBittorrent container, the one
// whose first config let the private Docker networks in without a password. Anything else
// (the all-in-one image's, or a qBittorrent of the owner's someone pointed
// ARRMADA_QBITTORRENT_URL at) is never secured by Arrmada.
func isComposeBundleQbit(cfg config.Config) bool {
	if cfg.QbittorrentURL == "" || cfg.AIOQbittorrentURL != "" {
		return false
	}
	u, err := url.Parse(cfg.QbittorrentURL)
	return err == nil && u.Hostname() == composeBundleHost
}

// loginFile keeps the Compose bundle's qBittorrent login in the data folder (see
// download.BundledLoginKeeper).
type loginFile struct{ path string }

func (f loginFile) Load() (string, string, bool, error) {
	l, err := aio.ReadLogin(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return l.Username, l.Password, true, nil
}

func (f loginFile) Make(username string) (string, error) {
	l, err := aio.NewLogin(username)
	if err != nil {
		return "", err
	}
	if err := aio.SaveLogin(f.path, l); err != nil {
		return "", err
	}
	return l.Password, nil
}

// secureBundledQbit gives the Compose bundle's qBittorrent a password and switches off the
// login-free access its first config had (download.Service.SecureBundledQbit), retrying
// while it starts. Whatever happens, qBittorrent stays reachable for Arrmada; a failure is
// logged and tried again at the next start.
func secureBundledQbit(ctx context.Context, log *slog.Logger, downloads *download.Service, cfg config.Config) {
	var (
		out  download.SecureOutcome
		last error
	)
	err := retryBoot(ctx, func(ctx context.Context) error {
		out, last = downloads.SecureBundledQbit(ctx, cfg.QbittorrentURL, loginFile{cfg.QbittorrentLogin})
		if last != nil && errors.Is(last, download.ErrQbitUnreachable) {
			return last // still starting: try again shortly
		}
		return nil
	})
	switch {
	case err != nil && ctx.Err() != nil:
		return // shutting down
	case err != nil:
		log.Warn("could not reach the bundled qBittorrent to give it a password; it still lets Arrmada in as before, and Arrmada tries again at its next start", "err", last)
	case errors.Is(last, download.ErrQbitLoginRefused):
		log.Warn("the bundled qBittorrent asks for a password Arrmada doesn't have: enter it on its card in Settings → Connections (see docs/troubleshooting.md)", "err", last)
	case last != nil:
		log.Warn("could not give the bundled qBittorrent a password; it still lets Arrmada in as before, and Arrmada tries again at its next start", "err", last)
	case out == download.SecureNewPassword:
		log.Info("the bundled qBittorrent now asks for a password, which Arrmada made and signs in with; an admin can see it on its card in Settings → Connections")
	case out == download.SecureKeptPassword:
		log.Info("the bundled qBittorrent now asks for the password you set in it; Arrmada signs in with the one on its card")
	}
}
