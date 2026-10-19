package httpapi

import (
	"context"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// The MusicBrainz card (Settings → Connections): which server Arrmada asks, how fast it
// may ask a self-hosted mirror, the contact the User-Agent carries, a Test, and how big
// the permanent answer cache is. Admin only: it names an address the server will call,
// and a private (LAN) address is allowed here on purpose — that's where a mirror lives.

// mbTestTimeout bounds the Test button's one lookup (MusicBrainz's 503 retries included).
const mbTestTimeout = 30 * time.Second

// musicBrainzView is the card's state.
type musicBrainzView struct {
	BaseURL        string                `json:"base_url"` // "" = the public server
	DefaultBaseURL string                `json:"default_base_url"`
	MirrorRate     int                   `json:"mirror_rate"` // requests a second to a mirror
	MaxMirrorRate  int                   `json:"max_mirror_rate"`
	Contact        string                `json:"contact"`
	Cache          metadata.MBCacheStats `json:"cache"`
	// Paused: MusicBrainz refused recent background lookups; until then Music runs on the
	// cache and only lookups someone is waiting on are sent.
	Paused      bool   `json:"paused"`
	PausedUntil string `json:"paused_until,omitempty"`
}

// mbMirrorRate is the saved mirror rate, 1 when unset or unreadable.
func (a *api) mbMirrorRate(ctx context.Context) int {
	n, err := strconv.Atoi(a.deps.Settings.Get(ctx, settings.KeyMusicBrainzMirrorRate, ""))
	if err != nil || n < 1 {
		return 1
	}
	if n > metadata.MBMaxMirrorRate {
		return metadata.MBMaxMirrorRate
	}
	return n
}

func (a *api) musicBrainzState(ctx context.Context) musicBrainzView {
	v := musicBrainzView{
		BaseURL:        a.deps.Settings.Get(ctx, settings.KeyMusicBrainzURL, ""),
		DefaultBaseURL: metadata.MBDefaultBaseURL,
		MirrorRate:     a.mbMirrorRate(ctx),
		MaxMirrorRate:  metadata.MBMaxMirrorRate,
		Contact:        a.deps.Settings.Get(ctx, settings.KeyMusicBrainzContact, ""),
	}
	if a.deps.MusicBrainz != nil {
		if s, err := a.deps.MusicBrainz.CacheStats(ctx); err == nil {
			v.Cache = s
		}
	}
	if paused, until := metadata.MusicBrainzPaused(); paused {
		v.Paused, v.PausedUntil = true, until.UTC().Format(time.RFC3339)
	}
	return v
}

func (a *api) handleGetMusicBrainz(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.musicBrainzState(r.Context()))
}

// normalizeMBContact checks the contact: one plain email address, or "" for none.
func normalizeMBContact(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", true
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s || strings.ContainsAny(s, "()") {
		return "", false
	}
	return s, true
}

func (a *api) handleSetMusicBrainz(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL    *string `json:"base_url"`
		MirrorRate *int    `json:"mirror_rate"`
		Contact    *string `json:"contact"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	// Everything is checked before anything is saved.
	var base, contact string
	if req.BaseURL != nil {
		v, err := metadata.NormalizeMBBaseURL(*req.BaseURL)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if v == metadata.MBDefaultBaseURL {
			v = "" // the public server is the default, not a setting
		}
		base = v
	}
	if req.MirrorRate != nil && (*req.MirrorRate < 1 || *req.MirrorRate > metadata.MBMaxMirrorRate) {
		a.writeError(w, http.StatusBadRequest, "a mirror's rate must be between 1 and "+strconv.Itoa(metadata.MBMaxMirrorRate)+" requests a second")
		return
	}
	if req.Contact != nil {
		v, ok := normalizeMBContact(*req.Contact)
		if !ok {
			a.writeError(w, http.StatusBadRequest, "the contact must be one email address, like you@example.com")
			return
		}
		contact = v
	}
	save := func(key, value string) bool {
		if err := a.deps.Settings.Set(ctx, key, value); err != nil {
			a.writeError(w, http.StatusInternalServerError, "could not save the MusicBrainz settings")
			return false
		}
		return true
	}
	if req.BaseURL != nil && !save(settings.KeyMusicBrainzURL, base) {
		return
	}
	if req.MirrorRate != nil && !save(settings.KeyMusicBrainzMirrorRate, strconv.Itoa(*req.MirrorRate)) {
		return
	}
	if req.Contact != nil && !save(settings.KeyMusicBrainzContact, contact) {
		return
	}
	a.writeJSON(w, http.StatusOK, a.musicBrainzState(ctx))
}

// handleTestMusicBrainz looks up a well-known artist at an address (the one sent, or the
// saved one), not from the cache, and says plainly whether it answered.
func (a *api) handleTestMusicBrainz(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL *string `json:"base_url"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if a.deps.MusicBrainz == nil {
		a.writeError(w, http.StatusServiceUnavailable, "MusicBrainz isn't available")
		return
	}
	base := a.deps.Settings.Get(r.Context(), settings.KeyMusicBrainzURL, "")
	if req.BaseURL != nil {
		v, err := metadata.NormalizeMBBaseURL(*req.BaseURL)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		base = v
	}
	ctx, cancel := context.WithTimeout(r.Context(), mbTestTimeout)
	defer cancel()
	if err := a.deps.MusicBrainz.Test(ctx, base); err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": "MusicBrainz answered."})
}
