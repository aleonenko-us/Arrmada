package download

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A library with its own downloads folder (GitHub #11) gets its torrents there, so the
// download client check judges it against that folder, not the client's default save
// folder: on the same drive as its own folder it's fine, on another it's flagged.
func TestCheckPathsJudgesALibraryByItsOwnDownloads(t *testing.T) {
	defer func(fn func(a, b string) (bool, bool)) { sameDeviceFn = fn }(sameDeviceFn)
	base := t.TempDir()
	dir := func(p string) string {
		full := filepath.Join(base, filepath.FromSlash(p))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		return full
	}
	save := dir("a/downloads")
	movies := dir("a/movies") // same drive as the save folder
	tv := dir("b/tv")         // other drive, no own folder
	kids := dir("b/kids")     // other drive, own folder beside it
	anime := dir("a/anime")   // same drive as the save folder, own folder elsewhere
	kidsDL, animeDL := dir("b/dl"), dir("c/dl")
	// "Drives" are the first path segment under base.
	drive := func(p string) string {
		rel, _ := filepath.Rel(base, p)
		return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	}
	sameDeviceFn = func(a, b string) (bool, bool) { return drive(a) == drive(b), true }

	env := PathEnv{Downloads: save, Libraries: []string{movies, tv, kids, anime},
		FolderDownloads: map[string]string{filepath.Clean(kids): kidsDL, filepath.Clean(anime): animeDL}}
	rep := checkPaths(Client{Name: "qb", Kind: KindQbittorrent}, save, nil, env)
	got := strings.Join(rep.OtherDrive, ",")
	if want := strings.Join([]string{tv, anime}, ","); got != want {
		t.Errorf("other drive = %s, want %s", got, want)
	}
	// SABnzbd never saves into them (torrents only): its jobs are judged against its own
	// folder, so Kids (on b, with a/ as SAB's folder) is on another drive and Anime isn't.
	rep = checkPaths(Client{Name: "sab", Kind: KindSABnzbd}, save, nil, env)
	got = strings.Join(rep.OtherDrive, ",")
	if want := strings.Join([]string{tv, kids}, ","); got != want {
		t.Errorf("usenet other drive = %s, want %s", got, want)
	}
}

// An add with a save path is explicitly manual (autoTMM=false): with qBittorrent's default
// mode on Automatic, a torrent added without it is auto-managed and saves to the
// category's folder, ignoring savepath. An add without a save path leaves the mode alone.
func TestQBittorrentAddWithSavePathIsManual(t *testing.T) {
	var mu sync.Mutex
	var got []string
	var logins int32
	srv := qbitTestServer(t, &logins, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/add" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("form: %v", err)
		}
		mu.Lock()
		got = append(got, r.FormValue("savepath")+"|"+r.FormValue("autoTMM"))
		mu.Unlock()
		fmt.Fprint(w, "Ok.")
	})
	q := NewQBittorrent()
	dc := Client{ID: 1, URL: srv.URL}
	for _, sp := range []string{"/storage/disk2/downloads", ""} {
		if err := q.Add(context.Background(), dc, AddRequest{URL: "magnet:?xt=urn:btih:abc", Category: "arrmada", SavePath: sp}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "/storage/disk2/downloads|false" || got[1] != "|" {
		t.Errorf("adds = %q", got)
	}
}

// SABnzbd's leftover guard never deletes a job folder that is or holds a library folder's
// own downloads folder, where torrents seed — but a downloads folder that is (or holds)
// the Downloads folder doesn't stop tidying inside it.
func TestLeftoverGuardSparesFolderDownloads(t *testing.T) {
	dl := t.TempDir()
	for _, d := range []string{"usenet/complete/Job", "usenet/complete/Odd/seeding", "other"} {
		if err := os.MkdirAll(filepath.Join(dl, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := []string{dl, filepath.Join(dl, "usenet/complete")}
	g := leftoverGuard{roots: roots, protected: roots,
		noHold: []string{dl, filepath.Join(dl, "usenet/complete/Odd/seeding")}}
	if _, err := g.check(filepath.Join(dl, "usenet/complete/Job")); err != nil {
		t.Errorf("an ordinary job folder refused: %v", err)
	}
	for _, p := range []string{
		filepath.Join(dl, "usenet/complete/Odd"),         // holds one
		filepath.Join(dl, "usenet/complete/Odd/seeding"), // is one
	} {
		if _, err := g.check(p); err == nil {
			t.Errorf("%s was cleared for deleting", p)
		}
	}
	env := PathEnv{FolderDownloads: map[string]string{"/m": "/x/dl", "/k": "/x/dl/", "/t": "/y/dl"}}
	if got := env.ownDownloads(); len(got) != 2 {
		t.Errorf("ownDownloads = %v, want each folder once", got)
	}
}
