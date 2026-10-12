package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

const fileTestSRT = "1\n00:00:01,000 --> 00:00:02,000\nHello.\n\n2\n00:00:03,000 --> 00:00:04,000\nAgain.\n"

// subtitleFileServer is a Subtitles service over a real movies module with one movie
// (id 1) in its own temp folder, a sidecar beside it, and a recycle bin.
func subtitleFileServer(t *testing.T) (*routeServer, string) {
	t.Helper()
	dir := t.TempDir()
	video := filepath.Join(dir, "Heat (1995).mkv")
	for name, body := range map[string]string{"Heat (1995).mkv": "x", "Heat (1995).en.srt": fileTestSRT} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		mv := movies.NewService(d.Store.DB(), nil, nil, t.TempDir(), "", nil, log)
		d.Subtitles = subtitles.NewService(d.Store.DB(), mv, nil, d.Settings, subtitles.NewOpenSubtitles("", "", ""), "ffmpeg", "ffprobe", t.TempDir(), log)
		d.Subtitles.SetBin(library.SingleBin(bin))
	})
	if _, err := s.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (1, 101, 'Heat', 1, 1, ?)`, video); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// The per-file routes resolve the video from the database and accept only a bare name
// that pairs with it: a path, another folder's file or a non-subtitle is a 400, and
// nothing on disk changes.
func TestSubtitleFileRoutesRejectForeignPaths(t *testing.T) {
	s, dir := subtitleFileServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "Heat (1995).en.srt"), []byte(fileTestSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"../Heat (1995).en.srt", filepath.Join(other, "Heat (1995).en.srt"), "x/Heat (1995).en.srt",
		"Heat (1995).mkv", "Heat (1995).en.nfo", "Heat (1995).fr.srt",
	} {
		body, _ := json.Marshal(map[string]any{"kind": "movie", "movie_id": 1, "name": name, "offset_ms": 500})
		if rec := s.doBody("POST", "/api/v1/subtitles/file/shift", string(body), mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("shift %q: HTTP %d %s, want 400", name, rec.Code, rec.Body)
		}
		if rec := s.do("DELETE", "/api/v1/subtitles/file?kind=movie&id=1&name="+url.QueryEscape(name), mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("delete %q: HTTP %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	for _, p := range []string{filepath.Join(dir, "Heat (1995).en.srt"), filepath.Join(other, "Heat (1995).en.srt")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if rec := s.do("GET", "/api/v1/subtitles/file?kind=movie&id=9", mgr); rec.Code != http.StatusNotFound {
		t.Errorf("unknown movie: HTTP %d", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/subtitles/file?kind=movie", mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("no id: HTTP %d", rec.Code)
	}
}

// The drawer's round trip over HTTP: read the file, nudge it, delete it to the bin.
func TestSubtitleFileRoutesRoundTrip(t *testing.T) {
	s, dir := subtitleFileServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleRequester)
	if rec := s.do("GET", "/api/v1/subtitles/file?kind=movie&id=1", viewer); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}

	var v subtitles.FileView
	rec := s.do("GET", "/api/v1/subtitles/file?kind=movie&id=1", mgr)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("view: HTTP %d %s", rec.Code, rec.Body)
	}
	if len(v.Sidecars) != 1 || v.Sidecars[0].Name != "Heat (1995).en.srt" || v.Sidecars[0].CueCount != 2 || v.Sidecars[0].FirstCues[0].StartMs != 1000 {
		t.Fatalf("sidecars = %+v", v.Sidecars)
	}

	var sv subtitles.SidecarView
	rec = s.doBody("POST", "/api/v1/subtitles/file/shift", `{"kind":"movie","movie_id":1,"name":"Heat (1995).en.srt","offset_ms":1000}`, mgr)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &sv) != nil || sv.FirstCues[0].StartMs != 2000 {
		t.Fatalf("shift: HTTP %d %s", rec.Code, rec.Body)
	}

	rec = s.do("DELETE", "/api/v1/subtitles/file?kind=movie&id=1&name="+url.QueryEscape("Heat (1995).en.srt"), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: HTTP %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Heat (1995).en.srt")); !os.IsNotExist(err) {
		t.Errorf("the sidecar is still there: %v", err)
	}
}
