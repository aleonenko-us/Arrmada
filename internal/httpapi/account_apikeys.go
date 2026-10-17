package httpapi

import (
	"errors"
	"net/http"

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
	need, err := a.keyStepUp(r, u)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list your API keys")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"keys":        keys,
		"expiry_days": auth.APIKeyExpiryDays,
		"max":         auth.MaxAPIKeysPerUser,
		// What making a key will ask for (see handleCreateMyAPIKey).
		"password_required": need.password,
		"code_required":     need.code,
		"reauth_required":   need.reauth,
	})
}

// keyStepUp is what making a key asks of the caller beyond being signed in.
type keyStepUp struct {
	password bool // the account has a password: type it
	code     bool // two-factor sign-in is on: a current code too
	reauth   bool // no password to ask for, and this session wasn't signed in recently
}

func (a *api) keyStepUp(r *http.Request, u *auth.User) (keyStepUp, error) {
	var s keyStepUp
	var err error
	if s.password, err = a.deps.Auth.HasPassword(r.Context(), u.ID); err != nil {
		return s, err
	}
	if s.code, err = a.deps.Auth.TwoFactorEnabled(r.Context(), u.ID); err != nil {
		return s, err
	}
	if !s.password {
		recent, err := a.deps.Auth.SessionSignedInRecently(r.Context(), u.ID, sessionToken(r))
		if err != nil {
			return s, err
		}
		s.reauth = !recent
	}
	return s, nil
}

// handleCreateMyAPIKey makes a key for the caller: {name, scope: "read"|"full",
// expires_in_days: 0|30|90|365, password, code}. It answers 201 with the key, once.
//
// A key outlives the session that made it, so a borrowed or hijacked browser session (or
// a script injected into the page) must not be enough to mint a lasting way in:
//   - an account with a password types it (throttled like the password change's);
//   - with two-factor sign-in on, a current code too (under the sign-in code limits);
//   - an account with no password (Plex or Jellyfin sign-in only) can only make a key from
//     a session signed in within auth.KeyMintWindow; otherwise it's asked to sign in again
//     (403, code "reauth"). Signing in through Plex or Jellyfin is its step-up.
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
		Code          string `json:"code"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	n := auth.NewAPIKey{Name: req.Name, Scope: auth.KeyScope(req.Scope), ExpiresInDays: req.ExpiresInDays}
	// A bad form is refused before a password or a one-time code is spent on it.
	if err := n.Validate(); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	need, err := a.keyStepUp(r, u)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not make the key")
		return
	}
	if need.reauth {
		a.writeJSON(w, http.StatusForbidden, map[string]any{
			"status": "error", "code": "reauth",
			"message": "For your safety, keys can only be made within 15 minutes of signing in. Sign out and sign in again, then make the key.",
		})
		return
	}
	if need.password && !a.checkMyPassword(w, r, u, req.Password) {
		return
	}
	if need.code && !a.checkMyCode(w, r, u, req.Code) {
		return
	}
	raw, key, err := a.deps.Auth.CreateAPIKey(r.Context(), u.ID, n)
	switch {
	case errors.Is(err, auth.ErrAPIKeyName), errors.Is(err, auth.ErrAPIKeyScope),
		errors.Is(err, auth.ErrAPIKeyExpiry), errors.Is(err, auth.ErrTooManyKeys), errors.Is(err, auth.ErrAPIKeyAccount):
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not make the key")
		return
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
