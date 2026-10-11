package httpapi

import (
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/convert"
)

// handleConvertPlan — GET /api/v1/convert/plan?key=: one file's conversion plan (what the
// picture becomes, every track kept or removed and why, the warnings), from the index.
// Manager-only like the rest of Convert.
func (a *api) handleConvertPlan(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		a.writeError(w, http.StatusBadRequest, "pass key=")
		return
	}
	v, err := a.deps.Convert.PlanView(r.Context(), key)
	switch {
	case errors.Is(err, convert.ErrNotIndexed):
		a.writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, convert.ErrInvalidItem):
		a.writeError(w, http.StatusBadRequest, "invalid item")
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not build the conversion plan")
		return
	}
	a.writeJSON(w, http.StatusOK, v)
}
