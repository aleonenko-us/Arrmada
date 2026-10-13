package httpapi

import (
	"context"
	"encoding/json"
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

// Importing TRaSH JSON answers the converted formats and saves nothing; JSON that isn't a
// custom format is a 400.
func TestImportTrash(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	before, err := s.deps.Quality.ListStored(context.Background(), quality.MediaMovie)
	if err != nil {
		t.Fatal(err)
	}
	cf := `{"name":"x265 (HD)","trash_scores":{"default":-10000},"specifications":[{"name":"x265","implementation":"ReleaseTitleSpecification","negate":false,"required":true,"fields":{"value":"[xh][ ._-]?265"}}]}`
	body, _ := json.Marshal(map[string]string{"json": cf, "media": "movie"})
	rec := s.doBody("POST", "/api/v1/quality/import-trash", string(body), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got quality.TrashImport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Formats) != 1 || got.Formats[0].Format.Name != "x265 (HD)" || got.Formats[0].SuggestedScore == nil {
		t.Errorf("import = %+v", got)
	}
	after, _ := s.deps.Quality.ListStored(context.Background(), quality.MediaMovie)
	if len(after) != len(before) {
		t.Error("an import saved a profile")
	}
	if rec := s.doBody("POST", "/api/v1/quality/import-trash", `{"json":"not json","media":"movie"}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: HTTP %d", rec.Code)
	}
}

// The editor's live check answers per-format errors and sample matches; the profile list
// carries the vocabulary the editor's dropdowns are built from.
func TestCheckFormatsAndVocabulary(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, member := s.user(t, "member@example.com", auth.RoleRequester)
	body := `{"custom_formats":[{"name":"Remux","conditions":[{"type":"source","value":"remux"}]},{"name":"Bad","match":"any","conditions":[{"type":"release_title","value":"[x"}]}]}`
	if rec := s.doBody("POST", "/api/v1/quality/formats/check", body, member); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d", rec.Code)
	}
	rec := s.doBody("POST", "/api/v1/quality/formats/check", body, mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got quality.FormatReport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Matches) != 2 || got.Matches[0] != 1 || len(got.Errors) != 1 || got.Errors[0].Format != "Bad" {
		t.Errorf("check = %+v", got)
	}

	rec = s.do("GET", "/api/v1/quality/profiles?media=movie", mgr)
	var list struct {
		Vocabulary map[string][]quality.VocabValue `json:"vocabulary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Vocabulary["codec"]) == 0 || len(list.Vocabulary["source"]) == 0 {
		t.Errorf("vocabulary = %v", list.Vocabulary)
	}
}

// Release-group tiers save and come back; a group in both tiers, or one that's also a
// low-quality group, is a 400. The profile list carries the suggested tiers.
func TestQualityProfileGroupTiers(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) { d.Quality = quality.NewService(d.Store.DB()) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	for _, bad := range []string{
		`{"name":"T","media_type":"movie","format_scores":{},"group_tiers":{"tier1":["FLUX"],"tier2":["flux"]}}`,
		`{"name":"T","media_type":"movie","format_scores":{},"group_tiers":{"tier1":["YTS"],"tier2":[]}}`,
	} {
		if rec := s.doBody("POST", "/api/v1/quality/profiles", bad, mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d: %s", bad, rec.Code, rec.Body)
		}
	}
	rec := s.doBody("POST", "/api/v1/quality/profiles", `{"name":"T","media_type":"movie","format_scores":{},"group_tiers":{"tier1":["ALPHA"],"tier2":[]}}`, mgr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var sp quality.StoredProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	if sp.GroupTiers == nil || len(sp.GroupTiers.Tier1) != 1 || sp.GroupTiers.Tier1[0] != "ALPHA" {
		t.Errorf("tiers = %+v", sp.GroupTiers)
	}
	rec = s.do("GET", "/api/v1/quality/profiles?media=movie", mgr)
	var list struct {
		GroupTiers quality.GroupTiers `json:"group_tiers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.GroupTiers.Tier1) == 0 {
		t.Errorf("suggested tiers = %+v, %v", list.GroupTiers, err)
	}
}
