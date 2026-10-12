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

// handleImportTrash converts pasted TRaSH custom-format JSON into Arrmada formats, with a
// warning for everything that couldn't be carried over. Nothing is saved: the editor adds
// the chosen formats to the profile being edited, and saving the profile keeps them.
func (a *api) handleImportTrash(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JSON  string `json:"json"`
		Media string `json:"media"` // movie (Radarr's numbering) or series (Sonarr's)
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	out, err := quality.ImportTrashCF([]byte(req.JSON), req.Media)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, out)
}
