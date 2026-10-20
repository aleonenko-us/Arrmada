package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
	"github.com/tristenlammi/arrmada/internal/updater"
)

// Values of updatecheck.Status.Apply.
const (
	applyReady      = "ready"
	applyNeedsSetup = "needs_setup"
)

// withApply adds what only the API knows to the update check's answer: whether the app
// can install the update itself (an updater container is set up) and whether automatic
// updates are on.
func (a *api) withApply(ctx context.Context, st updatecheck.Status) updatecheck.Status {
	switch {
	case st.Host == updatecheck.HostUnraid:
		st.Apply = "" // Unraid's Docker page updates it
	case a.deps.Updater.Available():
		st.Apply = applyReady
	default:
		st.Apply = applyNeedsSetup
	}
	if a.deps.Settings != nil {
		st.AutoUpdate = a.deps.Settings.GetBool(ctx, updater.KeyAutoEnabled, false)
	}
	return st
}

// updateStatus is the "update" block of /system/status, or nil when no check is wired.
func (a *api) updateStatus(ctx context.Context) *updatecheck.Status {
	if a.deps.Updates == nil {
		return nil
	}
	st := a.withApply(ctx, a.deps.Updates.Status(ctx))
	return &st
}

// handleSetUpdateCheck — PUT /api/v1/system/update-check {enabled} (admin): switches the
// check on or off. While it's off Arrmada asks GitHub nothing at all.
func (a *api) handleSetUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil || a.deps.Settings == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the update check isn't available")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		a.writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	ctx := r.Context()
	if err := a.deps.Settings.SetBool(ctx, updatecheck.KeyEnabled, *req.Enabled); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	a.writeJSON(w, http.StatusOK, a.withApply(ctx, a.deps.Updates.Status(ctx)))
}

// handleUpdateCheckNow — POST /api/v1/system/update-check (admin): asks GitHub now
// (at most once a minute; sooner returns the last answer) and answers with the result.
func (a *api) handleUpdateCheckNow(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the update check isn't available")
		return
	}
	if !a.deps.Updates.Enabled(r.Context()) {
		a.writeError(w, http.StatusConflict, "The update check is switched off. Switch it on to check.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	a.writeJSON(w, http.StatusOK, a.withApply(ctx, a.deps.Updates.CheckNow(ctx)))
}

// handleUpdateApply — POST /api/v1/system/update/apply (admin): asks the updater container
// to install the newer release. It answers 202 as soon as the updater has started: the app
// is restarted part-way through, so the page follows it with /update/progress and then
// by asking the app's health until it answers with the new version.
func (a *api) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the update check isn't available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st := a.withApply(ctx, a.deps.Updates.Status(ctx))
	switch {
	case st.Apply == "":
		a.writeError(w, http.StatusConflict, "This container is updated from Unraid's Docker page, not from here.")
		return
	case st.Apply != applyReady:
		a.writeError(w, http.StatusConflict, "One-click updates aren't set up on this server. Run the install line with --with-updater once to turn them on.")
		return
	case !st.Enabled || !st.Available:
		a.writeError(w, http.StatusConflict, "There's no newer release to install.")
		return
	}
	err := a.deps.Updater.Apply(ctx)
	switch {
	case errors.Is(err, updater.ErrBusy):
		a.writeError(w, http.StatusConflict, "An update is already running.")
		return
	case err != nil:
		a.deps.Log.Warn("update: the updater wouldn't start it", "err", err)
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	tag := ""
	if st.Latest != nil {
		tag = st.Latest.Tag
	}
	a.deps.Log.Info("update: started from the web", "from", st.Current, "to", tag)
	a.writeJSON(w, http.StatusAccepted, updater.Progress{State: updater.StateRunning})
}

// handleUpdateProgress — GET /api/v1/system/update/progress (admin): how the last or
// running update is going, as the updater container reports it.
func (a *api) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	if !a.deps.Updater.Available() {
		a.writeError(w, http.StatusConflict, "One-click updates aren't set up on this server.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p, err := a.deps.Updater.Progress(ctx)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

// handleSetAutoUpdate — PUT /api/v1/system/update/auto {enabled} (admin): switches the
// automatic updates on or off. Off by default, and only on with an updater to do it.
func (a *api) handleSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	if a.deps.Updates == nil || a.deps.Settings == nil {
		a.writeError(w, http.StatusServiceUnavailable, "the update check isn't available")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		a.writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	ctx := r.Context()
	if *req.Enabled && !a.deps.Updater.Available() {
		a.writeError(w, http.StatusConflict, "Automatic updates need the updater. Run the install line with --with-updater once to set it up.")
		return
	}
	if err := a.deps.Settings.SetBool(ctx, updater.KeyAutoEnabled, *req.Enabled); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	a.writeJSON(w, http.StatusOK, a.withApply(ctx, a.deps.Updates.Status(ctx)))
}
