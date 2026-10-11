package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/convert"
)

// Convert's originals hold: releasing what it keeps (deletes originals for good) and
// reverting a conversion from it. Manager-only like the rest of Convert; the page confirms
// each first.

// handleConvertHoldRelease — POST /api/v1/convert/history/{id}/release: delete one held
// original now and count its space as saved. Answers the updated ledger row.
func (a *api) handleConvertHoldRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathValueID(w, r, "id")
	if !ok {
		return
	}
	e, err := a.deps.Convert.ReleaseHold(r.Context(), id)
	if a.convertHoldError(w, err, "could not release the original") {
		return
	}
	a.writeJSON(w, http.StatusOK, e)
}

// handleConvertHoldReleaseOlder — POST /api/v1/convert/history/release?older_than_days=N:
// release every held original converted at least N days ago (0 = all of them).
func (a *api) handleConvertHoldReleaseOlder(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("older_than_days"))
	if err != nil || days < 0 {
		a.writeError(w, http.StatusBadRequest, "pass older_than_days=N (0 or more)")
		return
	}
	n, freed, err := a.deps.Convert.ReleaseHoldsOlderThan(r.Context(), days)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not release the originals")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"released": n, "freed_bytes": freed})
}

// handleConvertRevert — POST /api/v1/convert/history/{id}/revert: put the conversion's
// original back from the hold in place of the converted file. Answers the updated row.
// Refused (409, with the reason in words) when the library file has changed since, or the
// file is being converted right now.
func (a *api) handleConvertRevert(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathValueID(w, r, "id")
	if !ok {
		return
	}
	e, err := a.deps.Convert.Revert(r.Context(), id)
	if a.convertHoldError(w, err, "could not revert the conversion") {
		return
	}
	a.writeJSON(w, http.StatusOK, e)
}

// convertHoldError answers a hold action's error (true when there was one).
func (a *api) convertHoldError(w http.ResponseWriter, err error, fallback string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, convert.ErrNoHistory):
		a.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, convert.ErrNotHeld), errors.Is(err, convert.ErrSwapBusy),
		errors.Is(err, convert.ErrRevertChanged), errors.Is(err, convert.ErrRevertBusy):
		a.writeError(w, http.StatusConflict, err.Error())
	default:
		a.writeError(w, http.StatusInternalServerError, fallback+": "+err.Error())
	}
	return true
}
