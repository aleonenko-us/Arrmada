package httpapi

import (
	"net/http"
	"os"
	"strings"

	"github.com/tristenlammi/arrmada/internal/aio"
)

// The Unraid all-in-one image's built-in qBittorrent. Arrmada and people alike sign in
// with a login generated on first start (internal/aio); people reach its own web page on
// a published port. Its card links that page and lets an admin look the login up. The
// Compose bundle has no such login, so none of this shows there.

// qbitUIPort is the host port the built-in qBittorrent's web page is published on, from
// the template (ARRMADA_QBIT_WEBUI_HOST_PORT), for the card's link.
func qbitUIPort() string {
	if p := strings.TrimSpace(os.Getenv("ARRMADA_QBIT_WEBUI_HOST_PORT")); p != "" {
		return p
	}
	return "8091"
}

// handleBundledQbitLogin is the built-in qBittorrent's web UI login, for an admin who
// wants qBittorrent's own page. Admin-only, never cached, never logged.
func (a *api) handleBundledQbitLogin(w http.ResponseWriter, r *http.Request) {
	path := a.deps.Config.QbittorrentLogin
	if path == "" {
		a.writeError(w, http.StatusNotFound, "this install's qBittorrent has no login made by Arrmada")
		return
	}
	login, err := aio.ReadLogin(path)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "the login Arrmada made for qBittorrent isn't there any more; use the one you set in qBittorrent")
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	a.writeJSON(w, http.StatusOK, map[string]string{"username": login.Username, "password": login.Password})
}
