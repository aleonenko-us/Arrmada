package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The shadow-mode record reads back as JSON and downloads as CSV.
func TestRankingShadowReport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := settings.NewService(st.DB())
	q := quality.NewServiceWith(st.DB(), set)
	a := &api{deps: Deps{Settings: set, Quality: q}}
	ctx := t.Context()
	sp, err := q.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "p", AllowedResolutions: []string{"2160p", "1080p"},
		Ideal: &quality.IdealFile{Codec: map[string]string{"hevc": quality.PrefWant}, HDR: map[string]string{"HDR10": quality.PrefWant, "HDR10+": quality.PrefWant},
			Audio: map[string]string{"atmos": quality.PrefWant}}})
	if err != nil {
		t.Fatal(err)
	}
	q.Decide(ctx, "custom:"+strconv.FormatInt(sp.ID, 10), []quality.Candidate{
		quality.NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50),
		quality.NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50),
	})

	w := httptest.NewRecorder()
	a.handleRankingShadow(w, httptest.NewRequest(http.MethodGet, "/api/v1/quality/ranking-shadow", nil))
	var rep quality.ShadowReport
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &rep) != nil {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	// v2 decides by default and classic runs in the shadow; the row is the same pair of
	// picks it would be the other way round.
	if rep.Mode != "v2" || rep.Decisions != 1 || rep.Disagreements != 1 || len(rep.Rows) != 1 || rep.Rows[0].V2Key != "resolution" ||
		rep.Rows[0].V2Winner != "Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD" || rep.Rows[0].V1Winner != "Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD" {
		t.Fatalf("report = %+v", rep)
	}

	w = httptest.NewRecorder()
	a.handleRankingShadow(w, httptest.NewRequest(http.MethodGet, "/api/v1/quality/ranking-shadow?format=csv", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(w.Body.String(), "Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD") {
		t.Fatalf("csv: HTTP %d %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}

	w = httptest.NewRecorder()
	a.handleRankingShadow(w, httptest.NewRequest(http.MethodGet, "/api/v1/quality/ranking-shadow?limit=-1", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: HTTP %d", w.Code)
	}
}

// quality_ranking takes only v1 or v2, reads v2 until something is saved, and an explicit
// v1 (the rollback) sticks.
func TestQualityRankingSettingValidated(t *testing.T) {
	a := settingsAPI(t)
	if w := putSettings(t, a, `{"quality_ranking":"v3"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("v3: HTTP %d", w.Code)
	}
	_, got := getSettings(t, a)
	if got["quality_ranking"] != "v2" {
		t.Fatalf("default = %v", got["quality_ranking"])
	}
	// It changes what every profile grabs: the owner's call, not a manager's.
	if w := putSettingsAs(t, a, auth.RoleManager, `{"quality_ranking":"v1"}`); w.Code != http.StatusForbidden {
		t.Fatalf("manager: HTTP %d", w.Code)
	}
	// Sending it back unchanged is fine for a manager (the page saves whole objects).
	if w := putSettingsAs(t, a, auth.RoleManager, `{"quality_ranking":"v2"}`); w.Code != http.StatusOK {
		t.Fatalf("manager, unchanged: HTTP %d", w.Code)
	}
	// The rollback: an admin saves v1 and it reads back as v1.
	if w := putSettings(t, a, `{"quality_ranking":"v1"}`); w.Code != http.StatusOK {
		t.Fatalf("v1: HTTP %d", w.Code)
	}
	if _, got := getSettings(t, a); got["quality_ranking"] != "v1" {
		t.Fatalf("after saving v1 = %v", got["quality_ranking"])
	}
}
