package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// recheckTMDB re-runs the health panel's TMDB key check after the key changed or was
// tested, so a fixed key clears the warning now instead of at the next six-hourly check.
func (a *api) recheckTMDB(r *http.Request) { a.recheckHealth(r, "tmdb.key") }

// lastErrorer is a provider that keeps its last complaint about the key (OMDb).
type lastErrorer interface {
	LastError() (string, time.Time)
}

// keyStatuses is every credential's state, with the provider's last error added where it
// keeps one: a spent OMDb quota otherwise only showed as ratings quietly missing.
func (a *api) keyStatuses(ctx context.Context) []apikeys.KeyStatus {
	keys := a.deps.APIKeys.Status(ctx)
	if le, ok := a.deps.Ratings.(lastErrorer); ok {
		for i := range keys {
			if keys[i].ID != "omdb" {
				continue
			}
			if msg, at := le.LastError(); msg != "" {
				keys[i].LastError, keys[i].LastErrorAt = msg, &at
			}
		}
	}
	return keys
}

// handleGetAPIKeys returns the state of every credential — configured or not, from where,
// and a short hint — but never a secret itself.
func (a *api) handleGetAPIKeys(w http.ResponseWriter, r *http.Request) {
	if a.deps.APIKeys == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"keys": []any{}})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"keys": a.keyStatuses(r.Context())})
}

// handleTestAPIKey makes a real request with a key and reports the outcome. With no body
// it tests the saved key; with {"value": "..."} it tests that candidate instead — a key
// typed and not yet saved — which is sent to the provider once and never stored or
// logged. Only keys with a verifier wired up can be tested; anything else is a 400.
func (a *api) handleTestAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Value string `json:"value"`
	}
	// The body is optional: the Test button on a saved key sends none.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		a.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	candidate := strings.TrimSpace(body.Value)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := a.keyTest(ctx, id, candidate)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A test of the saved TMDB key settles the health panel's TMDB warning either way.
	// A candidate says nothing about the key in use, so it leaves the panel alone.
	if id == "tmdb" && candidate == "" {
		a.recheckTMDB(r)
	}
	if candidate == "" {
		a.recordKeyTest(ctx, id, res.Err)
	}
	if res.Err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": res.Err.Error()})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": res.Detail})
}

// keyTestResult is one key Test's answer: Detail when it passed, Err (in words) when not.
type keyTestResult struct {
	Detail string
	Err    error
}

// errNoKeyTest is a key with no test wired up.
var errNoKeyTest = errors.New("no test is available for that key")

// keyTest makes a real request with a key — the saved one, or candidate when it isn't
// empty — and reports what came back. The error is the request itself being wrong (no test
// for that key, a candidate where only the saved value can be tested); a key that fails
// is a result, not an error.
func (a *api) keyTest(ctx context.Context, id, candidate string) (keyTestResult, error) {
	var res keyTestResult
	switch verify, ok := a.deps.KeyVerifiers[id]; {
	case id == "hardcover":
		if candidate != "" {
			return res, errors.New("Hardcover can only test the saved key; save it, then Test")
		}
		if a.deps.Books == nil {
			return res, errNoKeyTest
		}
		res.Detail, res.Err = a.deps.Books.VerifyHardcover(ctx)
	case id == "flaresolverr":
		if candidate != "" {
			return res, errors.New("FlareSolverr can only test the saved URL; save it, then Test")
		}
		res.Detail, res.Err = testFlareSolverr(ctx, a.deps.FlareSolverr)
	case ok:
		res.Detail, res.Err = verify(ctx, candidate)
		if errors.Is(res.Err, metadata.ErrNotConfigured) || errors.Is(res.Err, subtitles.ErrNotConfigured) {
			res.Err = errors.New("no key is set to test")
		}
	case id == "tmdb" && candidate == "":
		// No verifier wired: fall back to the health check's own TMDB validation.
		v, ok := a.deps.Discovery.(tmdbValidator)
		if !ok {
			return res, errNoKeyTest
		}
		switch res.Err = tmdbValidate(ctx, v); {
		case res.Err == nil:
			res.Detail = "TMDB accepted the key."
		case errors.Is(res.Err, health.ErrKeyRejected):
			res.Err = errors.New("TMDB rejected the key — check it's the v3 API key, copied in full")
		case errors.Is(res.Err, health.ErrKeyMissing):
			res.Err = errors.New("no TMDB key is set")
		}
	default:
		return res, errNoKeyTest
	}
	return res, nil
}

