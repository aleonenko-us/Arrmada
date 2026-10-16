package aio

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// The bundled FlareSolverr listens on the loopback address only: nothing outside the
// container can reach it, so it can't clash with another FlareSolverr on 8191 either.
const (
	FlareSolverrPort = 8191
	FlareSolverrURL  = "http://127.0.0.1:8191"
)

// DefaultTorrentPort is the incoming BitTorrent port when the template doesn't name one.
// Not 6881: an Unraid server that already runs a torrent client most likely has that
// one, and a port that's taken stops the whole container from starting.
const DefaultTorrentPort = 56881

// Options is the all-in-one container's setup, read from its environment.
type Options struct {
	UID, GID     int
	DataDir      string // appdata (/data): the database, and the qBittorrent profile
	Downloads    string // where qBittorrent saves until Arrmada sets its own folder
	Qbittorrent  bool   // run the bundled qBittorrent
	FlareSolverr bool   // run the bundled FlareSolverr
	TorrentPort  int
}

// QbittorrentProfile is qBittorrent's --profile folder: in appdata, so its torrents and
// settings survive an update, and are kept when it's switched off.
func (o Options) QbittorrentProfile() string { return filepath.Join(o.DataDir, "qbittorrent") }

// ReadOptions reads the environment. flareInstalled says whether this image has
// FlareSolverr at all (the ARM build doesn't). notes are lines for the container log:
// what runs, and anything in the environment that was read differently from how it's
// written.
func ReadOptions(getenv func(string) string, flareInstalled bool) (o Options, notes []string) {
	num := func(key string, def int) int {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			notes = append(notes, fmt.Sprintf("%s=%q isn't a number; using %d", key, v, def))
			return def
		}
		return n
	}
	on := func(key string) bool {
		v, ok := parseSwitch(getenv(key))
		if !ok {
			notes = append(notes, fmt.Sprintf("%s=%q isn't true or false; leaving it on", key, getenv(key)))
		}
		return v
	}
	o = Options{
		UID:          num("PUID", 1000),
		GID:          num("PGID", 1000),
		DataDir:      orDefault(getenv("ARRMADA_DATA_DIR"), "/data"),
		Downloads:    orDefault(getenv("ARRMADA_DOWNLOADS_DIR"), "/media/downloads"),
		Qbittorrent:  on("ARRMADA_BUNDLE_QBITTORRENT"),
		FlareSolverr: on("ARRMADA_BUNDLE_FLARESOLVERR"),
		TorrentPort:  num("ARRMADA_QBIT_PORT", DefaultTorrentPort),
	}
	if o.TorrentPort < 1 || o.TorrentPort > 65535 {
		notes = append(notes, fmt.Sprintf("ARRMADA_QBIT_PORT=%d isn't a port; using %d", o.TorrentPort, DefaultTorrentPort))
		o.TorrentPort = DefaultTorrentPort
	}
	if o.FlareSolverr && !flareInstalled {
		notes = append(notes, "FlareSolverr isn't part of the ARM build of this image (its Chromium is x86-64 only), so it stays off. Add your own in Settings → Connections if a tracker needs it.")
		o.FlareSolverr = false
	}
	notes = append(notes, fmt.Sprintf("bundled qBittorrent %s, bundled FlareSolverr %s", onOff(o.Qbittorrent), onOff(o.FlareSolverr)))
	return o, notes
}

// AppEnv is what Arrmada's own environment gains, on top of the container's. A switched
// on companion is registered and defaulted exactly as the Compose bundle's are
// (ARRMADA_QBITTORRENT_URL, ARRMADA_QBIT_PORT, ARRMADA_FLARESOLVERR_URL); a switched-off
// one is simply absent, as in the plain image. ARRMADA_AIO_QBITTORRENT_URL is always
// set, so Arrmada can tell its bundled client was switched off and stop sending to it.
// A FlareSolverr address the owner set in the template wins over the bundled one.
func AppEnv(o Options, getenv func(string) string) []string {
	env := []string{"ARRMADA_AIO_QBITTORRENT_URL=" + LocalURL}
	if o.Qbittorrent {
		env = append(env,
			"ARRMADA_QBITTORRENT_URL="+LocalURL,
			"ARRMADA_QBIT_PORT="+strconv.Itoa(o.TorrentPort),
			"ARRMADA_QBITTORRENT_LOGIN="+LoginPath(o.QbittorrentProfile()),
		)
	} else {
		// A value left in the template must not register a client that isn't running.
		env = append(env, "ARRMADA_QBITTORRENT_URL=", "ARRMADA_QBITTORRENT_LOGIN=")
	}
	if o.FlareSolverr && strings.TrimSpace(getenv("ARRMADA_FLARESOLVERR_URL")) == "" {
		env = append(env, "ARRMADA_FLARESOLVERR_URL="+FlareSolverrURL)
	}
	return env
}

// parseSwitch reads an on/off variable; blank is on (the all-in-one default). ok is
// false for anything it can't read, which also counts as on: switching a companion off
// is the choice that needs saying clearly.
func parseSwitch(v string) (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return true, false
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// MergeEnv returns base with every KEY=value in extra applied: an existing key is
// replaced in place, a new one appended.
func MergeEnv(base, extra []string) []string {
	out := append([]string(nil), base...)
	idx := map[string]int{}
	for i, kv := range out {
		k, _, _ := strings.Cut(kv, "=")
		idx[k] = i
	}
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}
