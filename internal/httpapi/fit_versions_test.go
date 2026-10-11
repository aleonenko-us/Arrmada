package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Convert indexes every track of a movie, but the library's fit badges are one per film,
// judged on its default file: a 4K version in the index doesn't add a second badge.
func TestLibraryFitIgnoresExtraVersions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	dir := t.TempDir()
	mv := movies.NewService(db, nil, nil, dir, "", nil, log)
	cv := convert.NewService(db, mv, nil, settings.NewService(db), filepath.Join(dir, "no-ffmpeg"), filepath.Join(dir, "no-ffprobe"),
		filepath.Join(dir, "scratch"), filepath.Join(dir, "recycle"), log)
	a := &api{deps: Deps{Store: st, Log: log, Movies: mv, Quality: quality.NewService(db), Convert: cv}}

	info := `{"video_codec":"h264","width":1920,"height":1080,"resolution":"1080p","bitrate_kbps":8000,"size_bytes":1000}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO movies (id, tmdb_id, title, has_file, movie_file_path) VALUES (1, 1, 'Film', 1, '/m/Film.mkv')`, nil},
		{`INSERT INTO movie_versions (id, movie_id, label, has_file, file_path) VALUES (2, 1, '4K', 1, '/m/Film - 4K.mkv')`, nil},
		// The analysis version the index writes today (convert's probeSchemaVersion).
		{`INSERT INTO convert_library (path, media_type, movie_id, version_id, version_label, title, size_bytes, video_codec, info_json, info_ver)
		  VALUES ('/m/Film.mkv', 'movie', 1, 0, '', 'Film', 1000, 'h264', ?, 4),
		         ('/m/Film - 4K.mkv', 'movie', 1, 2, '4K', 'Film', 1000, 'h264', ?, 4)`, []any{info, info}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	files, err := cv.IndexedFiles(ctx, "movie", 0)
	if err != nil || len(files) != 2 {
		t.Fatalf("precondition: both tracks indexed, got %d (%v) — has the analysis version moved on?", len(files), err)
	}

	rec := httptest.NewRecorder()
	a.libraryFitMovies(rec, ctx)
	var out struct {
		Items []fitItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].MovieID != 1 {
		t.Fatalf("fit items = %+v, want one for the film", out.Items)
	}
	if got := a.indexedForMedia(ctx, quality.MediaMovie); len(got) != 1 {
		t.Fatalf("profile bars count %d files, want 1", len(got))
	}
}
