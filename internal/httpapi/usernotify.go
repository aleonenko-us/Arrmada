package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/notify"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// handleMyNotifications returns the current user's in-app inbox + unread count.
func (a *api) handleMyNotifications(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	items, err := a.deps.Requests.Inbox(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load notifications")
		return
	}
	if items == nil {
		items = []requests.UserNotification{}
	}
	a.inboxWatchLinks(r.Context(), items, u)
	unread := 0
	for _, n := range items {
		if !n.Read {
			unread++
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"notifications": items, "unread": unread})
}

// inboxWatchLinks gives each 'ready' notice its Watch buttons, one per media server: the
// title's page as that server's index has it now (from memory — a notice sent during the
// grace period gains one once the server has the title), else the one saved when it was
// sent. A server that isn't set up or whose index isn't built shows no link, so the bell
// never offers a Watch button that can't work. The reader's own server comes first;
// plex_url stays for older pages.
func (a *api) inboxWatchLinks(ctx context.Context, items []requests.UserNotification, reader *auth.User) {
	srcs := a.linkSources(ctx)
	for i := range items {
		n := &items[i]
		saved := map[mediaserver.Kind]string{mediaserver.Plex: n.PlexURL, mediaserver.Jellyfin: n.JellyfinURL}
		n.PlexURL, n.JellyfinURL, n.WatchLinks = "", "", nil
		if len(srcs) == 0 {
			continue
		}
		urls := make([]string, len(srcs))
		if media, id, ok := requests.ReadyRef(n.Ref); ok {
			urls = lookupLinks(ctx, srcs, media, insights.ExternalIDs{TMDB: id})
		}
		for j, s := range srcs {
			if urls[j] == "" {
				urls[j] = saved[s.kind]
			}
		}
		links := collectLinks(srcs, urls)
		n.PlexURL = linkFor(links, mediaserver.Plex)
		n.WatchLinks = a.leadWith(ctx, links, reader)
	}
}

// handleMarkNotificationRead marks one inbox item read.
func (a *api) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Requests.MarkRead(r.Context(), id, u.ID); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not update notification")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMarkAllNotificationsRead marks the current user's whole inbox read.
func (a *api) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if err := a.deps.Requests.MarkAllRead(r.Context(), u.ID); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not update notifications")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetMyApprise says whether the current user has a personal Apprise URL set — a
// hint of it, never the URL — and why it's being skipped if it no longer passes the
// check for their account.
func (a *api) handleGetMyApprise(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	a.writeMyApprise(w, r, u)
}

func (a *api) writeMyApprise(w http.ResponseWriter, r *http.Request, u *auth.User) {
	set, hint, blocked, err := a.deps.Requests.AppriseStatus(r.Context(), u.ID, u.Role.AtLeast(auth.RoleManager))
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load setting")
		return
	}
	out := map[string]any{"set": set, "hint": hint}
	if blocked != "" {
		out["blocked_reason"] = blocked
	}
	a.writeJSON(w, http.StatusOK, out)
}

// handleSetMyApprise sets the current user's personal Apprise URL (empty clears it).
func (a *api) handleSetMyApprise(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	// Validate before storing: the URL becomes an apprise CLI argument on the server
	// and the server posts to it, so for a requester it must be a push service that
	// can't be aimed at the local network (notify/ssrf.go). Staff keep the ordinary
	// rules. ("" clears the setting and is always allowed.)
	req.URL = strings.TrimSpace(req.URL)
	if req.URL != "" {
		staff := u.Role.AtLeast(auth.RoleManager)
		if verr := notify.ValidateUserAppriseURL(r.Context(), req.URL, staff, a.resolver()); verr != nil {
			a.writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
	}
	if err := a.deps.Requests.SetApprise(r.Context(), u.ID, req.URL); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save setting")
		return
	}
	a.writeMyApprise(w, r, u)
}

// handleGetNotifyPrefs is the caller's own per-event choices (which notices reach their
// phones and Apprise link), every known key filled in. Requesters only have use for the
// first three; new_request is staff's "Someone requests something".
func (a *api) handleGetNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	p, err := a.deps.Requests.NotifyPrefs(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not load your notification choices")
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

// handleSetNotifyPrefs changes the caller's own choices: a JSON object of key → bool.
// Unknown keys are ignored (an older or newer page can't break it), and keys left out
// keep their value.
func (a *api) handleSetNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	// Not decodeJSON: that refuses unknown fields, and these are ignored by design.
	var change map[string]bool
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&change); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := a.deps.Requests.SetNotifyPrefs(r.Context(), u.ID, change)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save your notification choices")
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

// resolver is what personal Apprise URLs are checked against: the system resolver, or
// a stub a test put in Deps.
func (a *api) resolver() notify.Resolver {
	if a.deps.Resolver != nil {
		return a.deps.Resolver
	}
	return net.DefaultResolver
}
