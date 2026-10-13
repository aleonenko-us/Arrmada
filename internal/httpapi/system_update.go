package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/tristenlammi/arrmada/internal/updatecheck"
)

// updateStatus is the "update" block of /system/status, or nil when no check is wired.
func (a *api) updateStatus(ctx context.Context) *updatecheck.Status {
	if a.deps.Updates == nil {
		return nil
	}
	st := a.deps.Updates.Status(ctx)
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
	a.writeJSON(w, http.StatusOK, a.deps.Updates.Status(ctx))
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
	a.writeJSON(w, http.StatusOK, a.deps.Updates.CheckNow(ctx))
}
