package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// movieDeleteServer has one movie with one synthetic file in a temp library.
func movieDeleteServer(t *testing.T, bin string) (*routeServer, *http.Cookie, int64, string) {
	t.Helper()
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		d.Bus = eventbus.New(d.Log)
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, root, bin, d.Bus, d.Log)
		d.Automation = automation.New(d.Movies, nil, nil, nil, d.Store.DB(), d.Bus, d.Log, "")
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	res, err := s.st.DB().Exec(`INSERT INTO movies (tmdb_id, title, year, monitored) VALUES (11, 'Heat', 1995, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	if err := os.MkdirAll(filepath.Dir(video), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB().Exec(`UPDATE movies SET has_file = 1, movie_file_path = ? WHERE id = ?`, video, id); err != nil {
		t.Fatal(err)
	}
	return s, mgr, id, video
}

// The preview names each file with its size, counts the subtitles that go with them, lists
// the artwork in the movie's own folder and that the folder goes, and says where they'd
// go — the bin's path, or that the bin is off.
func TestMovieDeletePreview(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	s, mgr, id, video := movieDeleteServer(t, bin)
	if err := os.WriteFile(video[:len(video)-len(".mkv")]+".en.srt", []byte("subs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(video), "poster.jpg"), []byte("art"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB().Exec(`INSERT INTO grabs (movie_id, title, media_type, info_hash) VALUES (?, 'Heat.1995.2160p', 'movie', '0123456789abcdef0123456789abcdef01234567')`, id); err != nil {
		t.Fatal(err)
	}
	rec := s.do("GET", fmt.Sprintf("/api/v1/movies/%d/delete-preview", id), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Versions []struct {
			ID        int64  `json:"id"`
			Label     string `json:"label"`
			FileName  string `json:"file_name"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"versions"`
		Sidecars int   `json:"sidecars"`
		Bytes    int64 `json:"bytes"`
		Extras   []struct {
			Folder    string `json:"folder"`
			Name      string `json:"name"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"extras"`
		Folders []struct {
			Name    string `json:"name"`
			Removed bool   `json:"removed"`
		} `json:"folders"`
		Recycle struct {
			Enabled bool `json:"enabled"`
		} `json:"recycle"`
		Pending []struct {
			Hash  string `json:"hash"`
			Title string `json:"title"`
		} `json:"pending_downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Versions) != 1 || got.Versions[0].FileName != "Heat (1995).mkv" || got.Versions[0].SizeBytes != 10 {
		t.Errorf("versions = %+v", got.Versions)
	}
	if got.Sidecars != 1 || got.Bytes != 17 {
		t.Errorf("sidecars = %d, bytes = %d; want 1 and 17", got.Sidecars, got.Bytes)
	}
	if len(got.Extras) != 1 || got.Extras[0].Name != "poster.jpg" || got.Extras[0].Folder != "Heat (1995)" || got.Extras[0].SizeBytes != 3 {
		t.Errorf("extras = %+v, want the poster", got.Extras)
	}
	if len(got.Folders) != 1 || got.Folders[0].Name != "Heat (1995)" || !got.Folders[0].Removed {
		t.Errorf("folders = %+v, want Heat (1995) removed", got.Folders)
	}
	if !got.Recycle.Enabled {
		t.Error("the route server's bin is on; the preview should say so")
	}
	if len(got.Pending) != 1 || got.Pending[0].Title != "Heat.1995.2160p" {
		t.Errorf("pending_downloads = %+v, want the in-flight grab", got.Pending)
	}
	if rec := s.do("GET", "/api/v1/movies/999/delete-preview", mgr); rec.Code != http.StatusNotFound {
		t.Errorf("unknown movie: HTTP %d, want 404", rec.Code)
	}
}

