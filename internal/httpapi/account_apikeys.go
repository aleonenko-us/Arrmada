package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// Personal API keys (Me → Personal API keys; the admin's list is in People & access). A
// key lets a script call Arrmada as its owner; internal/auth/apikeys.go has the scheme
// and apikey_routes.go the routes a key may call. None of these routes is among them: a
// key can't list, make or revoke keys, so a leaked key can't mint itself a successor.
//
// The key itself appears in exactly one answer, the one that made it. Lists carry a hint
// ("arrk_…7f3q"), never the key or its hash, and no log line names either.

// handleMyAPIKeys lists the caller's own keys, with what the create form offers.
func (a *api) handleMyAPIKeys(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	keys, err := a.deps.Auth.ListAPIKeys(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list your API keys")
		return
	}
	has, err := a.deps.Auth.HasPassword(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list your API keys")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"keys":        keys,
		"expiry_days": auth.APIKeyExpiryDays,
		"max":         auth.MaxAPIKeysPerUser,
		// Making a key asks for the account's password when it has one (see below).
		"password_required": has,
	})
}

// handleCreateMyAPIKey makes a key for the caller: {name, scope: "read"|"full",
// expires_in_days: 0|30|90|365, password}. It answers 201 with the key, once.
//
// The account's password is asked for when it has one. A key outlives the session that
// made it, so a borrowed or hijacked browser session (or a script injected into the page)
// must not be enough to mint a lasting way in. A Plex- or Jellyfin-only account has no
// password to ask for; its session is all there is. Wrong passwords are throttled like
// the password change's.
func (a *api) handleCreateMyAPIKey(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var req struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expires_in_days"`
		Password      string `json:"password"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	pwKey := "pw:" + strconv.FormatInt(u.ID, 10)
	if l := a.loginLimiter; l != nil {
		if blocked, retry := l.blocked(pwKey); blocked {
			a.tooManyAttempts(w, retry)
			return
		}
	}
	has, err := a.deps.Auth.HasPassword(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not make the key")
		return
	}
	if has && !a.deps.Auth.CheckPassword(r.Context(), u.ID, req.Password) {
		if a.loginLimiter != nil {
			a.loginLimiter.fail(pwKey, "")
		}
		a.writeError(w, http.StatusBadRequest, "Your password is wrong")
		return
	}
	raw, key, err := a.deps.Auth.CreateAPIKey(r.Context(), u.ID, auth.NewAPIKey{
		Name: req.Name, Scope: auth.KeyScope(req.Scope), ExpiresInDays: req.ExpiresInDays,
	})
	switch {
	case errors.Is(err, auth.ErrAPIKeyName), errors.Is(err, auth.ErrAPIKeyScope),
		errors.Is(err, auth.ErrAPIKeyExpiry), errors.Is(err, auth.ErrTooManyKeys), errors.Is(err, auth.ErrAPIKeyAccount):
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not make the key")
		return
	}
	if has && a.loginLimiter != nil {
		a.loginLimiter.reset(pwKey, "")
	}
	// Who, which key and what it may do; never the key.
	a.deps.Log.Info("account: personal API key made", "user_id", u.ID, "key_id", key.ID, "scope", string(key.Scope), "expires_in_days", req.ExpiresInDays)
	a.writeJSON(w, http.StatusCreated, map[string]any{"key": raw, "api_key": key})
}

// handleRevokeMyAPIKey revokes one of the caller's keys. Another account's key is simply
// not found. The next request with it is refused.
func (a *api) handleRevokeMyAPIKey(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	gone, err := a.deps.Auth.RevokeAPIKey(r.Context(), u.ID, id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not revoke the key")
		return
	}
	if !gone {
		a.writeError(w, http.StatusNotFound, "no such key")
		return
	}
	a.deps.Log.Info("account: personal API key revoked by its owner", "user_id", u.ID, "key_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleListAllAPIKeys is the admin's view: every account's keys, whose and when, never
// from where (that's the owner's, like their devices).
func (a *api) handleListAllAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.deps.Auth.ListAllAPIKeys(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list API keys")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// handleRevokeAnyAPIKey is an admin revoking anyone's key.
func (a *api) handleRevokeAnyAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	owner, err := a.deps.Auth.RevokeAnyAPIKey(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not revoke the key")
		return
	}
	if owner == 0 {
		a.writeError(w, http.StatusNotFound, "no such key")
		return
	}
	var by int64
	if me, ok := userFrom(r); ok && me != nil {
		by = me.ID
	}
	a.deps.Log.Info("users: personal API key revoked by an admin", "user_id", owner, "key_id", id, "by_user_id", by)
	w.WriteHeader(http.StatusNoContent)
}
