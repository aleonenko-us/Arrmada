package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
)

// The series page asks for its own held downloads only: ?media_type=series&expected_id=N
// returns that show's reviews and nothing of another show or of a movie with the same id.
func TestListReviewsFilterByExpectedSeries(t *testing.T) {
	a, st := reviewAPI(t)
	for _, row := range []struct {
		hash, kind string
		id         int
	}{{"a1", "series", 1}, {"a2", "series", 2}, {"a3", "movie", 1}, {"a4", "series", 1}} {
		if _, err := st.DB().Exec(`INSERT INTO import_reviews (hash, name, content_path, media_type, expected_id, reason)
			VALUES (?, ?, ?, ?, ?, 'held')`, row.hash, "Name "+row.hash, t.TempDir(), row.kind, row.id); err != nil {
			t.Fatal(err)
		}
	}
	list := func(query string) (int, []automation.Review) {
		rec := httptest.NewRecorder()
		a.handleListReviews(rec, httptest.NewRequest(http.MethodGet, "/api/v1/reviews"+query, nil))
		var body struct {
			Reviews []automation.Review `json:"reviews"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Reviews
	}
	if code, all := list(""); code != http.StatusOK || len(all) != 4 {
		t.Fatalf("unfiltered: %d, %d reviews; want all 4", code, len(all))
	}
	code, got := list("?media_type=series&expected_id=1")
	if code != http.StatusOK || len(got) != 2 {
		t.Fatalf("series 1: %d, %d reviews; want 2", code, len(got))
	}
	for _, rv := range got {
		if rv.MediaType != "series" || rv.ExpectedID != 1 {
			t.Errorf("series 1's list holds %s %d", rv.MediaType, rv.ExpectedID)
		}
	}
	if _, none := list("?media_type=series&expected_id=9"); none == nil || len(none) != 0 {
		t.Errorf("a show with nothing held: %v, want an empty list", none)
	}
	if code, _ := list("?expected_id=abc"); code != http.StatusBadRequest {
		t.Errorf("a bad id: HTTP %d, want 400", code)
	}
}
