package aio

import "strconv"

// Service is one process the supervisor runs.
type Service struct {
	Name string
	Path string
	Args []string
	Dir  string
	Env  []string // the whole environment (nil: the supervisor's own)
	// UID/GID to run as; nil runs as the supervisor's user (root), for a service that
	// drops privileges itself (Arrmada, through the image's entrypoint).
	UID, GID *int
	// Main is Arrmada. Its output goes to the container log untouched, exactly as in the
	// plain image, and when it exits the container exits with its code: Docker's restart
	// policy then brings everything back, as it would the plain image. The others are
	// restarted on their own when they stop.
	Main bool
}

// Where the image puts things (the aio stages of the Dockerfile).
const (
	Entrypoint     = "/entrypoint.sh" // the plain image's: fixes ownership, drops to PUID:PGID, runs arrmada
	QbittorrentBin = "/usr/local/bin/qbittorrent-nox"
	// FlareSolverr sits where its own image keeps it (/app): its code looks for the
	// chromedriver at /app/chromedriver and its Chromium at /app/chrome/chrome.
	FlareSolverrDir    = "/app"
	FlareSolverrPython = "/opt/flaresolverr/venv/bin/python"
	FlareSolverrHome   = "/tmp/flaresolverr" // Chromium's profile and caches; nothing to keep
)

// Plan is the services to run: Arrmada always, and each companion that's switched on.
// environ is the container's environment. Arrmada gets all of it plus AppEnv; the
// companions get only what they need, so no key or token given to Arrmada reaches them.
func Plan(o Options, environ []string, getenv func(string) string) []Service {
	uid, gid := o.UID, o.GID
	base := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	for _, k := range []string{"TZ", "LANG"} {
		if v := getenv(k); v != "" {
			base = append(base, k+"="+v)
		}
	}
	if getenv("LANG") == "" {
		base = append(base, "LANG=C.UTF-8") // Qt and Python both want a UTF-8 locale
	}
	svcs := []Service{{
		Name: "arrmada",
		Path: Entrypoint,
		Env:  MergeEnv(environ, AppEnv(o, getenv)),
		Main: true,
	}}
	if o.Qbittorrent {
		profile := o.QbittorrentProfile()
		svcs = append(svcs, Service{
			Name: "qbittorrent",
			Path: QbittorrentBin,
			Args: []string{"--profile=" + profile, "--webui-port=" + strconv.Itoa(WebUIPort), "--confirm-legal-notice"},
			Dir:  profile,
			Env:  append(append([]string(nil), base...), "HOME="+profile),
			UID:  &uid, GID: &gid,
		})
	}
	if o.FlareSolverr {
		svcs = append(svcs, Service{
			Name: "flaresolverr",
			Path: FlareSolverrPython,
			Args: []string{"-u", FlareSolverrDir + "/flaresolverr.py"},
			Dir:  FlareSolverrDir,
			Env: append(append([]string(nil), base...),
				"HOME="+FlareSolverrHome,
				"HOST=127.0.0.1",
				"PORT="+strconv.Itoa(FlareSolverrPort),
				"LOG_LEVEL=info",
				// Headless here means a hidden display FlareSolverr starts itself (Xvfb):
				// Chromium's own headless mode is what Cloudflare spots.
				"HEADLESS=true",
				"PYTHONDONTWRITEBYTECODE=1",
			),
			UID: &uid, GID: &gid,
		})
	}
	return svcs
}