// recordKeyTest notes a Test of a saved key in the status tracker, so its card on
// Settings → Connections shows when it last worked or why it failed. A key that isn't set
// has nothing to record (FlareSolverr records its own pings).
func (a *api) recordKeyTest(ctx context.Context, id string, err error) {
	if a.deps.ConnStatus == nil || a.deps.APIKeys == nil || id == "flaresolverr" || ctx.Err() != nil {
		return
	}
	if !a.keyConfigured(ctx, id) {
		return
	}
	a.deps.ConnStatus.Record(connKindAPIKey, id, connstatus.Outcome{Err: err, Name: id})
}

// keyConfigured reports whether a credential has a value (saved or from the install).
func (a *api) keyConfigured(ctx context.Context, id string) bool {
	return a.deps.APIKeys != nil && a.deps.APIKeys.Value(ctx, id) != ""
}

// resetKeyStatus forgets a key's recorded Tests after it changed: they said nothing about
// the new value. The OpenSubtitles login is tested through the API key's Test.
// Called after every save and clear.
func (a *api) resetKeyStatus(ctx context.Context, id string) {
	// Subtitles forgets a rejected login and lets the next sweep retry what it parked.
	if strings.HasPrefix(id, "opensubtitles_") && a.deps.Subtitles != nil {
		a.deps.Subtitles.CredentialsChanged(ctx)
	}
	if a.deps.ConnStatus == nil {
		return
	}
	switch id {
	case "flaresolverr":
		_ = a.deps.ConnStatus.Reset(ctx, connstatus.KindFlareSolverr, flareSolverrRef)
	case "opensubtitles_username", "opensubtitles_password":
		_ = a.deps.ConnStatus.Reset(ctx, connKindAPIKey, "opensubtitles_api")
	default:
		_ = a.deps.ConnStatus.Reset(ctx, connKindAPIKey, id)
	}
}

// handleSetAPIKey saves one credential. A blank value is refused: clearing a key is its own
// confirmed DELETE, so an empty Save can never wipe a working key by accident.
func (a *api) handleSetAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing key id")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if a.deps.APIKeys == nil {
		a.writeError(w, http.StatusInternalServerError, "key store unavailable")
		return
	}
	// The mail credentials have their own card and checks (/settings/smtp).
	if !apikeys.Listed(id) {
		a.writeError(w, http.StatusNotFound, "unknown key")
		return
	}
	switch err := a.deps.APIKeys.Set(r.Context(), id, req.Value); {
	case errors.Is(err, apikeys.ErrEmptyValue):
		a.writeError(w, http.StatusBadRequest, "value is required — use Clear to remove a key")
		return
	case errors.Is(err, apikeys.ErrUnknownKey):
		a.writeError(w, http.StatusNotFound, "unknown key")
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not save the key")
		return
	}
	a.resetKeyStatus(r.Context(), id)
	if id == "tmdb" {
		a.recheckTMDB(r)
	}
	// A Hardcover key makes Hardcover the books catalogue; bring the library across now.
	if id == "hardcover" && strings.TrimSpace(req.Value) != "" && a.deps.Books != nil {
		a.deps.Books.MaybeSubmitUpgrade(a.runCtx(), a.jobSubmitter(), triggerFor(r))
	}
	// Return the fresh status so the UI reflects the new state (masked) without a reload.
	a.writeJSON(w, http.StatusOK, map[string]any{"keys": a.keyStatuses(r.Context())})
}

// handleClearAPIKey removes a saved credential, so it falls back to the install-time env
// var if there is one. Clearing Hardcover leaves books on the ids they already have; new
// lookups go back to Open Library.
func (a *api) handleClearAPIKey(w http.ResponseWriter, r *http.Request) {
	if a.deps.APIKeys == nil {
		a.writeError(w, http.StatusInternalServerError, "key store unavailable")
		return
	}
	if !apikeys.Listed(r.PathValue("id")) {
		a.writeError(w, http.StatusNotFound, "unknown key")
		return
	}
	switch err := a.deps.APIKeys.Clear(r.Context(), r.PathValue("id")); {
	case errors.Is(err, apikeys.ErrUnknownKey):
		a.writeError(w, http.StatusNotFound, "unknown key")
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not clear the key")
		return
	}
	a.resetKeyStatus(r.Context(), r.PathValue("id"))
	if r.PathValue("id") == "tmdb" {
		a.recheckTMDB(r)
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"keys": a.keyStatuses(r.Context())})
}