// A delete moves only the leftovers sent back from the preview: without the list the
// artwork stays (and so does the folder); with it, the listed artwork goes and the folder
// with it, while a file that turned up after the preview stays.
func TestDeleteMovieMovesOnlyConfirmedLeftovers(t *testing.T) {
	for _, withList := range []bool{false, true} {
		bin := filepath.Join(t.TempDir(), "bin")
		s, mgr, id, video := movieDeleteServer(t, bin)
		dir := filepath.Dir(video)
		poster := filepath.Join(dir, "poster.jpg")
		if err := os.WriteFile(poster, []byte("art"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := ""
		if withList {
			rec := s.do("GET", fmt.Sprintf("/api/v1/movies/%d/delete-preview", id), mgr)
			var got struct {
				Extras []struct {
					Path string `json:"path"`
				} `json:"extras"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Extras) != 1 || got.Extras[0].Path != poster {
				t.Fatalf("preview extras = %+v (%v)", got.Extras, err)
			}
			b, _ := json.Marshal(map[string]any{"leftovers": []string{got.Extras[0].Path}})
			body = string(b)
			// Turned up after the preview: never shown, so it stays.
			if err := os.WriteFile(filepath.Join(dir, "fanart.jpg"), []byte("late"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		rec := s.doJSON("DELETE", fmt.Sprintf("/api/v1/movies/%d?delete_files=true", id), mgr, body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("list=%v: HTTP %d: %s", withList, rec.Code, rec.Body)
		}
		if _, err := os.Stat(video); err == nil {
			t.Errorf("list=%v: the video is still there", withList)
		}
		_, err := os.Stat(poster)
		if withList && err == nil {
			t.Error("the listed poster is still there")
		}
		if !withList && err != nil {
			t.Error("without the preview's list the poster went")
		}
		if withList {
			if _, err := os.Stat(filepath.Join(dir, "fanart.jpg")); err != nil {
				t.Error("a file that turned up after the preview went")
			}
		}
	}
}

// A movie delete the recycle bin refuses answers 409 with a message saying why, and the
// movie and its file are still there.
func TestDeleteMovieReturns409OnRecycleFailure(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(bin, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, mgr, id, video := movieDeleteServer(t, bin)

	rec := s.do("DELETE", fmt.Sprintf("/api/v1/movies/%d?delete_files=true", id), mgr)
	if rec.Code != http.StatusConflict {
		t.Fatalf("HTTP %d, want 409: %s", rec.Code, rec.Body)
	}
	var body struct {
		Message string   `json:"message"`
		Failed  []string `json:"failed"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Message == "" || len(body.Failed) != 1 {
		t.Errorf("409 body should explain and name the file: %s", rec.Body)
	}
	if _, err := os.Stat(video); err != nil {
		t.Error("the file was deleted although the bin refused it")
	}
	if _, err := s.deps.Movies.Get(context.Background(), id); err != nil {
		t.Errorf("the movie is gone: %v", err)
	}

	// The single-file delete answers the same way.
	if rec := s.do("DELETE", fmt.Sprintf("/api/v1/movies/%d/file", id), mgr); rec.Code != http.StatusConflict {
		t.Errorf("file delete: HTTP %d, want 409", rec.Code)
	}
	if rec := s.do("DELETE", fmt.Sprintf("/api/v1/movies/%d/versions/0/file", id), mgr); rec.Code != http.StatusConflict {
		t.Errorf("version file delete: HTTP %d, want 409", rec.Code)
	}
}

// The missing-file Clear record answers 409 and deletes nothing while the file is on disk,
// and clears the record (leaving the disk alone) once it's really gone.
func TestForgetMissingFileHandler(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	s, mgr, id, video := movieDeleteServer(t, bin)
	path := fmt.Sprintf("/api/v1/movies/%d/file/forget", id)

	if rec := s.doJSON("POST", path, mgr, `{"version_id":0}`); rec.Code != http.StatusConflict {
		t.Fatalf("file present: HTTP %d, want 409: %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(video); err != nil {
		t.Fatalf("the file was touched: %v", err)
	}
	if m, _ := s.deps.Movies.Get(context.Background(), id); !m.HasFile {
		t.Fatal("the record was cleared although the file is there")
	}

	if err := os.Remove(video); err != nil {
		t.Fatal(err)
	}
	if rec := s.doJSON("POST", path, mgr, `{"version_id":0}`); rec.Code != http.StatusOK {
		t.Fatalf("file gone: HTTP %d: %s", rec.Code, rec.Body)
	}
	if m, _ := s.deps.Movies.Get(context.Background(), id); m.HasFile {
		t.Fatal("record not cleared")
	}
}
