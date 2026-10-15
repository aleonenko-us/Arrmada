package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

// handleRankingShadow is the shadow-mode record for review: per-day counts of the
// decisions that had a choice to make and how many the two rankings (v2, the default, and
// classic v1, the rollback) would have picked differently, and each disagreement (most
// recently seen first) — recorded whichever of the two decides. ?limit= caps the rows
// (default 500); ?format=csv downloads every disagreement as a spreadsheet. Admin-only: it
// is what the choice between the two rankings rests on.
func (a *api) handleRankingShadow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="ranking-v2-shadow-`+time.Now().UTC().Format("2006-01-02")+`.csv"`)
		if err := a.deps.Quality.WriteShadowCSV(ctx, w); err != nil && a.deps.Log != nil {
			a.deps.Log.Warn("quality: ranking shadow export failed", "err", err)
		}
		return
	}
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			a.writeError(w, http.StatusBadRequest, "limit must be a whole number (0 = all)")
			return
		}
		limit = n
	}
	rep, err := a.deps.Quality.ShadowReport(ctx, limit)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the ranking comparison")
		return
	}
	a.writeJSON(w, http.StatusOK, rep)
}
