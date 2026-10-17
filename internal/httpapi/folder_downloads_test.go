package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

// A library folder's own downloads folder (GitHub #11), end to end through the API: set,
// listed, refused where it would mix with a library or the data folder, cleared.
func TestFolderDownloadsAPI(t *testing.T) {
	a, _, base := foldersAPI(t)
	a.deps.Folders.SetSettingWriter(a.deps.Settings.Set)
	ctx := context.Background()
	kids, err := a.deps.Folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaMovie, Path: filepath.Join(base, "kids"), Label: "Kids"})
	if err != nil {
		t.Fatal(err)
	}
	disk2 := filepath.Join(base, "disk2", "downloads")
	if err := os.MkdirAll(disk2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "a-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	put := func(body string) (int, libfolders.DownloadsResult, string) {
		w := folderCall(t, a.handleSetFolderDownloads, http.MethodPut, body, "")
		var res libfolders.DownloadsResult
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, res, w.Body.String()
	}
	body := func(media string, id int64, p string) string {
		b, _ := json.Marshal(map[string]any{"media": media, "id": id, "path": p})
		return string(b)
	}

	// An extra folder and a default folder each get one; the list carries them.
	if code, res, raw := put(body("movie", kids.ID, disk2)); code != http.StatusOK || res.Folder.Downloads != disk2 || res.Warning != "" {
		t.Fatalf("set Kids downloads = %d %s", code, raw)
	}
	if code, res, raw := put(body("series", 0, disk2)); code != http.StatusOK || res.Folder.Downloads != disk2 || !res.Folder.Default {
		t.Fatalf("set TV downloads = %d %s", code, raw)
	}
	if got := a.deps.Settings.Get(ctx, libroots.KeyTVDownloads, ""); got != disk2 {
		t.Errorf("TV downloads setting = %q, want %q", got, disk2)
	}
	got := listFolders(t, a)
	if got.Movie.Folders[1].Downloads != disk2 || got.Movie.Folders[0].Downloads != "" || got.Series.Folders[0].Downloads != disk2 {
		t.Errorf("listed downloads = %+v / %+v", got.Movie.Folders, got.Series.Folders)
	}

	// Refusals, each before anything is saved.
	refused := map[string]string{
		"relative":               "downloads",
		"missing":                filepath.Join(base, "nope"),
		"a file":                 filepath.Join(base, "a-file"),
		"inside a library":       filepath.Join(base, "movies"),
		"inside an extra folder": filepath.Join(base, "kids"),
		"around a library":       base,
		"the data folder":        filepath.Join(base, "data"),
		"the container data dir": "/data",
	}
	if err := os.MkdirAll(filepath.Join(base, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, p := range refused {
		if code, _, raw := put(body("movie", 0, p)); code != http.StatusBadRequest {
			t.Errorf("%s (%s): %d %s, want 400", name, p, code, raw)
		}
	}
	if v := a.deps.Settings.Get(ctx, libroots.KeyMoviesDownloads, ""); v != "" {
		t.Errorf("a refused folder was saved: %q", v)
	}
	// Unknown folder or library.
	if code, _, _ := put(body("movie", 999, disk2)); code != http.StatusNotFound {
		t.Errorf("unknown folder = %d, want 404", code)
	}
	if code, _, _ := put(body("music", 0, disk2)); code != http.StatusBadRequest {
		t.Errorf("music = %d, want 400", code)
	}

	// A library folder can no longer be added inside, or saved onto, a downloads folder.
	if _, err := a.deps.Folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: disk2}); err == nil {
		t.Error("an extra library folder was added on a downloads folder")
	}
	if w := putPaths(t, a, map[string]any{"movies": disk2}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "downloads folder") {
		t.Errorf("default Movies folder onto a downloads folder = %d %s, want 400", w.Code, w.Body.String())
	}
	// The Downloads folder itself may be the same folder.
	if w := putPaths(t, a, map[string]any{"downloads": disk2}); w.Code != http.StatusOK {
		t.Errorf("Downloads onto a library folder's downloads folder = %d %s, want 200", w.Code, w.Body.String())
	}

	// Cleared: back to the Downloads folder.
	if code, res, raw := put(body("movie", kids.ID, "")); code != http.StatusOK || res.Folder.Downloads != "" {
		t.Fatalf("clear = %d %s", code, raw)
	}
}

