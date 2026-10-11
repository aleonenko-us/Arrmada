package automation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// MOV-12: a download grabbed for a track attaches to that track, even at the same
// resolution as the default track's file, and the default file stays put. Temp dirs only.
func TestWatchImportsRoutesByGrabVersion(t *testing.T) {
	ctx := context.Background()
	c, mv, _, _ := attachTestCoord(t)
	lib := t.TempDir()
	def := filepath.Join(lib, "Dune (2021)", "Dune (2021) - 2160p BluRay.mkv")
	writeVideo(t, def)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored, has_file, movie_file_path, source_release)
		VALUES (1, 438631, 'Dune', 2021, 1, 1, ?, 'Dune.2021.2160p.BluRay.x265-GRP')`, def)
	mustExec(t, c, `INSERT INTO movie_versions (id, movie_id, label, monitored, edition) VALUES (7, 1, 'Director''s Cut', 1, 'Director''s Cut')`)
	mustExec(t, c, `INSERT INTO grabs (movie_id, version_id, title, indexer, quality_profile, media_type, info_hash)
		VALUES (1, 7, 'Dune.2021.2160p.BluRay.x265-OTHER', 'idx', '', 'movie', 'aaaa')`)

	// An import record from before tracks were decided at import (no movie on it): the
	// grab row's track decides.
	dc := filepath.Join(lib, "Dune (2021)", "Dune (2021) {edition-Director's Cut} - 2160p BluRay.mkv")
	writeVideo(t, dc)
	out, err := c.AttachMovieImport(ctx, library.ImportRecord{
		Hash: "AAAA", TargetPath: dc, Title: "Dune", Year: 2021, ReleaseName: "Dune.2021.2160p.BluRay.x265-OTHER", VersionID: -1,
	})
	if err != nil || out != library.Attached {
		t.Fatalf("attach = %v, %v", out, err)
	}
	rows, _ := mv.VersionRows(ctx, 1)
	if rows[0].FilePath != def || rows[1].FilePath != dc || !rows[1].HasFile {
		t.Fatalf("tracks = %+v", rows)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatalf("the default track's file was touched: %v", err)
	}
}

// The track the import decided (kept on the record) wins: a default-track grab the
// importer routed to an extra track lands there.
func TestAttachUsesDecidedTrack(t *testing.T) {
	ctx := context.Background()
	c, mv, _, _ := attachTestCoord(t)
	lib := t.TempDir()
	def := filepath.Join(lib, "Heat (1995)", "Heat (1995) - 1080p BluRay.mkv")
	writeVideo(t, def)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored, has_file, movie_file_path)
		VALUES (2, 949, 'Heat', 1995, 1, 1, ?)`, def)
	mustExec(t, c, `INSERT INTO movie_versions (id, movie_id, label, monitored) VALUES (9, 2, '4K', 1)`)
	file := filepath.Join(lib, "Heat (1995)", "Heat (1995) - 2160p BluRay.mkv")
	writeVideo(t, file)
	out, err := c.AttachMovieImport(ctx, library.ImportRecord{
		Hash: "bbbb", TargetPath: file, Title: "Heat", Year: 1995, MovieID: 2, VersionID: 9,
	})
	if err != nil || out != library.Attached {
		t.Fatalf("attach = %v, %v", out, err)
	}
	rows, _ := mv.VersionRows(ctx, 2)
	if rows[0].FilePath != def || rows[1].FilePath != file {
		t.Fatalf("tracks = %+v", rows)
	}
	// Offered again for the default track: refused (it's the 4K track's file), not retried.
	out, err = c.AttachMovieImport(ctx, library.ImportRecord{
		Hash: "cccc", TargetPath: file, Title: "Heat", Year: 1995, MovieID: 2, VersionID: 0,
	})
	if out != library.Refused || err == nil {
		t.Fatalf("attach onto another track's file = %v, %v; want Refused", out, err)
	}
}

func TestRecordManualGrabStoresVersion(t *testing.T) {
	ctx := context.Background()
	c, _, _, _ := attachTestCoord(t)
	c.quality = quality.NewService(c.db)
	c.indexers = indexer.NewService(c.db, c.log, nil)
	mustExec(t, c, `INSERT INTO movies (id, tmdb_id, title, year, monitored, quality_profile) VALUES (3, 1, 'Alien', 1979, 1, '')`)
	existing := filepath.Join(t.TempDir(), "Alien (1979) {edition-Director's Cut} - 2160p.mkv")
	writeVideo(t, existing)
	mustExec(t, c, `INSERT INTO movie_versions (id, movie_id, label, monitored, has_file, file_path, edition) VALUES (11, 3, 'DC', 1, 1, ?, 'Director''s Cut')`, existing)

	c.RecordManualGrab(ctx, 3, 11, "Alien.1979.Directors.Cut.2160p.UHD.BluRay-GRP", "idx", "hash11")
	ref, ok := c.MovieGrabForHash(ctx, "HASH11")
	if !ok || ref.MovieID != 3 || ref.VersionID != 11 || ref.ReplacesPath != existing {
		t.Fatalf("grab = %+v, %v; want movie 3, track 11, replacing the track's file", ref, ok)
	}
	// A track that isn't this movie's falls back to the default track.
	c.RecordManualGrab(ctx, 3, 999, "Alien.1979.1080p.BluRay-GRP", "idx", "hash0")
	if ref, ok := c.MovieGrabForHash(ctx, "hash0"); !ok || ref.VersionID != 0 || ref.ReplacesPath != "" {
		t.Fatalf("foreign track grab = %+v, %v", ref, ok)
	}
}
