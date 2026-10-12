package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// Saving a profile whose custom format has a pattern that doesn't compile is a 400 that
// names the format, on create and on update, and nothing is written.
func TestSaveQualityProfileRefusesInvalidFormat(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	bad := `{"name":"Mine","media_type":"movie","format_scores":{},"custom_formats":[{"name":"Tier one","conditions":[{"type":"release_title","value":"(FLUX"}]}]}`
	rec := s.doBody("POST", "/api/v1/quality/profiles", bad, mgr)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Tier one") {
		t.Fatalf("create: HTTP %d: %s", rec.Code, rec.Body)
	}

	sp, err := s.deps.Quality.Create(context.Background(), quality.StoredProfile{MediaType: quality.MediaMovie, Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	rec = s.doBody("PUT", fmt.Sprintf("/api/v1/quality/profiles/%d", sp.ID), bad, mgr)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Tier one") {
		t.Fatalf("update: HTTP %d: %s", rec.Code, rec.Body)
	}
	got, err := s.deps.Quality.GetStored(context.Background(), fmt.Sprintf("custom:%d", sp.ID))
	if err != nil || len(got.CustomFormats) != 0 {
		t.Errorf("a refused update was written: %+v, %v", got.CustomFormats, err)
	}

	good := strings.Replace(bad, `(FLUX`, `\\bFLUX\\b`, 1)
	if rec := s.doBody("PUT", fmt.Sprintf("/api/v1/quality/profiles/%d", sp.ID), good, mgr); rec.Code != http.StatusOK {
		t.Fatalf("valid update: HTTP %d: %s", rec.Code, rec.Body)
	}
}
