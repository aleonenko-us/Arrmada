package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// MOV-14: a track's label, profile and edition can be edited (an unknown profile is
// refused), manual import can target a track, and one movie's routes never reach another
// movie's track. Temp dirs and synthetic files only.

type versionsFixture struct {
	s         *routeServer
	mgr       *http.Cookie
	movieID   int64
	other     int64 // a second movie
	versionID int64 // an extra track of movieID
	otherVID  int64 // an extra track of other
	movies    *movies.Service
	downloads string
	library   string
}

func newVersionsFixture(t *testing.T) versionsFixture {
	t.Helper()
	base := t.TempDir()
	f := versionsFixture{downloads: filepath.Join(base, "downloads"), library: filepath.Join(base, "movies")}
	for _, d := range []string{f.downloads, f.library} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.s = newRouteServer(t, func(d *Deps) {
		d.Config.DownloadsDir, d.Config.MoviesDir = f.downloads, f.library
		d.Bus = eventbus.New(d.Log)
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, f.library, "", d.Bus, d.Log)
		d.Quality = quality.NewService(d.Store.DB())
		d.Automation = automation.New(d.Movies, nil, nil, d.Quality, d.Store.DB(), d.Bus, d.Log, "")
		f.movies = d.Movies
	})
	_, f.mgr = f.s.user(t, "mgr@example.com", auth.RoleManager)
	ctx := t.Context()
	for i, title := range []string{"Heat", "Alien"} {
		res, err := f.s.st.DB().Exec(`INSERT INTO movies (tmdb_id, title, year, monitored) VALUES (?, ?, 1995, 1)`, 100+i, title)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		v, err := f.movies.AddVersion(ctx, id, "DC", "", "Director's Cut", true)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.movieID, f.versionID = id, v.ID
		} else {
			f.other, f.otherVID = id, v.ID
		}
	}
	return f
}

func TestUpdateVersionRejectsUnknownProfile(t *testing.T) {
	f := newVersionsFixture(t)
	path := fmt.Sprintf("/api/v1/movies/%d/versions/%d", f.movieID, f.versionID)
	if rec := f.s.doJSON("PUT", path, f.mgr, `{"label":"DC","quality_profile":"custom:9999","edition":"Director's Cut","monitored":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown profile: HTTP %d, want 400: %s", rec.Code, rec.Body)
	}
	// A real edit saves, with the edition stored the parser's way.
	if rec := f.s.doJSON("PUT", path, f.mgr, `{"label":"Final","edition":"final cut","monitored":true}`); rec.Code != http.StatusOK {
		t.Fatalf("edit: HTTP %d: %s", rec.Code, rec.Body)
	}
	rows, _ := f.movies.VersionRows(t.Context(), f.movieID)
	if got := rows[1]; got.Label != "Final" || got.Edition != "Final Cut" {
		t.Fatalf("track after edit = %+v", got)
	}
	// Another movie's track can't be edited or removed through this movie.
	cross := fmt.Sprintf("/api/v1/movies/%d/versions/%d", f.movieID, f.otherVID)
	if rec := f.s.doJSON("PUT", cross, f.mgr, `{"label":"x","monitored":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-movie edit: HTTP %d, want 404", rec.Code)
	}
	if rec := f.s.do("DELETE", cross, f.mgr); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-movie delete: HTTP %d, want 404", rec.Code)
	}
	if rec := f.s.do("DELETE", cross+"/file", f.mgr); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-movie file delete: HTTP %d, want 404", rec.Code)
	}
	if rows, _ := f.movies.VersionRows(t.Context(), f.other); len(rows) != 2 || rows[1].Label != "DC" {
		t.Fatalf("the other movie's track changed: %+v", rows)
	}
}

// A grab's track is the one its search ranked for (the token says so): a body naming a
// different track is refused before anything is grabbed.
func TestGrabRefusesOtherTrack(t *testing.T) {
	f := newVersionsFixture(t)
	u, err := f.s.auth.UserByUsername(t.Context(), "mgr@example.com")
	if err != nil {
		t.Fatal(err)
	}
	tok := f.s.deps.Automation.IssueReleaseToken(automation.ReleaseRef{
		MediaKind: automation.ReleaseKindMovie, MediaID: f.movieID, VersionID: f.versionID,
		Title: "Heat.1995.Directors.Cut.1080p", DownloadURL: "magnet:?xt=urn:btih:00", UserID: u.ID,
	})
	body := fmt.Sprintf(`{"token":%q,"movie_id":%d,"version_id":%d}`, tok, f.movieID, f.versionID+1)
	if rec := f.s.doJSON("POST", "/api/v1/grab", f.mgr, body); rec.Code != http.StatusBadRequest {
		t.Fatalf("other track: HTTP %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestManualImportIntoVersion(t *testing.T) {
	f := newVersionsFixture(t)
	src := filepath.Join(f.downloads, "Heat.1995.Directors.Cut.1080p.BluRay.x264-GRP.mkv")
	if err := os.WriteFile(src, []byte("dc"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/movies/%d/manualimport", f.movieID)
	body := fmt.Sprintf(`{"path":%q,"version_id":%d}`, src, f.versionID)
	if rec := f.s.doJSON("POST", path, f.mgr, body); rec.Code != http.StatusOK {
		t.Fatalf("import: HTTP %d: %s", rec.Code, rec.Body)
	}
	rows, _ := f.movies.VersionRows(t.Context(), f.movieID)
	if rows[0].HasFile || !rows[1].HasFile {
		t.Fatalf("tracks = %+v; want the file on the DC track only", rows)
	}
	want := filepath.Join(f.library, "Heat (1995)", "Heat (1995) {edition-Director's Cut} - 1080p BluRay.mkv")
	if rows[1].FilePath != want {
		t.Fatalf("placed at %q, want %q", rows[1].FilePath, want)
	}
	// Another movie's track: 404. A path outside the import folders: 400 (the SEC guard).
	cross := fmt.Sprintf(`{"path":%q,"version_id":%d}`, src, f.otherVID)
	if rec := f.s.doJSON("POST", path, f.mgr, cross); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-movie track: HTTP %d, want 404", rec.Code)
	}
	outside := fmt.Sprintf(`{"path":%q,"version_id":%d}`, filepath.Join(t.TempDir(), "x.mkv"), f.versionID)
	if rec := f.s.doJSON("POST", path, f.mgr, outside); rec.Code != http.StatusBadRequest {
		t.Fatalf("outside path: HTTP %d, want 400", rec.Code)
	}
}
