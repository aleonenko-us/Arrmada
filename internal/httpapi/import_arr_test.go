package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/arrimport"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

const arrTestKey = "radarr-key-never-echoed"

// fakeRadarr is a Radarr with one root folder (/arr/movies) and two films: Heat with its
// file, Ronin wanted.
func fakeRadarr(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != arrTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/system/status":
			_, _ = w.Write([]byte(`{"appName":"Radarr","instanceName":"Radarr 4K","version":"6.4.4"}`))
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":4,"name":"HD-1080p","items":[{"quality":{"resolution":1080},"items":[],"allowed":true}]}]`))
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"path":"/arr/movies","freeSpace":1}]`))
		case "/api/v3/movie":
			_, _ = w.Write([]byte(`[
				{"id":1,"title":"Heat","year":1995,"tmdbId":949,"qualityProfileId":4,"monitored":true,"minimumAvailability":"released",
				 "path":"/arr/movies/Heat (1995)","rootFolderPath":"/arr/movies","hasFile":true,
				 "movieFile":{"id":1,"movieId":1,"relativePath":"Heat (1995).mkv","path":"/arr/movies/Heat (1995)/Heat (1995).mkv","size":2048}},
				{"id":2,"title":"Ronin","year":1998,"tmdbId":8195,"qualityProfileId":4,"monitored":true,"minimumAvailability":"released",
				 "path":"/arr/movies/Ronin (1998)","rootFolderPath":"/arr/movies","hasFile":false}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type arrMeta struct{ available bool }

func (m arrMeta) Available() bool { return m.available }
func (arrMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (arrMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, metadata.ErrNotFound
}
func (arrMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	return &metadata.MovieDetails{MovieResult: metadata.MovieResult{TMDBID: id, Title: "Film " + strconv.Itoa(id), Year: 1995}, Status: "Released"}, nil
}

func arrAPI(t *testing.T, metaReady bool) (*api, string, *fakeJobs, string) {
	t.Helper()
	a, st, base := foldersAPI(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mv := movies.NewService(st.DB(), arrMeta{available: metaReady}, nil, filepath.Join(base, "movies"), "", nil, log)
	mv.SetFolders(a.deps.Folders)
	a.deps.Movies = mv
	a.deps.Quality = quality.NewService(st.DB())
	sp, err := a.deps.Quality.Create(context.Background(), quality.StoredProfile{MediaType: quality.MediaMovie, Name: "HD-1080p", AllowedResolutions: []string{"1080p"}})
	if err != nil {
		t.Fatal(err)
	}
	fj := newFakeJobs(false)
	a.deps.Jobs = fj
	heat := filepath.Join(base, "movies", "Heat (1995)", "Heat (1995).mkv")
	if err := os.MkdirAll(filepath.Dir(heat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(heat, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, fakeRadarr(t).URL, fj, "custom:" + strconv.FormatInt(sp.ID, 10)
}

func arrBody(url, key, extra string) string {
	b := `{"app":"radarr","url":` + jsonString(url) + `,"api_key":` + jsonString(key)
	if extra != "" {
		b += "," + extra
	}
	return b + "}"
}

func TestArrPreviewAPI(t *testing.T) {
	a, url, _, profile := arrAPI(t, true)

	w := folderCall(t, a.handleArrPreview, http.MethodPost, arrBody(url, "wrong-key", ""), "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "API key was refused") {
		t.Fatalf("wrong key = %d %s", w.Code, w.Body.String())
	}

	w = folderCall(t, a.handleArrPreview, http.MethodPost, arrBody(url, arrTestKey, ""), "")
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), arrTestKey) {
		t.Fatal("the preview sent the API key back")
	}
	var got struct {
		Preview  arrimport.Preview   `json:"preview"`
		Roots    []arrimport.Mapping `json:"roots"`
		PathMaps []arrimport.PathMap `json:"path_maps"`
		Targets  []arrimport.Target  `json:"targets"`
		NotReady string              `json:"not_ready"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// /arr/movies is Arrmada's Movies folder by name, so its path map is suggested and Heat's
	// file is found through it.
	if len(got.Roots) != 1 || got.Roots[0].FolderID == nil || *got.Roots[0].FolderID != 0 {
		t.Errorf("roots = %+v", got.Roots)
	}
	if len(got.PathMaps) != 1 || got.PathMaps[0].From != "/arr/movies" {
		t.Errorf("path maps = %+v", got.PathMaps)
	}
	c := got.Preview.Counts
	if c.Titles != 2 || c.New != 2 || c.FilesFound != 1 || c.Wanted != 1 || got.Preview.Name != "Radarr 4K" {
		t.Errorf("preview = %+v", got.Preview)
	}
	// The profile of the same name wins over the default 1080p one the app ships with.
	if len(got.Preview.Profiles) != 1 || got.Preview.Profiles[0].Suggested != profile || len(got.Targets) < 2 || got.NotReady != "" {
		t.Errorf("profiles = %+v targets = %+v not_ready=%q", got.Preview.Profiles, got.Targets, got.NotReady)
	}

	// Mapping nothing: the file isn't where Radarr says, and the preview says so.
	w = folderCall(t, a.handleArrPreview, http.MethodPost, arrBody(url, arrTestKey, `"path_maps":[]`), "")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Preview.Counts.NotFound != 1 || len(got.Preview.NotFound) != 1 {
		t.Errorf("unmapped preview = %+v", got.Preview)
	}
	// A broken mapping is refused.
	w = folderCall(t, a.handleArrPreview, http.MethodPost, arrBody(url, arrTestKey, `"path_maps":[{"from":"/arr","to":"relative"}]`), "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("relative path map = %d", w.Code)
	}
}

func TestArrImportAPI(t *testing.T) {
	a, url, fj, profile := arrAPI(t, true)

	for _, bad := range []string{
		`"profiles":{"4":"custom:999"}`,
		`"folders":{"/arr/movies":42}`,
		`"path_maps":[{"from":"/arr","to":""}]`,
	} {
		if w := folderCall(t, a.handleArrImport, http.MethodPost, arrBody(url, arrTestKey, bad), ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", bad, w.Code, w.Body.String())
		}
	}
	if w := folderCall(t, a.handleArrImport, http.MethodPost, arrBody(url, "wrong-key", ""), ""); w.Code != http.StatusBadGateway {
		t.Errorf("wrong key = %d", w.Code)
	}
	if len(fj.submitted) != 0 {
		t.Fatalf("a refused import started a job: %+v", fj.submitted)
	}

	body := arrBody(url, arrTestKey, `"profiles":{"4":`+jsonString(profile)+`},"folders":{"/arr/movies":0},"path_maps":[{"from":"/arr/movies","to":`+jsonString(a.roots().Movies(context.Background()))+`}]`)
	w := folderCall(t, a.handleArrImport, http.MethodPost, body, "")
	if w.Code != http.StatusAccepted || strings.Contains(w.Body.String(), arrTestKey) {
		t.Fatalf("import = %d %s", w.Code, w.Body.String())
	}
	if len(fj.submitted) != 1 || fj.submitted[0].Kind != "import.arr" || fj.submitted[0].Target != "arr" {
		t.Fatalf("submitted = %+v", fj.submitted)
	}
	// One import at a time.
	if w := folderCall(t, a.handleArrImport, http.MethodPost, body, ""); w.Code != http.StatusConflict {
		t.Errorf("second import = %d", w.Code)
	}

	out, err := fj.submitted[0].Fn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(arrimport.Result)
	if res.Imported != 2 || res.FilesAttached != 1 || res.Paced != 1 || res.Name != "Radarr 4K" {
		t.Fatalf("result = %+v", res)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), arrTestKey) {
		t.Fatal("the job result carries the API key")
	}
	heat, err := a.deps.Movies.GetByTMDB(context.Background(), 949)
	if err != nil || !heat.HasFile || heat.QualityProfile != profile {
		t.Errorf("heat = %+v, %v", heat, err)
	}
	if s := arrSummary(res); !strings.Contains(s, "Imported 2 films · 1 file recorded") || !strings.Contains(s, "1 wanted") {
		t.Errorf("summary = %q", s)
	}
}

// Without a TMDB key nothing can be added: the preview says so, the import refuses.
func TestArrImportNeedsMetadata(t *testing.T) {
	a, url, fj, _ := arrAPI(t, false)
	w := folderCall(t, a.handleArrPreview, http.MethodPost, arrBody(url, arrTestKey, ""), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "TMDB key") {
		t.Errorf("preview without metadata = %d %s", w.Code, w.Body.String())
	}
	if w := folderCall(t, a.handleArrImport, http.MethodPost, arrBody(url, arrTestKey, ""), ""); w.Code != http.StatusBadRequest || len(fj.submitted) != 0 {
		t.Errorf("import without metadata = %d", w.Code)
	}
}
