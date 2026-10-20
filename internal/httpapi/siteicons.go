package httpapi

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/tristenlammi/arrmada/internal/siteicon"
)

// siteIconsOnce makes the icon service on first use, under the data folder.
type siteIconsOnce struct {
	once sync.Once
	svc  *siteicon.Service
}

func (a *api) icons() *siteicon.Service {
	a.siteIcons.once.Do(func() {
		a.siteIcons.svc = siteicon.New(filepath.Join(a.deps.Config.DataDir, "cache", "site-icons"))
	})
	return a.siteIcons.svc
}

// handleSiteIcon serves one indexer's site icon by slug (only sites on siteicon's list).
// 404 means none to be had: the UI shows the tracker's monogram instead.
func (a *api) handleSiteIcon(w http.ResponseWriter, r *http.Request) {
	b, ct, err := a.icons().Get(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, siteicon.ErrUnknown) || errors.Is(err, siteicon.ErrNone) {
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.Error(w, "no icon", http.StatusNotFound)
			return
		}
		a.writeError(w, http.StatusBadGateway, "could not fetch the icon")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=604800")
	_, _ = w.Write(b)
}
