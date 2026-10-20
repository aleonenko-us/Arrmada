package libfolders

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A film imported as a hardlink out of its folder's own downloads folder (GitHub #11) and
// then moved to another folder: the move takes the library file, never the download, so
// the torrent keeps seeding from where it is — on one filesystem or across two — and the
// downloads folders themselves are untouched. Later grabs follow the film to the new
// folder's downloads folder (the folder its files are in now).
func TestMoveLeavesASeedingDownloadInPlace(t *testing.T) {
	for _, cross := range []bool{false, true} {
		name := "rename"
		if cross {
			name = "copy"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.withSettings(t)
			ctx := context.Background()
			kids := f.add(t, MediaMovie, "kids", "")
			moviesDL := mkdir(t, filepath.Join(f.base, "movies-dl"))
			kidsDL := mkdir(t, filepath.Join(f.base, "kids-dl"))
			if _, err := f.svc.SetDownloads(ctx, MediaMovie, DefaultID, moviesDL); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, kidsDL); err != nil {
				t.Fatal(err)
			}
			seeding := writeFile(t, filepath.Join(moviesDL, "Heat.1995.1080p", "Heat.1995.1080p.mkv"), "the film")
			dir := filepath.Join(f.movies, "Heat (1995)")
			lib := filepath.Join(dir, "Heat (1995).mkv")
			mkdir(t, dir)
			if err := os.Link(seeding, lib); err != nil {
				t.Skipf("no hardlinks here: %v", err)
			}
			if _, err := f.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, has_file, movie_file_path) VALUES (1, 949, 'Heat', 1995, 1, ?)`, lib); err != nil {
				t.Fatal(err)
			}
			if got := f.roots.DownloadsFor(ctx, MediaMovie, ""); got != moviesDL {
				t.Fatalf("before: grabs save to %q", got)
			}
			m, _ := f.mover(t, filepath.Join(f.base, "bin"))
			if cross {
				m.Rename = func(oldpath, newpath string) error {
					if oldpath == dir {
						return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
					}
					return os.Rename(oldpath, newpath)
				}
			}
			if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, seeding); got != "the film" {
				t.Errorf("the seeding download holds %q after the move", got)
			}
			moved := filepath.Join(kids.Path, "Heat (1995)", "Heat (1995).mkv")
			if got := readFile(t, moved); got != "the film" {
				t.Errorf("the moved film holds %q", got)
			}
			a, _ := os.Stat(seeding)
			b, _ := os.Stat(moved)
			if same := os.SameFile(a, b); same == cross {
				// A rename keeps the link; a copy onto another drive can't.
				t.Errorf("library file shares the download's data: %v, want %v", same, !cross)
			}
			// The film lives in Kids now: new grabs and upgrades save beside it.
			owner, ok := f.svc.Owner(ctx, MediaMovie, moved)
			if !ok || owner.ID != kids.ID {
				t.Fatalf("owner = %+v %v", owner, ok)
			}
			if got := f.roots.DownloadsFor(ctx, MediaMovie, owner.Path); got != kidsDL {
				t.Errorf("after the move grabs save to %q, want %q", got, kidsDL)
			}
			for _, d := range []string{moviesDL, kidsDL} {
				if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
					t.Errorf("downloads folder %s touched: %v", d, err)
				}
			}
		})
	}
}
