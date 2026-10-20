package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// "Your Plex watchlist" on Discover, and its "Request everything on my Plex watchlist
// automatically" switch. Arrmada holds only the server owner's Plex token, so the
// watchlist is theirs: it shows to the account linked to the owner's Plex account, or to
// admins while no account is (internal/plexwatch).

// watchlistViewer reports whether u is the person whose Plex watchlist Arrmada can read.
func (a *api) watchlistViewer(ctx context.Context, u *auth.User) bool {
	pw := a.deps.PlexWatchlist
	if u == nil || u.Disabled || pw == nil || !pw.Linked(ctx) {
		return false
	}
	if owner := pw.OwnerPlexID(ctx); owner != "" && a.deps.Auth != nil {
		if a.deps.Auth.PlexIDForUser(ctx, u.ID) == owner {
			return true
		}
		if linked, err := a.deps.Auth.UserByPlexID(ctx, owner); err != nil || linked != nil {
			return false // someone else is the owner here (or we can't tell)
		}
	}
	return u.Role.AtLeast(auth.RoleAdmin)
}

// handlePlexWatchlist is the row: the watchlist's titles as cards, minus what's in the
// library and what the viewer hid, and whether automatic requests are on. Anyone who
// isn't the watchlist's owner gets an empty row (the page then shows none).
//
//	GET /api/v1/discover/plex-watchlist → {items, auto}
func (a *api) handlePlexWatchlist(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	ctx := r.Context()
	if !a.watchlistViewer(ctx, u) || !a.metadataReady() {
		a.writeJSON(w, http.StatusOK, map[string]any{"items": []discoverCard{}, "auto": false, "available": false})
		return
	}
	row, err := a.deps.PlexWatchlist.Row(ctx)
	if errors.Is(err, plex.ErrWatchlistToken) {
		a.writeError(w, http.StatusBadGateway, "Plex refused the saved token for the watchlist.")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "Couldn’t read your Plex watchlist right now.")
		return
	}
	cards := make([]discoverCard, 0, len(row))
	for _, c := range a.rowCards(ctx, row) {
		if !c.InLibrary && !c.HasFile {
			cards = append(cards, c)
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": cards, "auto": a.deps.PlexWatchlist.Auto(ctx), "available": true})
}

// handlePlexWatchlistAuto switches "Request everything on my Plex watchlist
// automatically" on or off. The requests are made as whoever switched it on, through the
// normal request path (their auto-approve and request limits apply).
//
//	PUT /api/v1/discover/plex-watchlist/auto {on: bool}
func (a *api) handlePlexWatchlistAuto(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r)
	ctx := r.Context()
	if !a.watchlistViewer(ctx, u) {
		a.writeError(w, http.StatusForbidden, "Only the Plex server’s owner can change this.")
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if !a.decodeJSON(w, r, &in) {
		return
	}
	if err := a.deps.PlexWatchlist.SetAuto(ctx, in.On, u.ID); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	a.deps.Log.Info("discover: Plex watchlist automatic requests switched", "on", in.On, "by", u.Username)
	a.writeJSON(w, http.StatusOK, map[string]any{"auto": in.On})
}

// handleDiscoverCertifications lists the region's age ratings for the browse filter.
//
//	GET /api/v1/discover/certifications?media=movie|series → {country, certifications}
func (a *api) handleDiscoverCertifications(w http.ResponseWriter, r *http.Request) {
	certs, ok := a.deps.Discovery.(metadata.DiscoverCertifications)
	if !ok || !a.metadataReady() {
		a.writeJSON(w, http.StatusOK, metadata.Certifications{Items: []metadata.Certification{}})
		return
	}
	c, err := certs.Certifications(r.Context(), r.URL.Query().Get("media"))
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, c)
}
