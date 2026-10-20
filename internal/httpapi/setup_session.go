package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/netutil"
)

// The first-run wizard runs before any account exists: providers first, the account last,
// so "Sign in with Plex" has a server to check against. Its calls (save the keys, the
// library folders, the media server, the indexers) are admin calls, and there is no admin
// yet, so they are authorised by a SETUP SESSION instead.
//
// What a setup session is, and is not:
//   - It is issued (POST /api/v1/setup/session) only while NO admin exists, and only to a
//     request that passes the same LAN-only rule as the first admin (ARRMADA_ALLOW_REMOTE_SETUP
//     lifts it for both). It lives in its own cookie, arrmada_setup (HttpOnly,
//     SameSite=Strict, Path=/api/v1), and is never looked at by authenticate: it is not a
//     login session and no route that needs a signed-in account will take it.
//   - It is re-checked on EVERY request that uses it, not just at issue time: the token must
//     be known and unexpired (two hours), no admin may exist, and the request must still
//     pass the LAN rule. The moment the first admin exists, every setup session is dead.
//   - It is accepted by an explicit list of routes only: those marked viaSetup() in
//     server.go, which are exactly the calls SetupWizard.tsx makes. A route not on the list
//     answers it 401 as it would anyone not signed in. A few of the listed routes narrow
//     themselves further when called this way (which settings, which key ids, which folder
//     roots, no secret hints in answers); see wizardSettings, wizardKeyID and wizardPaths.
//   - State-changing calls go through the same-origin checks every route has (csrf.go), on
//     top of SameSite=Strict.
//
// Anyone who can reach a fresh install from the LAN could already make the first admin
// through POST /api/v1/auth/setup, so a setup session grants strictly less than that.

const setupCookieName = "arrmada_setup"

const setupCtxKey = keyUseCtxKey + 1

// withSetupSession marks a request as authorised by a (just validated) setup session.
func withSetupSession(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), setupCtxKey, true))
}

// viaSetupSession reports whether the request got through on a setup session rather than
// an account. Handlers that narrow themselves for the wizard ask this.
func viaSetupSession(r *http.Request) bool {
	v, _ := r.Context().Value(setupCtxKey).(bool)
	return v
}

// setupAllowedFrom is the LAN rule for first-run setup, the one handleSetup is registered
// under: not from outside the home network unless ARRMADA_ALLOW_REMOTE_SETUP is set.
func (a *api) setupAllowedFrom(r *http.Request) bool {
	return a.deps.Config.AllowRemoteSetup || !a.classifyExternal(r)
}

// setupSessionOK is the per-request check: a setup cookie that names a live session, no
// admin anywhere, and a request that passes the LAN rule. It writes nothing.
func (a *api) setupSessionOK(r *http.Request) bool {
	c, err := r.Cookie(setupCookieName)
	if err != nil || c.Value == "" || a.deps.Auth == nil {
		return false
	}
	if !a.setupAllowedFrom(r) {
		return false
	}
	return a.deps.Auth.SetupSessionValid(r.Context(), c.Value)
}

func (a *api) setSetupCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     setupCookieName,
		Value:    token,
		Path:     "/api/v1",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *api) clearSetupCookie(w http.ResponseWriter, r *http.Request) {
	a.setSetupCookie(w, r, "", -1)
}

// handleSetupSession — POST /api/v1/setup/session. Starts the wizard's setup session. The
// token only ever travels in the cookie; the body says when it ends.
func (a *api) handleSetupSession(w http.ResponseWriter, r *http.Request) {
	n, err := a.deps.Auth.CountAdmins(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not check setup state")
		return
	}
	if n > 0 {
		a.writeError(w, http.StatusConflict, "setup already complete")
		return
	}
	if !a.setupAllowedFrom(r) {
		a.writeError(w, http.StatusForbidden, "For safety, first-run setup can only be started from your home network.")
		return
	}
	// Each one is a row, so the endpoint shares the first-admin endpoint's throttle.
	if !a.loginAllowed(w, r, "setupsession:"+netutil.ClientIP(r)) {
		return
	}
	token, exp, err := a.deps.Auth.CreateSetupSession(r.Context())
	if errors.Is(err, auth.ErrSetupClosed) {
		a.writeError(w, http.StatusConflict, "setup already complete")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not start setup")
		return
	}
	a.setSetupCookie(w, r, token, int(auth.SetupSessionTTL.Seconds()))
	a.writeJSON(w, http.StatusCreated, map[string]any{"expires_at": exp.UTC().Format("2006-01-02T15:04:05Z")})
}

