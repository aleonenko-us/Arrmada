package arrimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeArr is a Radarr (or Sonarr) with two root folders — the asking user's case:
// Movies and Kids movies.
func fakeArr(t *testing.T, app string) *httptest.Server {
	t.Helper()
	list := "/api/v3/movie"
	if app == Sonarr {
		list = "/api/v3/series"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"path":"/movies/","freeSpace":100},{"path":"/kids-movies","freeSpace":200}]`))
		case list:
			_, _ = w.Write([]byte(`[
				{"title":"Heat","rootFolderPath":"/movies/","path":"/movies/Heat (1995)"},
				{"title":"Ronin","rootFolderPath":"/movies","path":"/movies/Ronin (1998)"},
				{"title":"Up","rootFolderPath":"","path":"/kids-movies/Up (2009)"},
				{"title":"Lost","rootFolderPath":"/gone","path":"/gone/Lost"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRootsCountTitlesPerFolder(t *testing.T) {
	srv := fakeArr(t, Radarr)
	defer srv.Close()
	c, err := New("Radarr", srv.URL+"/", "secret")
	if err != nil {
		t.Fatal(err)
	}
	roots, err := c.Roots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0].Path != "/kids-movies" || roots[0].Titles != 1 || roots[1].Path != "/movies" || roots[1].Titles != 2 || roots[1].FreeBytes != 100 {
		t.Fatalf("roots = %+v", roots)
	}
	if c.Media() != "movie" {
		t.Errorf("media = %s", c.Media())
	}

	son := fakeArr(t, Sonarr)
	defer son.Close()
	cs, _ := New("sonarr", son.URL, "secret")
	if r, err := cs.Roots(context.Background()); err != nil || len(r) != 2 || cs.Media() != "series" {
		t.Errorf("sonarr roots = %+v, %v", r, err)
	}

	bad, _ := New(Radarr, srv.URL, "wrong")
	if _, err := bad.Roots(context.Background()); !errors.Is(err, ErrBadKey) {
		t.Errorf("a refused key: %v", err)
	}
	for _, in := range [][3]string{{"lidarr", srv.URL, "k"}, {Radarr, "radarr:7878", "k"}, {Radarr, srv.URL, " "}} {
		if _, err := New(in[0], in[1], in[2]); err == nil {
			t.Errorf("New(%q) accepted", in)
		}
	}
}

func TestMapMatchesFolders(t *testing.T) {
	roots := []Root{{Path: "/movies"}, {Path: "/kids-movies"}, {Path: "/anime"}, {Path: "/elsewhere"}}
	folders := []Folder{{ID: 0, Path: "/movies"}, {ID: 3, Path: "/storage/media/kids-movies"}}
	exists := func(p string) bool { return p == "/anime" }
	got := Map(roots, folders, exists)
	if got[0].FolderID == nil || *got[0].FolderID != 0 || got[0].MatchedBy != "path" {
		t.Errorf("/movies = %+v", got[0])
	}
	if got[1].FolderID == nil || *got[1].FolderID != 3 || got[1].MatchedBy != "name" {
		t.Errorf("/kids-movies = %+v", got[1])
	}
	if got[2].FolderID != nil || got[2].SuggestedPath != "/anime" {
		t.Errorf("/anime = %+v, want a new folder at the same path", got[2])
	}
	if got[3].FolderID != nil || got[3].SuggestedPath != "" {
		t.Errorf("/elsewhere = %+v, want nothing to suggest", got[3])
	}
	// An Arrmada folder is matched once: two roots named alike don't both take it.
	twice := Map([]Root{{Path: "/a/movies"}, {Path: "/b/movies"}}, []Folder{{ID: 1, Path: "/x/movies"}}, nil)
	if twice[0].FolderID == nil || twice[1].FolderID != nil {
		t.Errorf("twice = %+v", twice)
	}
}
