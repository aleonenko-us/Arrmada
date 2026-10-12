package metadata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// hcReleaseServer answers the one-book query; with refuse set it rejects any query that
// asks for release_date, the way Hasura rejects an unknown field, and counts queries.
func hcReleaseServer(t *testing.T, book string, refuse bool) (*Hardcover, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		seen = append(seen, req.Query)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if refuse && strings.Contains(req.Query, "release_date") {
			_, _ = io.WriteString(w, `{"errors":[{"message":"field 'release_date' not found in type: 'books'"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"books":[`+book+`]}}`)
	}))
	t.Cleanup(srv.Close)
	h := NewHardcoverFunc(func() string { return "testtoken" }, nil)
	h.endpoint = srv.URL
	return h, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

// GetBook carries the book's release date, as YYYY-MM-DD, into BookResult and
// BookDetails. [BOOK-18]
func TestHardcoverGetBookReleaseDate(t *testing.T) {
	h, seen := hcReleaseServer(t, `{"id":7,"title":"Wind and Truth","release_year":2027,"release_date":"2027-03-12",
		"contributions":[{"contribution":null,"author":{"id":1,"name":"Brandon Sanderson"}}]}`, false)
	d, err := h.GetBook(context.Background(), "hc:7")
	if err != nil {
		t.Fatal(err)
	}
	if d.ReleaseDate != "2027-03-12" || d.BookResult.ReleaseDate != "2027-03-12" || d.Year != 2027 {
		t.Errorf("release date = %q, year %d", d.ReleaseDate, d.Year)
	}
	if q := seen(); len(q) != 1 || !strings.Contains(q[0], "release_date") {
		t.Errorf("queries = %q, want one asking for release_date", q)
	}
}

// A missing or odd release date is no date, never an error.
func TestHardcoverReleaseDateDefensive(t *testing.T) {
	for raw, want := range map[string]string{
		`null`: "", `"2027"`: "", `"garbage"`: "", `"2027-03-12T00:00:00+00:00"`: "2027-03-12", `"2027-13-40"`: "",
	} {
		h, _ := hcReleaseServer(t, `{"id":7,"title":"X","release_date":`+raw+`}`, false)
		d, err := h.GetBook(context.Background(), "hc:7")
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if d.ReleaseDate != want {
			t.Errorf("release_date %s → %q, want %q", raw, d.ReleaseDate, want)
		}
	}
	h, _ := hcReleaseServer(t, `{"id":7,"title":"No Date Field"}`, false)
	if d, err := h.GetBook(context.Background(), "hc:7"); err != nil || d.ReleaseDate != "" || d.Title != "No Date Field" {
		t.Errorf("no field: %+v, %v", d, err)
	}
}

// If the live schema refuses release_date, books still load — without dates — and the
// field isn't asked for again.
func TestHardcoverReleaseDateRefusedFallsBack(t *testing.T) {
	h, seen := hcReleaseServer(t, `{"id":7,"title":"Dune"}`, true)
	d, err := h.GetBook(context.Background(), "hc:7")
	if err != nil || d.Title != "Dune" {
		t.Fatalf("GetBook = %+v, %v; want the book without a date", d, err)
	}
	if q := seen(); len(q) != 2 || strings.Contains(q[1], "release_date") {
		t.Fatalf("queries = %d, want the refused one then one without the field", len(q))
	}
	if _, err := h.GetBook(context.Background(), "hc:8"); err != nil {
		t.Fatal(err)
	}
	if q := seen(); len(q) != 3 || strings.Contains(q[2], "release_date") {
		t.Errorf("after a refusal the field must not be asked for again (%d queries)", len(q))
	}
}
