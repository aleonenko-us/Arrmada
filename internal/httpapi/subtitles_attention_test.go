package httpapi

import (
	"encoding/json"
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

// Retry now clears the bucket's backoff and queues those files at manual priority;
// both board routes are manager-only.
func TestRetryHandlerClearsBackoffAndQueues(t *testing.T) {
	lib := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		mv := movies.NewService(d.Store.DB(), nil, nil, lib, "", nil, log)
		d.Movies = mv
		d.Subtitles = subtitles.NewService(d.Store.DB(), mv, nil, d.Settings, subtitles.NewOpenSubtitles("", "", ""), "ffmpeg", "ffprobe", t.TempDir(), log)
	})
	l := s.deps.Subtitles.Ledger()
	for i, name := range []string{"A", "B"} {
		video := filepath.Join(lib, name+".mkv")
		if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		id := int64(i + 1)
		if _, err := s.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`, id, 1000+id, name, video); err != nil {
			t.Fatal(err)
		}
		if err := l.SaveBackoff(t.Context(), subtitles.Backoff{MediaKey: "m:" + string(rune('0'+id)), Lang: "en", VideoPath: video,
			PrimaryReason: subtitles.ResultNoMatch, Count: 1, NextRetryAt: 4102444800}); err != nil {
			t.Fatal(err)
		}
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleRequester)

	if rec := s.do("GET", "/api/v1/subtitles/attention", viewer); rec.Code != http.StatusForbidden {
		t.Errorf("requester board: HTTP %d", rec.Code)
	}
	if rec := s.doBody("POST", "/api/v1/subtitles/retry", `{"all_in_bucket":"no_match"}`, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("requester retry: HTTP %d", rec.Code)
	}
	rec := s.do("GET", "/api/v1/subtitles/attention", mgr)
	var board subtitles.AttentionBoard
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &board) != nil || board.Total != 2 {
		t.Fatalf("board: HTTP %d %s", rec.Code, rec.Body)
	}
	rec = s.doBody("POST", "/api/v1/subtitles/retry", `{"all_in_bucket":"no_match"}`, mgr)
	var res struct{ Queued int }
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Queued != 2 {
		t.Fatalf("retry: HTTP %d %s", rec.Code, rec.Body)
	}
	if left, _ := l.LoadBackoffs(t.Context()); len(left) != 0 {
		t.Errorf("backoff left %v", left)
	}
	jobs := s.deps.Subtitles.Jobs()
	if len(jobs) != 2 || jobs[0].Priority != subtitles.PrioManual || jobs[0].State != "queued" {
		t.Errorf("jobs %+v", jobs)
	}
	if rec := s.doBody("POST", "/api/v1/subtitles/retry", `{"all_in_bucket":"orphaned"}`, mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("orphans: HTTP %d", rec.Code)
	}
}
