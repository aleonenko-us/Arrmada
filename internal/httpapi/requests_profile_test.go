package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// createRequestAs posts a request and returns what the server stored for it.
func createRequestAs(t *testing.T, s *routeServer, c *http.Cookie, body string) requests.Request {
	t.Helper()
	rec := s.doJSON("POST", "/api/v1/requests", c, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create %s: HTTP %d %s", body, rec.Code, rec.Body)
	}
	var out struct {
		Request requests.Request `json:"request"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	stored, err := s.deps.Requests.Get(context.Background(), out.Request.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

// The quality profile on a new request is staff's to choose (issue #50): a manager's pick
// is kept, a requester's is dropped rather than refused, and an unknown pick from staff is
// still an error. A requester's book keeps the profile its Read/Listen choice maps to.
func TestCreateRequestProfileIsStaffOnly(t *testing.T) {
	s := requestsServer(t)
	ctx := context.Background()
	q := s.deps.Quality
	sp, err := q.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "HD-720p"})
	if err != nil {
		t.Fatal(err)
	}
	hd720 := "custom:" + strconv.FormatInt(sp.ID, 10)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	got := createRequestAs(t, s, kid, `{"media_type":"movie","tmdb_id":11,"title":"Heat","year":1995,"quality_profile":"`+hd720+`"}`)
	if got.QualityProfile != "" {
		t.Errorf("requester's request stored profile %q, want none (staff choose at approval)", got.QualityProfile)
	}
	// Dropped, not checked: a requester's unknown profile doesn't fail their request.
	got = createRequestAs(t, s, kid, `{"media_type":"movie","tmdb_id":12,"title":"Ronin","year":1998,"quality_profile":"custom:9999"}`)
	if got.QualityProfile != "" {
		t.Errorf("requester's unknown profile stored as %q", got.QualityProfile)
	}

	got = createRequestAs(t, s, mgr, `{"media_type":"movie","tmdb_id":13,"title":"Thief","year":1981,"quality_profile":"`+hd720+`"}`)
	if got.Status != requests.StatusPending || got.QualityProfile != hd720 {
		t.Errorf("manager's request = status %q profile %q, want pending on %q", got.Status, got.QualityProfile, hd720)
	}
	if rec := s.doJSON("POST", "/api/v1/requests", mgr, `{"media_type":"movie","tmdb_id":14,"title":"Collateral","year":2004,"quality_profile":"custom:9999"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("manager's unknown profile: HTTP %d, want 400", rec.Code)
	}
	// A profile for another kind of media is refused, on create and on approve.
	tvp, err := q.Create(ctx, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "TV 720p"})
	if err != nil {
		t.Fatal(err)
	}
	tv := "custom:" + strconv.FormatInt(tvp.ID, 10)
	if rec := s.doJSON("POST", "/api/v1/requests", mgr, `{"media_type":"movie","tmdb_id":15,"title":"Manhunter","year":1986,"quality_profile":"`+tv+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("manager's series profile on a movie: HTTP %d, want 400", rec.Code)
	}
	pending := createRequestAs(t, s, kid, `{"media_type":"movie","tmdb_id":16,"title":"Miami Vice","year":2006}`)
	if rec := s.doJSON("POST", "/api/v1/requests/"+strconv.FormatInt(pending.ID, 10)+"/approve", mgr, `{"quality_profile":"`+tv+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("approve on a series profile: HTTP %d, want 400: %s", rec.Code, rec.Body)
	}

	// Books: the formats choose the profile, whatever profile rode along.
	listen := createRequestAs(t, s, kid, `{"media_type":"book","ol_key":"OL1W","title":"Dune","author":"Frank Herbert","year":1965,"formats":"audiobook","quality_profile":"`+hd720+`"}`)
	want := q.BookProfileFor(ctx, false, true)
	if listen.Formats != requests.FormatsAudiobook || listen.QualityProfile != want {
		t.Errorf("requester's audiobook request = formats %q profile %q, want audiobook on %q", listen.Formats, listen.QualityProfile, want)
	}
	plain := createRequestAs(t, s, kid, `{"media_type":"book","ol_key":"OL2W","title":"Emma","author":"Jane Austen","year":1815,"formats":"ebook"}`)
	if plain.Formats != requests.FormatsEbook || plain.QualityProfile != q.BookProfileFor(ctx, true, false) {
		t.Errorf("requester's ebook request = formats %q profile %q", plain.Formats, plain.QualityProfile)
	}
}
