package httpapi

import (
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// personRow is one person on Settings → People & access: their account, how they get in,
// when they were last around, and whether they may use the audiobook server.
//
// Audiobook privacy: the listening times are when, never what. Nothing here comes from a
// column that names a book (item_key, titles) — the audio times are read from the device
// list and the listening log's totals, which have none — and TestPeopleListNoListeningContent
// holds the JSON keys to this list.
type personRow struct {
	ID                int64          `json:"id"`
	Username          string         `json:"username"`
	Role              auth.Role      `json:"role"`
	Disabled          bool           `json:"disabled"`
	CreatedAt         string         `json:"created_at,omitempty"`
	AutoApproveMovie  bool           `json:"auto_approve_movie"`
	AutoApproveSeries bool           `json:"auto_approve_series"`
	AutoApproveBook   bool           `json:"auto_approve_book"`
	PlexLinked        bool           `json:"plex_linked"`
	PlexUsername      string         `json:"plex_username,omitempty"`
	PlexOnly          bool           `json:"plex_only,omitempty"`
	PlexBlocked       bool           `json:"plex_blocked,omitempty"`
	Quota             auth.UserQuota `json:"quota"`
	// Sessions is how many browsers they're signed in on (only they see which).
	Sessions int `json:"sessions"`
	// Unix ms, 0 = never: the web app, a listening app, and the last listening.
	WebLast    int64 `json:"web_last"`
	AudioLast  int64 `json:"audio_last"`
	ListenLast int64 `json:"listen_last"`
	// LastSeen is the latest of the three.
	LastSeen int64 `json:"last_seen"`
	// AudioEligible: an enabled account that may request (read-only can't connect).
	AudioEligible    bool `json:"audio_eligible"`
	AudioAllowed     bool `json:"audio_allowed"`
	AudioPasswordSet bool `json:"audio_password_set"`
	AudioDevices     int  `json:"audio_devices"`
}

// handlePeople — GET /api/v1/people (admin): everyone once, with role, auto-approve, Plex
// link, last seen, sessions and audiobook access, so one list replaces Users, the
// audiobook access list and the Plex blocks.
func (a *api) handlePeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := a.deps.Auth.ListUsers(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list people")
		return
	}
	quotas, err := a.deps.Auth.Quotas(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list people")
		return
	}
	activity, err := a.deps.Auth.SessionActivityByUser(ctx)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list people")
		return
	}
	blocked := map[string]bool{}
	for _, b := range a.plexBlocks(ctx) {
		blocked[b.PlexID] = true
	}

	// The audiobook side, when the server is built in: who's switched off, who has a
	// password, each signed-in app's last use, and the last listening (a time only).
	denied := map[int64]bool{}
	withPassword := map[int64]bool{}
	audioLast := map[int64]int64{}
	audioDevices := map[int64]int{}
	listenLast := map[int64]int64{}
	if as := a.deps.AudioServer; as != nil {
		for _, id := range as.DeniedUsers(ctx) {
			denied[id] = true
		}
		withPassword = as.Accounts.PasswordsSet(ctx)
		if devices, err := as.Accounts.Devices(ctx, 0); err == nil {
			for _, d := range devices {
				audioDevices[d.UserID]++
				audioLast[d.UserID] = max(audioLast[d.UserID], d.LastUsedAt)
			}
		}
		// Only the last listening is read from the totals; the day boundary doesn't matter.
		if totals, err := as.Listen().Totals(ctx, time.Now()); err == nil {
			for id, t := range totals {
				listenLast[id] = t.LastListen
			}
		}
	}

	out := make([]personRow, 0, len(users))
	for _, u := range users {
		p := personRow{
			ID: u.ID, Username: u.Username, Role: u.Role, Disabled: u.Disabled, CreatedAt: u.CreatedAt,
			AutoApproveMovie: u.AutoApproveMovie, AutoApproveSeries: u.AutoApproveSeries, AutoApproveBook: u.AutoApproveBook,
			PlexLinked: u.PlexLinked, PlexUsername: u.PlexUsername, PlexOnly: u.PlexOnly,
			Quota:         auth.DefaultQuota,
			AudioEligible: !u.Disabled && u.Role.AtLeast(auth.RoleRequester),
			AudioAllowed:  !denied[u.ID], AudioPasswordSet: withPassword[u.ID],
			AudioLast: audioLast[u.ID], AudioDevices: audioDevices[u.ID], ListenLast: listenLast[u.ID],
		}
		if q, ok := quotas[u.ID]; ok {
			p.Quota = q
		}
		if u.PlexLinked && len(blocked) > 0 {
			p.PlexBlocked = blocked[a.deps.Auth.PlexIDForUser(ctx, u.ID)]
		}
		if s, ok := activity[u.ID]; ok {
			p.Sessions = s.Sessions
			if !s.LastSeen.IsZero() {
				p.WebLast = s.LastSeen.UnixMilli()
			}
		}
		p.LastSeen = max(p.WebLast, p.AudioLast, p.ListenLast)
		out = append(out, p)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"people": out})
}
