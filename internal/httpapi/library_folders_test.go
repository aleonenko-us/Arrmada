package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// foldersAPI is an api over a real database with real temp folders: the default Movies
// and TV folders and a share to add extra ones from.
func foldersAPI(t *testing.T) (*api, *store.Store, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	base := t.TempDir()
	for _, d := range []string{"movies", "tv", "kids", "anime", "downloads"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := &api{deps: Deps{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: settings.NewService(st.DB()),
		Config: config.Config{MoviesDir: filepath.Join(base, "movies"), TVDir: filepath.Join(base, "tv"),
			DownloadsDir: filepath.Join(base, "downloads"), DataDir: filepath.Join(base, "data")},
	}}
	a.deps.Folders = libfolders.New(st.DB(), a.roots(), a.deps.Config.DataDir)
	return a, st, base
}

func folderCall(t *testing.T, h http.HandlerFunc, method, body string, pathID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

type foldersBody struct {
	Movie  libraryFolders `json:"movie"`
	Series libraryFolders `json:"series"`
}

func listFolders(t *testing.T, a *api) foldersBody {
	t.Helper()
	w := folderCall(t, a.handleLibraryFolders, http.MethodGet, "", "")
	var got foldersBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}
	return got
}

func TestLibraryFoldersAPI(t *testing.T) {
	a, st, base := foldersAPI(t)

	// One folder per library: what every page reads to decide there's no picker.
	got := listFolders(t, a)
	if got.Movie.Multi || got.Series.Multi || len(got.Movie.Folders) != 1 || !got.Movie.Folders[0].Default {
		t.Fatalf("before any extra folder: %+v", got)
	}
	if w := folderCall(t, a.handleSuggestFolder, http.MethodGet, "", ""); w.Code != http.StatusBadRequest {
		t.Errorf("suggest without params = %d", w.Code)
	}

	kidsPath := filepath.Join(base, "kids")
	w := folderCall(t, a.handleAddLibraryFolder, http.MethodPost, `{"media":"movie","path":`+jsonString(kidsPath)+`,"label":"Kids","rule":"kids"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", w.Code, w.Body.String())
	}
	var kids libfolders.Folder
	_ = json.Unmarshal(w.Body.Bytes(), &kids)

	// A bad folder says why, plainly.
	w = folderCall(t, a.handleAddLibraryFolder, http.MethodPost, `{"media":"movie","path":`+jsonString(filepath.Join(base, "kdis"))+`}`, "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "doesn't exist") || strings.Contains(w.Body.String(), "invalid folder") {
		t.Errorf("missing folder = %d %s", w.Code, w.Body.String())
	}
	w = folderCall(t, a.handleAddLibraryFolder, http.MethodPost, `{"media":"book","path":`+jsonString(kidsPath)+`}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("books = %d", w.Code)
	}

	got = listFolders(t, a)
	if !got.Movie.Multi || got.Series.Multi || len(got.Movie.Folders) != 2 || got.Movie.Folders[1].Label != "Kids" {
		t.Fatalf("after adding Kids: %+v", got)
	}

	// Rename and change the rule.
	w = folderCall(t, a.handleUpdateLibraryFolder, http.MethodPut, `{"label":"Kids & family","rule":"animation"}`, "1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"rule":"animation"`) || !strings.Contains(w.Body.String(), "family") {
		t.Errorf("update = %d %s", w.Code, w.Body.String())
	}
	w = folderCall(t, a.handleUpdateLibraryFolder, http.MethodPut, `{"rule":"romance"}`, "1")
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad rule = %d", w.Code)
	}
	// Order: every extra folder once.
	w = folderCall(t, a.handleOrderLibraryFolders, http.MethodPut, `{"media":"movie","ids":[]}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("an order missing a folder = %d", w.Code)
	}
	w = folderCall(t, a.handleOrderLibraryFolders, http.MethodPut, `{"media":"movie","ids":[1]}`, "")
	if w.Code != http.StatusOK {
		t.Errorf("order = %d %s", w.Code, w.Body.String())
	}

	// The default can't move into an extra folder.
	w = putPaths(t, a, map[string]any{"movies": filepath.Join(kidsPath)})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "overlaps") {
		t.Errorf("default onto an extra folder = %d %s", w.Code, w.Body.String())
	}

	// A folder holding a title can't be removed, and says what to do.
	if _, err := st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, library_folder_id) VALUES (1, 862, 'Toy Story', ?)`, kids.ID); err != nil {
		t.Fatal(err)
	}
	w = folderCall(t, a.handleRemoveLibraryFolder, http.MethodDelete, "", "1")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "move them") {
		t.Errorf("remove in use = %d %s", w.Code, w.Body.String())
	}
	if got := listFolders(t, a); got.Movie.Folders[1].Titles != 1 {
		t.Errorf("titles in Kids = %d", got.Movie.Folders[1].Titles)
	}
	if _, err := st.DB().Exec(`UPDATE movies SET library_folder_id = 0`); err != nil {
		t.Fatal(err)
	}
	if w = folderCall(t, a.handleRemoveLibraryFolder, http.MethodDelete, "", "1"); w.Code != http.StatusNoContent {
		t.Errorf("remove = %d %s", w.Code, w.Body.String())
	}
	if w = folderCall(t, a.handleRemoveLibraryFolder, http.MethodDelete, "", "1"); w.Code != http.StatusNotFound {
		t.Errorf("remove twice = %d", w.Code)
	}
	if got := listFolders(t, a); got.Movie.Multi {
		t.Error("Movies still has several folders after the removal")
	}

	// Moving needs the mover; without one it says so rather than half-working.
	if w = folderCall(t, a.handleMoveMovieFolder, http.MethodPost, `{"folder_id":0}`, "1"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("move without a mover = %d", w.Code)
	}
}

