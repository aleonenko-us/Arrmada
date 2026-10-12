package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLanguagesHandler(t *testing.T) {
	a := &api{deps: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	rec := httptest.NewRecorder()
	a.handleLanguages(rec, httptest.NewRequest(http.MethodGet, "/api/v1/languages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 185 {
		t.Errorf("%d languages, want the whole table", len(rows))
	}
	byCode := map[string]map[string]any{}
	for _, r := range rows {
		if len(r) != 3 {
			t.Fatalf("row %v: want exactly code, name and regional", r)
		}
		byCode[r["code"].(string)] = r
	}
	if r := byCode["pt-br"]; r == nil || r["name"] != "Portuguese (Brazil)" || r["regional"] != true {
		t.Errorf("pt-br = %v", r)
	}
	if r := byCode["fr"]; r == nil || r["name"] != "French" || r["regional"] != false {
		t.Errorf("fr = %v", r)
	}
}
