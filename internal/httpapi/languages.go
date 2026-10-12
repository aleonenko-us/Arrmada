package httpapi

import (
	"net/http"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// languageRow is one entry of the shared language table as the UI's pickers see it.
type languageRow struct {
	Code     string `json:"code"`     // canonical: "fr", "pt-br"
	Name     string `json:"name"`     // "French", "Portuguese (Brazil)"
	Regional bool   `json:"regional"` // a regional variant of the language before the hyphen
}

// handleLanguages returns the language table (internal/langs) — static, read-only data —
// so every picker and chip names languages the same way the server matches them.
func (a *api) handleLanguages(w http.ResponseWriter, r *http.Request) {
	all := langs.All()
	out := make([]languageRow, 0, len(all))
	for _, l := range all {
		out = append(out, languageRow{Code: l.Code, Name: l.Name, Regional: l.Regional})
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	a.writeJSON(w, http.StatusOK, out)
}
