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

// The optional bundled Prowlarr and SABnzbd listen on every address in the container: their
// own pages are where the owner adds indexers and a Usenet provider, so the template
// publishes them (on host ports of the owner's choosing). Arrmada reaches both on the
// loopback address. SABnzbd's port isn't its usual 8080, which qBittorrent's web UI has.
const (
	ProwlarrPort = 9696
	ProwlarrURL  = "http://127.0.0.1:9696"
	SABnzbdPort  = 8085
	SABnzbdURL   = "http://127.0.0.1:8085"
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
	Prowlarr     bool   // run the bundled Prowlarr (opt-in: off unless switched on)
	SABnzbd      bool   // run the bundled SABnzbd (opt-in: off unless switched on)
	TorrentPort  int
}

// QbittorrentProfile is qBittorrent's --profile folder: in appdata, so its torrents and
// settings survive an update, and are kept when it's switched off.
func (o Options) QbittorrentProfile() string { return filepath.Join(o.DataDir, "qbittorrent") }

// ProwlarrDir is the bundled Prowlarr's data folder (its config.xml, database, logs and
// backups), in appdata beside qBittorrent's, and kept when it's switched off.
func (o Options) ProwlarrDir() string { return filepath.Join(o.DataDir, "prowlarr") }

// SABnzbdDir is the bundled SABnzbd's folder in appdata: its sabnzbd.ini, its queue
// (admin/) and its logs, kept when it's switched off. Its downloads aren't here: they go
// under the downloads folder, on the share the library is on, so imports are a rename.
func (o Options) SABnzbdDir() string { return filepath.Join(o.DataDir, "sabnzbd") }

// SABnzbdConfig is the bundled SABnzbd's sabnzbd.ini, which Arrmada reads its API key and
// page login from (ARRMADA_SABNZBD_CONFIG).
func (o Options) SABnzbdConfig() string { return filepath.Join(o.SABnzbdDir(), "sabnzbd.ini") }

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
		// 32 bits: an id ends up as a uint32 for the child processes, and a bigger
		// number would wrap ("4294967296" is uid 0, root).
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 {
			notes = append(notes, fmt.Sprintf("%s=%q isn't a number; using %d", key, v, def))
			return def
		}
		return int(n)
	}
	on := func(key string) bool {
		v, ok := parseSwitch(getenv(key))
		if !ok {
			notes = append(notes, fmt.Sprintf("%s=%q isn't true or false; leaving it on", key, getenv(key)))
		}
		return v
	}
	// The opt-in companions: only a clear "on" starts one. Blank (a cleared template
	// field) and anything unreadable leave it off, the way it ships.
	optIn := func(key string) bool {
		v, ok := parseOptIn(getenv(key))
		if !ok {
			notes = append(notes, fmt.Sprintf("%s=%q isn't true or false; leaving it off", key, getenv(key)))
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
		Prowlarr:     optIn("ARRMADA_BUNDLE_PROWLARR"),
		SABnzbd:      optIn("ARRMADA_BUNDLE_SABNZBD"),
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
	notes = append(notes, fmt.Sprintf("bundled qBittorrent %s, bundled FlareSolverr %s, bundled Prowlarr %s, bundled SABnzbd %s",
		onOff(o.Qbittorrent), onOff(o.FlareSolverr), onOff(o.Prowlarr), onOff(o.SABnzbd)))
	return o, notes
}

// AppEnv is what Arrmada's own environment gains, on top of the container's. A switched
// on companion is registered and defaulted exactly as the Compose bundle's are
// (ARRMADA_QBITTORRENT_URL, ARRMADA_QBIT_PORT, ARRMADA_FLARESOLVERR_URL,
// ARRMADA_PROWLARR_URL, ARRMADA_SABNZBD_URL); a switched-off one is simply absent, as in
// the plain image.
//
// The download clients: ARRMADA_AIO_QBITTORRENT_URL and ARRMADA_AIO_SABNZBD_URL are always
// set, so Arrmada can tell its bundled client was switched off and stop sending to it, and
// a URL left in the template never registers a client that isn't running.
//
// The other two are defaults the owner can override: a FlareSolverr or Prowlarr address
// set in the template wins over the bundled one, whether that's on or off.
// ARRMADA_AIO_PROWLARR_URL says the bundled Prowlarr is running there, so Arrmada treats it
// as its own (and points it at its FlareSolverr), as it does the Compose install's.
func AppEnv(o Options, getenv func(string) string) []string {
	env := []string{"ARRMADA_AIO_QBITTORRENT_URL=" + LocalURL, "ARRMADA_AIO_SABNZBD_URL=" + SABnzbdURL}
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
	if o.SABnzbd {
		env = append(env, "ARRMADA_SABNZBD_URL="+SABnzbdURL, "ARRMADA_SABNZBD_CONFIG="+o.SABnzbdConfig())
	} else {
		env = append(env, "ARRMADA_SABNZBD_URL=") // nothing registers or reads it while it's off
	}
	if o.FlareSolverr && strings.TrimSpace(getenv("ARRMADA_FLARESOLVERR_URL")) == "" {
		env = append(env, "ARRMADA_FLARESOLVERR_URL="+FlareSolverrURL)
	}
	if o.Prowlarr {
		env = append(env, "ARRMADA_AIO_PROWLARR_URL="+ProwlarrURL)
		if strings.TrimSpace(getenv("ARRMADA_PROWLARR_URL")) == "" {
			env = append(env, "ARRMADA_PROWLARR_URL="+ProwlarrURL)
		}
	} else {
		// One left in the environment must not say a Prowlarr that isn't running is ours.
		env = append(env, "ARRMADA_AIO_PROWLARR_URL=")
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

// parseOptIn reads the switch of a companion that ships switched off: blank is off, so
// clearing its template field never starts it. ok is false for anything it can't read,
// which also counts as off.
func parseOptIn(v string) (on, ok bool) {
	if strings.TrimSpace(v) == "" {
		return false, true
	}
	on, ok = parseSwitch(v)
	return on && ok, ok
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
