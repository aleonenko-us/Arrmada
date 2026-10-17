package download

import (
	"os"
	"path/filepath"
	"strings"
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
	rep := checkPaths(Client{Name: "qb"}, save, nil, env)
	got := strings.Join(rep.OtherDrive, ",")
	if want := strings.Join([]string{tv, anime}, ","); got != want {
		t.Errorf("other drive = %s, want %s", got, want)
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
