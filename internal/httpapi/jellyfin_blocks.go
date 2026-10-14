package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// Blocked Jellyfin users, like blocked Plex accounts (plex_blocks.go): deleting someone
// doesn't keep them out, since their next Sign in with Jellyfin would make a new account.
// The list holds the Jellyfin user ids the owner blocked, with the name they saw.

const jellyfinBlocksKey = "jellyfin_login_blocked"

type jellyfinBlock struct {
	JellyfinID string `json:"jellyfin_id"`
	Name       string `json:"name"`
	At         string `json:"at"`
}

var jellyfinBlocksMu sync.Mutex

func (a *api) jellyfinBlocks(ctx context.Context) []jellyfinBlock {
	var out []jellyfinBlock
	if raw := a.deps.Settings.Get(ctx, jellyfinBlocksKey, ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			a.deps.Log.Warn("users: the blocked Jellyfin users list is unreadable", "err", err)
		}
	}
	return out
}

func (a *api) saveJellyfinBlocks(ctx context.Context, list []jellyfinBlock) error {
	if list == nil {
		list = []jellyfinBlock{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return a.deps.Settings.Set(ctx, jellyfinBlocksKey, string(b))
}

// jellyfinBlocked reports whether a Jellyfin user id is on the block list.
func (a *api) jellyfinBlocked(ctx context.Context, jellyfinID string) bool {
	jellyfinID = strings.TrimSpace(jellyfinID)
	if jellyfinID == "" {
		return false
	}
	for _, b := range a.jellyfinBlocks(ctx) {
		if b.JellyfinID == jellyfinID {
			return true
		}
	}
	return false
}

func (a *api) addJellyfinBlock(ctx context.Context, jellyfinID, name string) (added bool, err error) {
	jellyfinBlocksMu.Lock()
	defer jellyfinBlocksMu.Unlock()
	list := a.jellyfinBlocks(ctx)
	for _, b := range list {
		if b.JellyfinID == jellyfinID {
			return false, nil
		}
	}
	list = append(list, jellyfinBlock{JellyfinID: jellyfinID, Name: name, At: time.Now().UTC().Format(time.RFC3339)})
	return true, a.saveJellyfinBlocks(ctx, list)
}

func (a *api) removeJellyfinBlock(ctx context.Context, jellyfinID string) (removed bool, err error) {
	jellyfinBlocksMu.Lock()
	defer jellyfinBlocksMu.Unlock()
	list := a.jellyfinBlocks(ctx)
	kept := list[:0]
	for _, b := range list {
		if b.JellyfinID == jellyfinID {
			removed = true
			continue
		}
		kept = append(kept, b)
	}
	if !removed {
		return false, nil
	}
	return true, a.saveJellyfinBlocks(ctx, kept)
}

// jellyfinSignInAllowed is the Jellyfin sign-in decision: a blocked Jellyfin user is refused
// before any account is found or made (u is nil then), and a disabled account after.
func (a *api) jellyfinSignInAllowed(ctx context.Context, jellyfinID string, u *auth.User) (bool, string) {
	if a.jellyfinBlocked(ctx, jellyfinID) {
		return false, "This Jellyfin user isn't allowed to sign in here."
	}
	if u != nil && u.Disabled {
		return false, "This account is disabled."
	}
	return true, ""
}

// handleListJellyfinBlocks: GET /api/v1/users/jellyfin-blocks.
func (a *api) handleListJellyfinBlocks(w http.ResponseWriter, r *http.Request) {
	list := a.jellyfinBlocks(r.Context())
	if list == nil {
		list = []jellyfinBlock{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"blocks": list})
}

// handleBlockUserJellyfin: POST /api/v1/users/{id}/block-jellyfin blocks the Jellyfin user
// linked to an account. The account stays as it is; disable it to sign them out now.
func (a *api) handleBlockUserJellyfin(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	u, err := a.deps.Auth.UserByID(ctx, id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if me, ok := userFrom(r); ok && me.ID == id {
		a.writeError(w, http.StatusBadRequest, "you can't block your own Jellyfin user")
		return
	}
	jfID := a.deps.Auth.JellyfinIDForUser(ctx, id)
	if jfID == "" {
		a.writeError(w, http.StatusBadRequest, "this account isn't linked to Jellyfin")
		return
	}
	if _, err := a.addJellyfinBlock(ctx, jfID, u.Username); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the block")
		return
	}
	a.deps.Log.Info("users: Jellyfin user blocked", "user_id", id)
	a.writeJSON(w, http.StatusOK, map[string]any{"blocks": a.jellyfinBlocks(ctx)})
}

// handleUnblockJellyfin: DELETE /api/v1/users/jellyfin-blocks/{jellyfinID}.
func (a *api) handleUnblockJellyfin(w http.ResponseWriter, r *http.Request) {
	removed, err := a.removeJellyfinBlock(r.Context(), r.PathValue("jellyfinID"))
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the block list")
		return
	}
	if !removed {
		a.writeError(w, http.StatusNotFound, "that Jellyfin user isn't blocked")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"blocks": a.jellyfinBlocks(r.Context())})
}
