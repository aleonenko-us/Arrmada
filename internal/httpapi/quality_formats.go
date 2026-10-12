package httpapi

import (
	"net/http"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// handleCheckFormats is the custom-format editor's live check: the profile's formats as
// edited, unsaved, answered with what would block a save (an invalid pattern, a missing
// value), what can never match, and how many of the sample releases each one matches.
func (a *api) handleCheckFormats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomFormats []quality.CustomFormat `json:"custom_formats"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	a.writeJSON(w, http.StatusOK, quality.CheckFormats(req.CustomFormats))
}