// A downloads folder cleared, replaced, or removed with its library folder still holds
// torrents seeding there: manual import, Needs you and Import into… can still read them.
// A folder that was never a downloads folder stays refused.
func TestFormerFolderDownloadsStayImportable(t *testing.T) {
	a, _, base := foldersAPI(t)
	a.deps.Folders.SetSettingWriter(a.deps.Settings.Set)
	ctx := context.Background()
	kids, err := a.deps.Folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaMovie, Path: filepath.Join(base, "kids")})
	if err != nil {
		t.Fatal(err)
	}
	anime, err := a.deps.Folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: filepath.Join(base, "anime")})
	if err != nil {
		t.Fatal(err)
	}
	dir := func(p string) string {
		if err := os.MkdirAll(filepath.Join(base, p, "Film.2020"), 0o755); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(base, p)
	}
	disk2, disk3, disk4, never := dir("disk2"), dir("disk3"), dir("disk4"), dir("never")
	for _, s := range []struct {
		id   int64
		m, p string
	}{{kids.ID, "movie", disk2}, {kids.ID, "movie", disk3}, {kids.ID, "movie", ""}, {anime.ID, "series", disk4}} {
		if _, err := a.deps.Folders.SetDownloads(ctx, s.m, s.id, s.p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.deps.Folders.Remove(ctx, anime.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{disk2, disk3, disk4} { // replaced, cleared, removed with its folder
		if _, err := a.checkImportPath(ctx, filepath.Join(p, "Film.2020")); err != nil {
			t.Errorf("a download left in former downloads folder %s refused: %v", p, err)
		}
	}
	if _, err := a.checkImportPath(ctx, filepath.Join(never, "Film.2020")); err == nil {
		t.Error("a folder that was never a downloads folder was accepted")
	}
	// Nothing saves there any more.
	if got := a.roots().DownloadFolders(ctx); len(got) != 1 {
		t.Errorf("DownloadFolders = %+v, want only the Downloads folder", got)
	}
}

// Every reader of "the downloads folder" sees the library folders' own ones: manual import
// accepts them, the health and disk panels list them (once each), and the download client
// path check judges a library against its own downloads folder.
func TestFolderDownloadsConsumers(t *testing.T) {
	a, _, base := foldersAPI(t)
	ctx := context.Background()
	own := filepath.Join(base, "disk2", "downloads")
	if err := os.MkdirAll(filepath.Join(own, "Film.2020"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Settings.Set(ctx, libroots.KeyMoviesDownloads, own); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Settings.Set(ctx, libroots.KeyTVDownloads, own); err != nil { // shared by both
		t.Fatal(err)
	}

	// Manual import and Import into… read from it.
	if _, err := a.checkImportPath(ctx, filepath.Join(own, "Film.2020")); err != nil {
		t.Errorf("a download in a library folder's downloads folder refused: %v", err)
	}
	// The health panel checks it — listed once although two folders use it.
	n := 0
	for _, f := range a.healthFolders(ctx, false, false) {
		if f.Path == own {
			n++
			if !health.IsDownloads(f) || f.Role != "downloads-movies" {
				t.Errorf("listed as %+v", f)
			}
		}
	}
	if n != 1 {
		t.Errorf("listed %d times, want once", n)
	}
	// The same check finds it gone.
	if err := os.RemoveAll(own); err != nil {
		t.Fatal(err)
	}
	res := health.LibraryFoldersCheck(a.libraryState, nil).Run(ctx)
	found := false
	for _, f := range res {
		if f.Key == "library.downloads-movies" {
			found = true
		}
	}
	if !found {
		t.Errorf("a missing downloads folder wasn't flagged: %+v", res)
	}

	env := DownloadPathEnv(ctx, a.roots(), a.deps.Config.DataDir)
	if env.FolderDownloads[filepath.Clean(a.deps.Config.MoviesDir)] != own || env.FolderDownloads[filepath.Clean(a.deps.Config.TVDir)] != own {
		t.Errorf("path env = %+v", env.FolderDownloads)
	}
}

// A torrent saved to a library folder's own downloads folder is still Arrmada's when a
// grab knows its hash; a stranger's there can be imported, and imports as a hardlink.
func TestUnmanagedInFolderDownloads(t *testing.T) {
	e := newUnmanagedEnv(t)
	ctx := context.Background()
	own := filepath.Join(filepath.Dir(e.a.deps.Config.MoviesDir), "movies-downloads")
	src := filepath.Join(own, "Some.Film.2001.1080p.BluRay-GRP")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.a.deps.Settings.Set(ctx, libroots.KeyMoviesDownloads, own); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(src, "Some.Film.2001.1080p.BluRay-GRP.mkv")
	f, err := os.Create(video)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(60 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := e.st.DB().Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, status) VALUES (1, 'Ours', 'x', 'movie', ?, 'grabbed')`, ours); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Some Film', 2001, 1, 'announced')`); err != nil {
		t.Fatal(err)
	}
	e.set(
		doneTorrent(ours, "Ours.2020.1080p", "linux", filepath.Join(own, "ours")),
		doneTorrent(strangerA, "Some.Film.2001.1080p.BluRay-GRP", "", src),
	)
	if got, _ := e.list(t); fmt.Sprint(got) != fmt.Sprint([]string{strangerA}) {
		t.Fatalf("unmanaged = %v, want only the stranger (Arrmada's own grab is recognised by its hash)", got)
	}
	j := e.importJob(t, e.post(t, e.a.handleImportUnmanaged, "/", strangerA, `{"kind":"movie","target_id":1}`))
	if j.Status != jobs.StatusSucceeded {
		t.Fatalf("import job = %+v", j)
	}
	m, err := e.a.deps.Movies.Get(ctx, 1)
	if err != nil || !m.HasFile {
		t.Fatalf("no file after the import: %+v %v", m, err)
	}
	a, err1 := os.Stat(video)
	b, err2 := os.Stat(m.MovieFilePath)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Errorf("the import isn't a hardlink of the download (%s → %s): %v %v", video, m.MovieFilePath, err1, err2)
	}
}