// handleSetupAccount — POST /api/v1/setup/account {username, password}: the wizard's last
// step. Makes the first admin, ends setup (setup_complete), kills the setup session and
// signs the new admin in. The admin insert is conditional on "no admin exists" in SQL, so
// two browsers finishing at once get one admin and one 409.
func (a *api) handleSetupAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if n, err := a.deps.Auth.CountAdmins(ctx); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not check setup state")
		return
	} else if n > 0 {
		a.writeError(w, http.StatusConflict, "setup already complete")
		return
	}
	if !a.setupSessionOK(r) {
		a.writeError(w, http.StatusUnauthorized, "your setup session has ended — reload the page to start again")
		return
	}
	if !a.loginAllowed(w, r, "setup:"+netutil.ClientIP(r)) {
		return
	}
	var body credentials
	if !a.decodeJSON(w, r, &body) {
		return
	}
	u, err := a.deps.Auth.CreateFirstAdmin(ctx, body.Username, body.Password)
	if err != nil {
		a.writeAuthError(w, err)
		return
	}
	a.firstAdminMade(w, r)
	a.startSession(w, r, u, http.StatusCreated)
}

// firstAdminMade is what follows the first admin being made by any route: setup is over
// (setup_complete, so the wizard doesn't come back) and the setup cookie is cleared.
func (a *api) firstAdminMade(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Settings.Set(r.Context(), keySetupComplete, "true"); err != nil {
		a.deps.Log.Warn("setup: couldn't record that setup is complete", "err", err)
	}
	a.clearSetupCookie(w, r)
}

// claimFirstAdmin is a media server's owner signing in while a setup session is open: the
// caller has already had the server vouch that this is its owner (Plex: the account owns
// the connected server; Jellyfin: an administrator). kind is "plex" or "jellyfin". On a
// refusal it has written the answer and returns nil.
func (a *api) claimFirstAdmin(w http.ResponseWriter, r *http.Request, kind, extID, extName string) *auth.User {
	u, err := a.deps.Auth.ClaimFirstAdminViaLink(r.Context(), kind, extID, extName)
	switch {
	case errors.Is(err, auth.ErrSetupClosed):
		a.writeError(w, http.StatusConflict, "Setup was already finished by someone else. Sign in instead.")
		return nil
	case errors.Is(err, auth.ErrUserExists):
		a.writeError(w, http.StatusConflict, "This account already exists here. Sign in instead.")
		return nil
	case err != nil:
		a.deps.Log.Error("setup: could not make the first admin from a media server sign-in", "server", kind, "err", err)
		a.writeError(w, http.StatusInternalServerError, "could not create your account")
		return nil
	}
	// The owner has to be able to sign in this way again: staff sign-in with this server
	// is the setting that lets a linked admin in. They chose this by signing in as owner.
	key := keyPlexSignInStaff
	if kind == "jellyfin" {
		key = keyJellyfinSignInStaff
	}
	if err := a.deps.Settings.SetBool(r.Context(), key, true); err != nil {
		a.deps.Log.Warn("setup: couldn't switch on staff sign-in for the new admin", "server", kind, "err", err)
	}
	a.deps.Log.Info("setup: the first admin signed up with their media server", "server", kind, "user_id", u.ID)
	a.firstAdminMade(w, r)
	return u
}

// --- narrowing a listed route for a setup session ---

// wizardSettingKeys are the only settings the wizard writes: each media server's family
// sign-in switches (SetupWizard.saveMedia).
var wizardSettingKeys = map[string]bool{
	"plex_login_enabled":                true,
	"plex_login_auto_approve_types":     true,
	"jellyfin_login_enabled":            true,
	"jellyfin_login_auto_approve_types": true,
}

// wizardKeyIDs are the credentials the wizard's first step saves and tests.
var wizardKeyIDs = map[string]bool{
	"tmdb": true, "hardcover": true, "tvdb": true, "omdb": true,
	"opensubtitles_api": true, "opensubtitles_username": true, "opensubtitles_password": true,
}

// quietWriter swaps a success body for {"status":"ok"}: the answers to the settings and key
// saves carry the whole settings view and every key's hint, which a setup session (nobody
// signed in) has no business reading. Errors pass through: they hold messages only.
type quietWriter struct {
	http.ResponseWriter
	code int
}

func (q *quietWriter) WriteHeader(code int) {
	if q.code == 0 {
		q.code = code
	}
	if code >= 400 {
		q.ResponseWriter.WriteHeader(code)
	}
}

func (q *quietWriter) Write(b []byte) (int, error) {
	if q.code == 0 {
		q.code = http.StatusOK
	}
	if q.code >= 400 {
		return q.ResponseWriter.Write(b)
	}
	return len(b), nil
}

