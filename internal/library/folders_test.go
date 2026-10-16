package library

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/libroots"
)

// A film that belongs to an extra Movies folder imports there: its track carries the
// folder as Root, and a new film folder is made under it rather than the default one.
func TestMovieImportLandsInItsOwnFolder(t *testing.T) {
	def, kids := t.TempDir(), t.TempDir()
	im := NewImporter("", quiet())
	im.SetRootFuncs(RootFuncs{Movie: func() string { return def }})

	src := t.TempDir()
	name := "Toy.Story.1995.1080p.BluRay.x264-GRP"
	writeFile(t, filepath.Join(src, name, name+".mkv"), 60<<20)
	res, err := im.ImportAsNamed(MovieTrack{Title: "Toy Story", Year: 1995, Root: kids}, filepath.Join(src, name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.TargetPath, kids+string(filepath.Separator)) {
		t.Errorf("imported to %q, want under the Kids folder %q", res.TargetPath, kids)
	}
	// No Root: the default folder, as always.
	if got := im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995}, "", ".mkv"); !strings.HasPrefix(got, def+string(filepath.Separator)) {
		t.Errorf("target %q isn't under the default folder", got)
	}
	// An existing folder still wins over the root: an upgrade joins the film where it is.
	existing := filepath.Join(def, "Heat (1995)")
	if got := im.MovieTrackTarget(MovieTrack{Title: "Heat", Year: 1995, Root: kids, Folder: existing}, "", ".mkv"); filepath.Dir(got) != existing {
		t.Errorf("target %q left the film's existing folder", got)
	}
	// A Root that isn't there fails like a missing library folder (never created).
	if _, err := im.ImportAsNamed(MovieTrack{Title: "Up", Year: 2009, Root: filepath.Join(kids, "unmounted")}, filepath.Join(src, name)); err == nil {
		t.Error("imported into a folder that isn't there")
	}
}

// InTV fixes the TV folder for one show's episodes and leaves the shared importer alone.
func TestInTVPlacesEpisodesInTheShowsFolder(t *testing.T) {
	def, anime := t.TempDir(), t.TempDir()
	im := NewImporter("", quiet())
	im.SetRootFuncs(RootFuncs{TV: func() string { return def }})

	src := t.TempDir()
	ep := "Frieren.S01E01.1080p.WEB-DL-GRP.mkv"
	writeFile(t, filepath.Join(src, ep), 60<<20)
	got, ok, err := im.InTV(anime).ImportEpisodeInto("", "Frieren", 2023, filepath.Join(src, ep))
	if err != nil || !ok {
		t.Fatalf("import = %v, %v", ok, err)
	}
	if !strings.HasPrefix(got.TargetPath, anime+string(filepath.Separator)) {
		t.Errorf("episode went to %q, want under the Anime folder %q", got.TargetPath, anime)
	}
	if im.TVRoot() != def {
		t.Errorf("the shared importer's TV folder changed to %q", im.TVRoot())
	}
	if im.InTV("") != im {
		t.Error(`InTV("") should be the importer itself`)
	}
}

// An extra folder's recycle bin is named after the folder, not just its library.
func TestRootBinsNameExtraFolders(t *testing.T) {
	movies, kids := t.TempDir(), t.TempDir()
	b := &RootBins{Roots: func() []libroots.Root {
		return []libroots.Root{{Name: "movies", Path: movies}, {Name: "movies", Path: kids, Label: "Kids movies"}}
	}}
	all := b.All()
	if len(all) != 2 || all[0].Label != "Movies" || all[1].Label != "Kids movies" {
		t.Fatalf("bins = %+v", all)
	}
	dir, err := b.For(filepath.Join(kids, "Up (2009)", "Up.mkv"))
	if err != nil || dir != filepath.Join(kids, RecycleDirName) {
		t.Errorf("a Kids file's bin = %q, %v; want the Kids folder's own", dir, err)
	}
}
