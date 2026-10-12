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

// A per-language redo is a manager action on a catalog title: bad modes, forced
// subtitles and unknown titles are refused; a good one queues a job for that language.
func TestSubtitleRedoHandler(t *testing.T) {
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
		{"requester", `{"kind":"movie","movie_id":1,"lang":"en","mode":"next"}`, viewer, http.StatusForbidden},
		{"bad mode", `{"kind":"movie","movie_id":1,"lang":"en","mode":"burn"}`, mgr, http.StatusBadRequest},
		{"bad lang", `{"kind":"movie","movie_id":1,"lang":"zz-top","mode":"next"}`, mgr, http.StatusBadRequest},
		{"forced", `{"kind":"movie","movie_id":1,"lang":"en","variant":"forced","mode":"next"}`, mgr, http.StatusBadRequest},
		{"unknown movie", `{"kind":"movie","movie_id":9,"lang":"en","mode":"next"}`, mgr, http.StatusNotFound},
		{"ok", `{"kind":"movie","movie_id":1,"lang":"eng","mode":"ai","audio_stream":1}`, mgr, http.StatusAccepted},
	} {
		if rec := s.doBody("POST", "/api/v1/subtitles/redo", tc.body, tc.who); rec.Code != tc.want {
			t.Errorf("%s: HTTP %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
	jobs := s.deps.Subtitles.Jobs()
	if len(jobs) != 1 || jobs[0].Mode != "ai" || len(jobs[0].Langs) != 1 || jobs[0].Langs[0] != "en" || jobs[0].AudioStream == nil || *jobs[0].AudioStream != 1 {
		t.Errorf("queued %+v", jobs)
	}
}
