package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
)

// The optional bundled SABnzbd (install.sh --with-sabnzbd). cmd/arrmada registers it once
// its config shows up; these are the pieces the pages need: the port its own web page is
// on (that's where the Usenet provider is added), bringing it back after a delete, and the
// Getting started step that stays open until a news server is set up.

// sabUIPort is the host port the bundled SABnzbd's web page is published on. install.sh
// picks the first free one of 8085-8087 and compose passes it in as
// ARRMADA_SABNZBD_HOST_PORT.
func sabUIPort() string {
	if p := strings.TrimSpace(os.Getenv("ARRMADA_SABNZBD_HOST_PORT")); p != "" {
		return p
	}
	return "8085"
}

// bundledSABKey is the bundled SABnzbd's API key from its read-only config, "" when this
// install doesn't run one (the file only exists once it's switched on).
func (a *api) bundledSABKey() string {
	if a.deps.Config.SABnzbdURL == "" || a.deps.Config.SABnzbdConfig == "" {
		return ""
	}
	key, err := download.ReadSABAPIKey(a.deps.Config.SABnzbdConfig)
	if err != nil {
		return ""
	}
	return key
}

// bundledSABLogin is the bundled SABnzbd's own web page login, from its read-only config:
// the one its init container made, or download.Service.SecureBundledSAB gave it. ok is
// false when this install runs none or it has no login yet.
func (a *api) bundledSABLogin() (username, password string, ok bool) {
	if a.deps.Config.SABnzbdURL == "" || a.deps.Config.SABnzbdConfig == "" {
		return "", "", false
	}
	u, p, ok, err := download.ReadSABLogin(a.deps.Config.SABnzbdConfig)
	return u, p, err == nil && ok
}

// handleBundledSABLogin is GET /api/v1/downloadclients/bundled-login?kind=sabnzbd: the
// bundled SABnzbd's page login, for an admin who opens it (that's where the Usenet provider
// is added). Admin-only, never cached, never logged.
func (a *api) handleBundledSABLogin(w http.ResponseWriter, r *http.Request) {
	u, p, ok := a.bundledSABLogin()
	if !ok {
		a.writeError(w, http.StatusNotFound, "the bundled SABnzbd's page has no login yet; Arrmada gives it one when it next starts")
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	a.writeJSON(w, http.StatusOK, map[string]string{"username": u, "password": p})
}

// restoreBundledSAB is handleRestoreBundledClient for the bundled SABnzbd: it clears the
// removed flag and registers it again with the key from its config.
func (a *api) restoreBundledSAB(w http.ResponseWriter, r *http.Request) {
	key := a.bundledSABKey()
	if key == "" {
		a.writeError(w, http.StatusBadRequest, "this install has no bundled SABnzbd (install.sh --with-sabnzbd adds one)")
		return
	}
	if err := a.deps.Downloads.RestoreBundledSAB(r.Context(), a.deps.Config.SABnzbdURL, key); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not restore the bundled SABnzbd")
		return
	}
	a.recheckHealth(r, "downloads")
	if c, ok, err := a.deps.Downloads.BundledClient(r.Context(), download.KindSABnzbd); err == nil && ok && c.Enabled {
		a.kickProwlarrSync(c)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"restored": true})
}

// usenetProviderItem is the Getting started step for the bundled SABnzbd: it arrives with
// no news server, and nothing downloads from Usenet until the owner adds their provider in
// SABnzbd itself. It links to the client's card, which opens SABnzbd's page. ok is false
// when there's no switched-on bundled SABnzbd (the step isn't shown at all).
func (a *api) usenetProviderItem(ctx context.Context) (checklistItem, bool) {
	if a.deps.Downloads == nil {
		return checklistItem{}, false
	}
	c, ok, err := a.deps.Downloads.BundledClient(ctx, download.KindSABnzbd)
	if err != nil || !ok || !c.Enabled {
		return checklistItem{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, checklistProbeTimeout)
	defer cancel()
	f, err := a.deps.Downloads.UsenetFacts(ctx, c.ID)
	fix := health.ConnectionFix(fmt.Sprintf("download_client-%d", c.ID), "Open SABnzbd")
	return fixItem("usenet_provider", "Add your Usenet provider in SABnzbd", err == nil && f.ServersConfigured > 0, fix), true
}
