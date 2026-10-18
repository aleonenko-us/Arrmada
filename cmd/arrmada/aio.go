package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
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

// composeBundleHost is the bundled qBittorrent's name on the Compose install's network: its
// service name, or its container name (Arrmada-qbittorrent), which differs only in case.
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
	return err == nil && strings.EqualFold(u.Hostname(), composeBundleHost)
}

// noteUnsecuredBundle says why a bundled qBittorrent outside the all-in-one image isn't
// given a password: its address isn't the Compose install's own container.
func noteUnsecuredBundle(ctx context.Context, log *slog.Logger, downloads *download.Service, cfg config.Config) {
	if cfg.QbittorrentURL == "" || cfg.AIOQbittorrentURL != "" {
		return
	}
	if _, ok, err := downloads.BundledClient(ctx, download.KindQbittorrent); err == nil && ok {
		log.Info("the bundled qBittorrent's address isn't the Compose install's own container, so Arrmada leaves its login settings alone", "url", cfg.QbittorrentURL)
	}
}

// secureLockStale is how old a lock file must be before it's taken for one a crashed
// process left: well past the longest a run can take (a minute of retries, then a few
// calls of at most 30 seconds each).
const secureLockStale = 15 * time.Minute

// lockFile takes an exclusive lock by creating path (O_EXCL), so two Arrmada processes on
// one data folder never run the password switch at the same time. ok is false while
// another holds it; a lock older than stale was left by a crash and is taken over.
func lockFile(path string, stale time.Duration) (release func(), ok bool, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			mine := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano())
			_, werr := f.WriteString(mine)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return nil, false, werr
			}
			// Only ever removes its own: one taken over as stale is someone else's now.
			return func() {
				if b, err := os.ReadFile(path); err == nil && string(b) == mine {
					os.Remove(path)
				}
			}, true, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, false, err
		}
		fi, serr := os.Stat(path)
		if serr != nil {
			if errors.Is(serr, fs.ErrNotExist) {
				continue // released meanwhile
			}
			return nil, false, serr
		}
		if time.Since(fi.ModTime()) < stale {
			return nil, false, nil
		}
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return nil, false, rerr
		}
	}
	return nil, false, nil
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

// reopenedMarker is the file update.sh (--rollback --with-db) and the troubleshooting steps
// leave beside the login when they switch qBittorrent's Docker-network access back on.
func (f loginFile) reopenedMarker() string {
	return filepath.Join(filepath.Dir(f.path), "qbittorrent-reopened")
}

// Reopened is what the marker says: there and empty after update.sh's rollback, "reset"
// when the owner asked Arrmada to give qBittorrent its password again.
func (f loginFile) Reopened() download.Reopen {
	b, err := os.ReadFile(f.reopenedMarker())
	switch {
	case err != nil:
		return download.ReopenNone
	case strings.TrimSpace(string(b)) == "reset":
		return download.ReopenReset
	}
	return download.ReopenRollback
}

func (f loginFile) ClearReopened() error {
	if err := os.Remove(f.reopenedMarker()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
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
	release, ok, err := lockFile(filepath.Join(cfg.DataDir, "qbittorrent-login.lock"), secureLockStale)
	switch {
	case err != nil:
		log.Warn("could not give the bundled qBittorrent a password: its lock file", "err", err)
		return
	case !ok:
		log.Info("another Arrmada process is giving the bundled qBittorrent its password; leaving it to that one")
		return
	}
	defer release()
	var (
		out  download.SecureOutcome
		last error
	)
	err = retryBoot(ctx, func(ctx context.Context) error {
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
	case errors.Is(last, download.ErrQbitKeptLoginStale):
		log.Warn("the bundled qBittorrent lets the Docker network in without a password again, but its password isn't the one Arrmada kept, so Arrmada left it as it is: enter its current login on its card in Settings → Connections, or switch the whitelist off in qBittorrent (Tools → Options → WebUI)", "err", last)
	case last != nil:
		log.Warn("could not give the bundled qBittorrent a password; it still lets Arrmada in as before, and Arrmada tries again at its next start", "err", last)
	case out == download.SecureNewPassword && !hasBundledCard(ctx, downloads, cfg.QbittorrentURL):
		// No card to show it on: say where it is.
		log.Info("the bundled qBittorrent now asks for a password, which Arrmada made; it has no card in Settings → Connections, so read it with: docker exec Arrmada-app cat /data/qbittorrent-login.json (Restore bundled qBittorrent brings the card back with it)")
	case out == download.SecureNewPassword:
		log.Info("the bundled qBittorrent now asks for a password, which Arrmada made and signs in with; an admin can see it on its card in Settings → Connections")
	case out == download.SecureKeptPassword:
		log.Info("the bundled qBittorrent now asks for the password you set in it; Arrmada signs in with the one on its card")
	}
	// Whatever happened, say so loudly when it still lets whole networks in without a
	// password (Settings → Status says it too, until it's fixed: bundledQbitCheck).
	if exp, err := downloads.BundledQbitExposure(ctx, cfg.QbittorrentURL); err == nil && len(exp.Broad) > 0 {
		log.Warn("the bundled qBittorrent still lets anything on these networks in without a password (its subnet whitelist)", "subnets", strings.Join(exp.Broad, ", "), "first_install_whitelist", exp.OldSeed)
	}
}

// bundledQbitCheck is the health line for a Compose bundle qBittorrent whose subnet
// whitelist still lets whole networks in without a password, asked without signing in
// (download.Service.BundledQbitExposure), so it clears by itself once that's fixed.
func bundledQbitCheck(downloads *download.Service, cfg config.Config) health.Check {
	return health.BundledQbitCheck(func(ctx context.Context) (health.QbitExposure, error) {
		exp, err := downloads.BundledQbitExposure(ctx, cfg.QbittorrentURL)
		return health.QbitExposure{Broad: exp.Broad, OldSeed: exp.OldSeed, Held: exp.Held}, err
	})
}

// hasBundledCard says the bundled qBittorrent's card is there and points at url.
func hasBundledCard(ctx context.Context, downloads *download.Service, url string) bool {
	c, ok, err := downloads.BundledClient(ctx, download.KindQbittorrent)
	return err == nil && ok && c.URL == url
}