// Without the folders service (tools, older tests) the routes answer 503 and the list is
// the one default folder.
func TestLibraryFoldersAPIUnwired(t *testing.T) {
	a := pathsAPI(t)
	if w := folderCall(t, a.handleAddLibraryFolder, http.MethodPost, `{}`, ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("add = %d", w.Code)
	}
	if got := listFolders(t, a); got.Movie.Multi || len(got.Movie.Folders) != 1 || got.Movie.Folders[0].Path != "/lib/movies" {
		t.Errorf("list = %+v", got)
	}
}

// Import & migrate reads a Radarr's root folders and pairs them with Arrmada's: here the
// main Movies folder by its name, and Kids as one Arrmada doesn't have yet.
func TestArrRootsAPI(t *testing.T) {
	a, _, base := foldersAPI(t)
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"path":"/data/movies"},{"path":"/data/kids"}]`))
		case "/api/v3/movie":
			_, _ = w.Write([]byte(`[{"rootFolderPath":"/data/movies"},{"rootFolderPath":"/data/kids"},{"rootFolderPath":"/data/kids"}]`))
		}
	}))
	defer radarr.Close()
	w := folderCall(t, a.handleArrRoots, http.MethodPost, `{"app":"radarr","url":`+jsonString(radarr.URL)+`,"api_key":"k"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("roots = %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Media string `json:"media"`
		Roots []struct {
			Path      string `json:"path"`
			Titles    int    `json:"titles"`
			FolderID  *int64 `json:"folder_id"`
			MatchedBy string `json:"matched_by"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Media != "movie" || len(got.Roots) != 2 {
		t.Fatalf("answer = %+v", got)
	}
	kids, movies := got.Roots[0], got.Roots[1]
	if kids.Titles != 2 || kids.FolderID != nil {
		t.Errorf("kids = %+v, want 2 titles and no Arrmada folder yet", kids)
	}
	if movies.FolderID == nil || *movies.FolderID != 0 || movies.MatchedBy != "name" {
		t.Errorf("movies = %+v, want the main folder (%s) by name", movies, filepath.Join(base, "movies"))
	}
	if w := folderCall(t, a.handleArrRoots, http.MethodPost, `{"app":"radarr","url":`+jsonString(radarr.URL)+`,"api_key":"nope"}`, ""); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "API key") {
		t.Errorf("a bad key = %d %s", w.Code, w.Body.String())
	}
	if w := folderCall(t, a.handleArrRoots, http.MethodPost, `{"app":"lidarr","url":"http://x","api_key":"k"}`, ""); w.Code != http.StatusBadRequest {
		t.Errorf("lidarr = %d", w.Code)
	}
}
