//go:build linux

// Command arrmada-aio starts the Unraid all-in-one image: Arrmada, plus the bundled
// qBittorrent and FlareSolverr unless they're switched off, and the optional Prowlarr and
// SABnzbd when they're switched on, in one container. It runs as root behind tini (PID 1,
// which reaps orphaned processes and passes the stop signal on), sets up qBittorrent's and
// SABnzbd's first configs, and supervises them all (internal/aio).
//
// The plain image doesn't contain it: there the entrypoint runs Arrmada directly, and
// the Compose install runs each companion in a container of its own.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tristenlammi/arrmada/internal/aio"
)

func main() {
	// `docker run <image> <command>` (arrmada version, backup, ...) runs that command, as
	// the plain image does, without starting any companion.
	if len(os.Args) > 1 {
		args := append([]string{aio.Entrypoint}, os.Args[1:]...)
		if err := syscall.Exec(aio.Entrypoint, args, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "[aio] couldn't run %s: %v\n", aio.Entrypoint, err)
			os.Exit(127)
		}
	}

	_, statErr := os.Stat(aio.FlareSolverrDir + "/flaresolverr.py")
	o, notes := aio.ReadOptions(os.Getenv, statErr == nil)
	for _, n := range notes {
		fmt.Printf("[aio] %s\n", n)
	}
	if err := prepare(o); err != nil {
		// Arrmada still starts (it says the client can't be reached); the log says why.
		fmt.Printf("[aio] couldn't set up the bundled services: %v\n", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	sup := &aio.Supervisor{Services: aio.Plan(o, os.Environ(), os.Getenv), Out: os.Stdout}
	code := sup.Run(ctx)
	stop()
	os.Exit(code)
}

// prepare makes the folders the companions write to, owned by the user they run as. The
// qBittorrent profile is never removed, so switching it off keeps its torrents for when
// it's switched back on.
func prepare(o aio.Options) error {
	if o.FlareSolverr {
		// FlareSolverr's user is neither the owner nor in the group of appdata: closing
		// "others" out of /data keeps the database and qBittorrent's login away from it.
		// First, so the folder is already PUID's when qBittorrent starts in it.
		if err := aio.CloseToOthers(o.DataDir, o.UID, o.GID); err != nil {
			return fmt.Errorf("closing %s to FlareSolverr: %w", o.DataDir, err)
		}
	}
	if o.Qbittorrent {
		profile := o.QbittorrentProfile()
		if err := os.MkdirAll(profile, 0o750); err != nil {
			return err
		}
		seeded, err := aio.SeedQbittorrent(aio.SeedOptions{Profile: profile, Downloads: o.Downloads, TorrentPort: o.TorrentPort, UID: o.UID, GID: o.GID})
		if err != nil {
			return fmt.Errorf("qBittorrent config: %w", err)
		}
		if seeded {
			fmt.Printf("[aio] qBittorrent: first start, wrote its config and a web UI login (shown to admins in Settings → Connections)\n")
		}
		// After a PUID/PGID change the old owner's files would be unwritable.
		if err := aio.ChownTree(profile, o.UID, o.GID); err != nil {
			return fmt.Errorf("qBittorrent folder: %w", err)
		}
	}
	if o.Prowlarr {
		// Prowlarr writes its own first config.xml (and its API key) when it starts.
		if err := os.MkdirAll(o.ProwlarrDir(), 0o750); err != nil {
			return err
		}
		if err := aio.ChownTree(o.ProwlarrDir(), o.UID, o.GID); err != nil {
			return fmt.Errorf("the Prowlarr folder: %w", err)
		}
	}
	if o.SABnzbd {
		host, _ := os.Hostname()
		seeded, err := aio.SeedSABnzbd(aio.SABSeedOptions{Config: o.SABnzbdConfig(), Downloads: o.Downloads, Hostname: host, UID: o.UID, GID: o.GID})
		if err != nil {
			return fmt.Errorf("the SABnzbd config: %w", err)
		}
		if seeded {
			fmt.Printf("[aio] SABnzbd: first start, wrote its config with an API key and a login for its page (shown to admins in Settings → Connections)\n")
		}
		if err := aio.ChownTree(o.SABnzbdDir(), o.UID, o.GID); err != nil {
			return fmt.Errorf("the SABnzbd folder: %w", err)
		}
	}
	if o.FlareSolverr {
		if err := os.MkdirAll(aio.FlareSolverrHome, 0o700); err != nil {
			return err
		}
		if err := aio.ChownTree(aio.FlareSolverrHome, aio.FlareSolverrUID, aio.FlareSolverrGID); err != nil {
			return err
		}
	}
	return nil
}
