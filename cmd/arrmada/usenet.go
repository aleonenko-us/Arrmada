package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// wireUsenet lets the indexers search usenet (Newznab) indexers only while a usenet
// download client exists. HasTransport is cached until a client changes, so asking on
// every search costs nothing, and adding or removing SABnzbd applies to the next search.
func wireUsenet(indexers *indexer.Service, downloads *download.Service) {
	indexers.SetUsenetAvailable(func() bool {
		return downloads.HasTransport(context.Background(), download.TransportUsenet)
	})
}

// bundledSAB keeps the optional bundled SABnzbd (the "sabnzbd" compose profile) registered
// as a download client. Its config, with the API key the init container generated, is
// mounted read-only into the app; the file appearing is what says the profile is on. It's
// watched rather than read once: on a first start the init container may still be writing
// it, and `install.sh --with-sabnzbd` on a running install starts SABnzbd without
// restarting the app. A changed key (regenerated in SABnzbd) is taken up the same way.
type bundledSAB struct {
	url, config string
	downloads   *download.Service
	// downloadsDir is Arrmada's downloads folder, read live.
	downloadsDir func(ctx context.Context) string
	// added runs when a new bundled row was made (the Prowlarr sync, which brings usenet
	// indexers in only while a usenet client exists).
	added    func()
	log      *slog.Logger
	interval time.Duration
	// tuneTries bounds how long a SABnzbd that never answers is retried after it appears.
	tuneTries int

	// What step has seen so far (run's goroutine only).
	lastKey, warned string
	tuned           bool
	tries           int
}

func (b *bundledSAB) run(ctx context.Context) {
	for {
		b.step(ctx)
		// Just registered and SABnzbd still starting: set it up as soon as it answers, so
		// its card isn't left showing folders it hasn't made yet.
		wait := b.interval
		if b.lastKey != "" && !b.tuned && b.tries < b.tuneTries {
			wait = min(wait, 5*time.Second)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// step looks at the config once: registers the client or takes up a changed key, then
// sets SABnzbd up until that has worked (or stopped being worth retrying).
func (b *bundledSAB) step(ctx context.Context) {
	key, err := download.ReadSABAPIKey(b.config)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// The bundled SABnzbd isn't switched on: nothing to do.
	case err != nil:
		if b.warned != err.Error() {
			b.warned = err.Error()
			b.log.Warn("bundled SABnzbd: couldn't read its API key from its config", "file", b.config, "err", err)
		}
	case key != b.lastKey:
		added, err := b.downloads.EnsureBundledSAB(ctx, b.url, key)
		if err != nil {
			b.log.Warn("bundled SABnzbd: couldn't register it", "err", err)
			break
		}
		b.lastKey, b.tuned, b.tries = key, false, 0
		if added && b.added != nil {
			b.added()
		}
	}
	if b.lastKey != "" && !b.tuned && b.tries < b.tuneTries {
		b.tries++
		b.tuned = b.tune(ctx, b.tries == b.tuneTries)
	}
}

// tune applies TuneBundledSAB once SABnzbd answers. It reports whether it's done: applied,
// nothing to apply (removed or switched off), or a problem retrying won't fix.
func (b *bundledSAB) tune(ctx context.Context, last bool) bool {
	got, err := b.downloads.TuneBundledSAB(ctx, b.downloadsDir(ctx))
	switch {
	case errors.Is(err, download.ErrBundledInactive):
		return true
	case errors.Is(err, download.ErrSABCantSee):
		b.log.Warn("bundled SABnzbd: not moving its folders", "err", err)
		return true
	case err != nil:
		if last {
			b.log.Warn("bundled SABnzbd: couldn't set it up; it's tried again at the next start", "err", err)
		}
		return false
	}
	b.logTuned(got, "Downloads folder")
	// Its own page, published on the network, had no login in its first config. Best
	// effort: the next start tries again, and Arrmada itself never needs one (the API key).
	if given, err := b.downloads.SecureBundledSAB(ctx, b.url); err != nil && !errors.Is(err, download.ErrBundledInactive) {
		b.log.Warn("bundled SABnzbd: couldn't give its own page a login; it's tried again at the next start", "err", err)
	} else if given {
		b.log.Info("bundled SABnzbd: its own page now asks for a login, which Arrmada made; an admin sees it on its card in Settings → Connections")
	}
	return true
}

func (b *bundledSAB) logTuned(got download.SABTuned, folder string) {
	if len(got.Categories) > 0 {
		b.log.Info("bundled SABnzbd: added Arrmada's categories", "categories", strings.Join(got.Categories, ", "))
	}
	if got.Folders != "" {
		b.log.Info("bundled SABnzbd: downloads now go to the "+folder, "folder", got.Folders)
	}
}

// foldersChanged follows a new Downloads folder, the way the bundled qBittorrent's save
// path does, with the same retries.
func (b *bundledSAB) foldersChanged(ctx context.Context) {
	var got download.SABTuned
	var last error
	err := retryBoot(ctx, func(ctx context.Context) error {
		got, last = b.downloads.TuneBundledSAB(ctx, b.downloadsDir(ctx))
		if errors.Is(last, download.ErrBundledInactive) || errors.Is(last, download.ErrSABCantSee) {
			return nil
		}
		return last
	})
	switch {
	case errors.Is(last, download.ErrSABCantSee):
		b.log.Warn("bundled SABnzbd: not moving its folders to the new Downloads folder", "err", last)
	case errors.Is(err, errRetriesExhausted):
		b.log.Warn("bundled SABnzbd: couldn't move its folders to the new Downloads folder", "err", last)
	case err == nil && last == nil:
		b.logTuned(got, "new Downloads folder")
	}
}
