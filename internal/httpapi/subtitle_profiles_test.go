package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// The profile routes: the default is seeded and listed first, a bad or duplicate profile
// is refused with a reason, the default can't be deleted, and an override needs a title.
func TestSubtitleProfileRoutes(t *testing.T) {
	s := newSubtitlesServer(t)
	_, mgr := s.user(t, "manager@example.com", auth.RoleManager)

	var list struct {
		Profiles  []subtitles.Profile  `json:"profiles"`
		Overrides []subtitles.Override `json:"overrides"`
	}
	rec := s.do("GET", "/api/v1/subtitles/profiles", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: HTTP %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 1 || !list.Profiles[0].IsDefault || len(list.Profiles[0].Items) != 1 || list.Overrides == nil {
		t.Fatalf("list = %+v, want the seeded default and an empty override list", list)
	}
	def := list.Profiles[0].ID

	for _, c := range []struct {
		body string
		code int
	}{
		{`{"name":"Anime","items":[{"lang":"ja"},{"lang":"en","variant":"forced"}]}`, http.StatusCreated},
		{`{"name":"anime","items":[{"lang":"ja"}]}`, http.StatusConflict},
		{`{"name":"Bad","items":[{"lang":"klingon"}]}`, http.StatusBadRequest},
		{`{"name":"Bad","items":[{"lang":"en","sdh":"sometimes"}]}`, http.StatusBadRequest},
		{`{"name":"","items":[{"lang":"en"}]}`, http.StatusBadRequest},
	} {
		if rec := s.doBody("POST", "/api/v1/subtitles/profiles", c.body, mgr); rec.Code != c.code {
			t.Errorf("POST %s: HTTP %d %s, want %d", c.body, rec.Code, rec.Body, c.code)
		}
	}
	if rec := s.do("DELETE", "/api/v1/subtitles/profiles/"+strconv.FormatInt(def, 10), mgr); rec.Code != http.StatusConflict {
		t.Errorf("deleting the default: HTTP %d, want 409", rec.Code)
	}
	if rec := s.doBody("PUT", "/api/v1/subtitles/profiles/"+strconv.FormatInt(def, 10), `{"items":[{"lang":"en","variant":"both","only_if_audio_differs":true}]}`, mgr); rec.Code != http.StatusOK {
		t.Errorf("editing the default: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doBody("PUT", "/api/v1/subtitles/overrides/movie/42", `{"profile_id":1}`, mgr); rec.Code != http.StatusNotFound {
		t.Errorf("an override for no movie: HTTP %d, want 404", rec.Code)
	}
	if rec := s.doBody("PUT", "/api/v1/subtitles/overrides/album/42", `{"profile_id":1}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("an override of an unknown kind: HTTP %d, want 400", rec.Code)
	}
}
