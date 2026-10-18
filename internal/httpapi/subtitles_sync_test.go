package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// The sync check finds the sidecar from the catalog: an unknown title or a missing
// sidecar is a 404 straight away, a path in the body (or a language that isn't one) is
// refused, and requesters can't call it at all.
func TestSyncCheckResolvesPathServerSide(t *testing.T) {
	lib := t.TempDir()
	video := filepath.Join(lib, "Movie (2020).mkv")
	if err := os.WriteFile(video, []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newRouteServer(t, func(d *Deps) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		mv := movies.NewService(d.Store.DB(), nil, nil, lib, "", nil, log)
		d.Movies = mv
		d.Subtitles = subtitles.NewService(d.Store.DB(), mv, nil, d.Settings, subtitles.NewOpenSubtitles("", "", ""), "ffmpeg", "ffprobe", t.TempDir(), log)
	})
	if _, err := s.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (1, 1001, 'Movie', 1, 1, ?)`, video); err != nil {
		t.Fatal(err)
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleRequester)

	for _, tc := range []struct {
		name, body string
		who        *http.Cookie
		want       int
	}{
		{"requester", `{"kind":"movie","movie_id":1,"lang":"en"}`, viewer, http.StatusForbidden},
		{"unknown movie", `{"kind":"movie","movie_id":99,"lang":"en"}`, mgr, http.StatusNotFound},
		{"no sidecar", `{"kind":"movie","movie_id":1,"lang":"en"}`, mgr, http.StatusNotFound},
		{"bad kind", `{"kind":"file","movie_id":1,"lang":"en"}`, mgr, http.StatusNotFound},
		{"a client path", `{"kind":"movie","movie_id":1,"lang":"en","path":"/etc/passwd"}`, mgr, http.StatusBadRequest},
		{"a path as the language", `{"kind":"movie","movie_id":1,"lang":"x/../../../../tmp/a"}`, mgr, http.StatusBadRequest},
		{"not a language", `{"kind":"movie","movie_id":1,"lang":"zz"}`, mgr, http.StatusBadRequest},
	} {
		if rec := s.doBody("POST", "/api/v1/subtitles/sync-check", tc.body, tc.who); rec.Code != tc.want {
			t.Errorf("%s: HTTP %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
	// An .ass sidecar can't be measured: refused before any job starts.
	if err := os.WriteFile(filepath.Join(lib, "Movie (2020).en.ass"), []byte("[Script Info]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := s.doBody("POST", "/api/v1/subtitles/sync-check", `{"kind":"movie","movie_id":1,"lang":"en"}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("ass sidecar: HTTP %d %s", rec.Code, rec.Body)
	}
}
