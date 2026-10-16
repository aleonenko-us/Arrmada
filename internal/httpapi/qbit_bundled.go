package httpapi

import (
	"net/http"
	"os"
	"strings"

	"github.com/tristenlammi/arrmada/internal/aio"
)

// The bundled qBittorrent's login. Arrmada and people alike sign in with a login Arrmada
// made: the Unraid all-in-one image's on first start (internal/aio), the Compose bundle's
// when Arrmada gave it a password (download.Service.SecureBundledQbit). People reach its
// own web page on a published port. Its card links that page and lets an admin look the
// login up.

// bundledQbitLogin is the login Arrmada made for the bundled qBittorrent, false when it
// hasn't made one (the Compose bundle before the switch, or a file gone missing).
func (a *api) bundledQbitLogin() (aio.Login, bool) {
	path := a.deps.Config.QbittorrentLogin
	if path == "" {
		return aio.Login{}, false
	}
	l, err := aio.ReadLogin(path)
	return l, err == nil
}

// qbitUIPort is the host port the bundled qBittorrent's web page is published on, for the
// card's link: the all-in-one template's (ARRMADA_QBIT_WEBUI_HOST_PORT, 8091 by default),
// or the one docker-compose.yml publishes (it passes ARRMADA_QBIT_WEBUI_HOST_PORT too).
// Empty when the Compose file doesn't say (the copy-paste example publishes none).
func (a *api) qbitUIPort() string {
	if p := strings.TrimSpace(os.Getenv("ARRMADA_QBIT_WEBUI_HOST_PORT")); p != "" {
		return p
	}
	if a.deps.Config.AIOQbittorrentURL != "" {
		return "8091"
	}
	return ""
}

// handleBundledQbitLogin is the bundled qBittorrent's web UI login, for an admin who
// wants qBittorrent's own page. Admin-only, never cached, never logged.
func (a *api) handleBundledQbitLogin(w http.ResponseWriter, r *http.Request) {
	if a.deps.Config.QbittorrentLogin == "" {
		a.writeError(w, http.StatusNotFound, "this install's qBittorrent has no login made by Arrmada")
		return
	}
	login, ok := a.bundledQbitLogin()
	if !ok {
		a.writeError(w, http.StatusNotFound, "there's no login made by Arrmada for qBittorrent; use the one you set in qBittorrent")
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	a.writeJSON(w, http.StatusOK, map[string]string{"username": login.Username, "password": login.Password})
}