func (q *quietWriter) done(a *api) {
	if q.code == 0 || q.code < 400 {
		a.writeJSON(q.ResponseWriter, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// readBody reads (up to 64 KB of) a request body and puts it back for the handler.
func readBody(r *http.Request) ([]byte, bool) {
	const max = 64 << 10
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil || len(b) > max {
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, true
}

// wizardSettings narrows PUT /api/v1/settings for a setup session to wizardSettingKeys,
// and answers without the settings view.
func (a *api) wizardSettings(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !viaSetupSession(r) {
			h(w, r)
			return
		}
		b, ok := readBody(r)
		var m map[string]json.RawMessage
		if !ok || json.Unmarshal(b, &m) != nil {
			a.writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		for k := range m {
			if !wizardSettingKeys[k] {
				a.writeError(w, http.StatusForbidden, "Setup can't change that setting yet. Finish setup first, then find it in Settings.")
				return
			}
		}
		q := &quietWriter{ResponseWriter: w}
		h(q, r)
		q.done(a)
	}
}

// wizardKeyID narrows the credential routes for a setup session to wizardKeyIDs and answers
// a save without the keys' hints.
func (a *api) wizardKeyID(h http.HandlerFunc, save bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !viaSetupSession(r) {
			h(w, r)
			return
		}
		if !wizardKeyIDs[r.PathValue("id")] {
			a.writeError(w, http.StatusForbidden, "Setup can't change that key yet. Finish setup first, then find it in Settings.")
			return
		}
		if !save {
			h(w, r)
			return
		}
		q := &quietWriter{ResponseWriter: w}
		h(q, r)
		q.done(a)
	}
}

// setupPathRoots are the folders a setup session may look into or point a library at: the
// mounts a container install sees media under, and the folders the install already names
// (the .env defaults, which is all a non-Docker install has) with the one above each.
// Anything else waits for an admin: Settings → Library has no such limit.
func (a *api) setupPathRoots(ctx context.Context) []string {
	var roots []string
	for _, m := range candidateMounts {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			roots = append(roots, m)
		}
	}
	c := a.pickedConfig(ctx)
	for _, d := range libraryDirSettings(&c) {
		p := strings.TrimSpace(*d.field)
		if p != "" && !filepath.IsAbs(p) {
			// A plain (non-Docker) install keeps the defaults relative to where it runs.
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
		}
		if p != "" && filepath.IsAbs(p) {
			p = filepath.Clean(p)
			roots = append(roots, p)
			// The folder above it, so the picker can step up to a sibling; never "/".
			if up := filepath.Dir(p); up != "/" && up != "." {
				roots = append(roots, up)
			}
		}
	}
	return roots
}

// underAny reports whether p is one of roots or inside one (after cleaning, so "..",
// doubled slashes and trailing slashes can't step out).
func underAny(p string, roots []string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	for _, root := range roots {
		if p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/") {
			return true
		}
	}
	return false
}

const errOutsideWizardRoots = "While setting up, folders have to be under /storage or /media (or the folders named in .env). Finish setup first, then pick any other folder in Settings → Library."

// wizardPaths narrows the folder routes for a setup session to setupPathRoots: the browse
// and check queries (path, downloads) and the folders a save names. Without a path, browse
// starts at the first root.
func (a *api) wizardPaths(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !viaSetupSession(r) {
			h(w, r)
			return
		}
		roots := a.setupPathRoots(r.Context())
		if len(roots) == 0 {
			a.writeError(w, http.StatusForbidden, errOutsideWizardRoots)
			return
		}
		if r.Method == http.MethodGet {
			q := r.URL.Query()
			if r.URL.Path == "/api/v1/system/browse" && strings.TrimSpace(q.Get("path")) == "" {
				q.Set("path", roots[0])
			}
			for _, k := range []string{"path", "downloads"} {
				if v := strings.TrimSpace(q.Get(k)); v != "" && !underAny(v, roots) {
					a.writeError(w, http.StatusForbidden, errOutsideWizardRoots)
					return
				}
			}
			r.URL.RawQuery = q.Encode()
			h(w, r)
			return
		}
		b, ok := readBody(r)
		var m map[string]json.RawMessage
		if !ok || json.Unmarshal(b, &m) != nil {
			a.writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		for k, raw := range m {
			if k == "create" {
				continue
			}
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				a.writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if v = strings.TrimSpace(v); v != "" && !underAny(v, roots) {
				a.writeError(w, http.StatusForbidden, errOutsideWizardRoots)
				return
			}
		}
		h(w, r)
	}
}
