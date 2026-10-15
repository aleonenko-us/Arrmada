package movies

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// genreMeta is fakeMeta whose details carry genres and a certification, for the rules.
type genreMeta struct {
	fakeMeta
	details map[int]metadata.MovieDetails
}

func (g genreMeta) GetMovie(ctx context.Context, id int) (*metadata.MovieDetails, error) {
	if d, ok := g.details[id]; ok {
		return &d, nil
	}
	return g.fakeMeta.GetMovie(ctx, id)
}

// foldersFixture is a movies service over a default Movies folder and an extra Kids
// folder (rule: kids ratings), all temp dirs.
func foldersFixture(t *testing.T) (*Service, *libfolders.Service, string, libfolders.Folder) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	base := t.TempDir()
	movies, kidsDir := filepath.Join(base, "movies"), filepath.Join(base, "kids")
	for _, d := range []string{movies, kidsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := libroots.New(nil, config.Config{MoviesDir: movies})
	folders := libfolders.New(st.DB(), roots, filepath.Join(base, "data"))
	roots.WithExtras(folders.Extras)
	kids, err := folders.Add(context.Background(), libfolders.AddInput{Media: libfolders.MediaMovie, Path: kidsDir, Label: "Kids", Rule: libfolders.RuleKids})
	if err != nil {
		t.Fatal(err)
	}
	films := []metadata.MovieResult{{TMDBID: 10, Title: "Movie", Year: 2010}, {TMDBID: 20, Title: "Other", Year: 2020}, {TMDBID: 862, Title: "Toy Story", Year: 1995}}
	meta := genreMeta{fakeMeta: fakeMeta{films: films}, details: map[int]metadata.MovieDetails{
		862: {MovieResult: films[2], Genres: []string{"Animation", "Family"}, Certification: "G", Status: "Released"},
		20:  {MovieResult: films[1], Genres: []string{"Drama"}, Certification: "R", Status: "Released"},
	}}
	svc := NewService(st.DB(), meta, nil, movies, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return movies })
	svc.SetFolders(folders)
	var fc fileCounter
	fc.install(svc)
	svc.SetOutbox(&recordingOutbox{})
	return svc, folders, movies, kids
}

// A new film goes where its rules say, or where staff chose; with no match, the default.
// Its imports then land in that folder.
func TestAddPicksTheFolderAndImportsFollow(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()

	toy, err := svc.AddWith(ctx, 862, AddOptions{QualityProfile: "hd", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if toy.LibraryFolderID != kids.ID {
		t.Errorf("a G film went to folder %d, want Kids %d", toy.LibraryFolderID, kids.ID)
	}
	other, err := svc.AddWith(ctx, 20, AddOptions{QualityProfile: "hd", Monitored: true})
	if err != nil || other.LibraryFolderID != libfolders.DefaultID {
		t.Fatalf("an R film = %+v, %v; want the default folder", other.LibraryFolderID, err)
	}
	// Staff's choice beats the rules, and must name a real folder.
	def := libfolders.DefaultID
	if err := svc.repo.Delete(ctx, toy.ID); err != nil {
		t.Fatal(err)
	}
	chosen, err := svc.AddWith(ctx, 862, AddOptions{QualityProfile: "hd", FolderID: &def})
	if err != nil || chosen.LibraryFolderID != libfolders.DefaultID {
		t.Fatalf("a chosen folder = %d, %v", chosen.LibraryFolderID, err)
	}
	bad := int64(99)
	if _, err := svc.AddWith(ctx, 10, AddOptions{QualityProfile: "hd", FolderID: &bad}); !errors.Is(err, ErrUnknownFolder) {
		t.Errorf("an unknown folder: %v", err)
	}
	if id, err := svc.SuggestFolder(ctx, 862); err != nil || id != kids.ID {
		t.Errorf("suggest = %d, %v", id, err)
	}

	// The import's naming carries the film's folder, so a new film folder is made there.
	if err := svc.repo.SetLibraryFolder(ctx, chosen.ID, kids.ID); err != nil {
		t.Fatal(err)
	}
	n, err := svc.ImportNaming(ctx, chosen.ID, -1, "", "Toy.Story.1995.1080p.BluRay.x264-GRP")
	if err != nil {
		t.Fatal(err)
	}
	if n.Track.Root != kids.Path {
		t.Errorf("track root = %q, want the Kids folder %q", n.Track.Root, kids.Path)
	}
	m, _ := svc.Get(ctx, chosen.ID)
	if got := svc.TrackTarget(m, nil, defaultVersionRow(m), "Toy.Story.1995.1080p.mkv", ".mkv"); !strings.HasPrefix(got, kids.Path+string(filepath.Separator)) {
		t.Errorf("target %q isn't in the Kids folder", got)
	}
	// A film whose file is already somewhere stays with its file (no split across folders).
	f := writeFixture(t, filepath.Join(movies, "Toy Story (1995)", "Toy Story.mkv"))
	if err := svc.setDefaultFile(ctx, chosen.ID, f); err != nil {
		t.Fatal(err)
	}
	m, _ = svc.Get(ctx, chosen.ID)
	if got := svc.rootFor(m); got != movies {
		t.Errorf("root for a film with a file in the default folder = %q", got)
	}
}

// A scan walks every Movies folder, and each film it adds belongs to the folder it was
// found in — whatever the rules would say. Unmatched folders remember their folder.
func TestScanWalksEveryFolder(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()
	videoFixture(t, filepath.Join(movies, "Other (2020)", "Other.2020.1080p.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Movie (2010)", "Movie.2010.1080p.mkv"))
	videoFixture(t, filepath.Join(kids.Path, "Mystery Film (2001)", "Mystery.Film.2001.mkv"))
	// The bin and other hidden folders are never scanned, in any folder.
	videoFixture(t, filepath.Join(kids.Path, ".arrmada-recycle", "Other (2020)", "old.mkv"))

	res, err := svc.ScanLibrary(ctx, "", ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 {
		t.Fatalf("imported %d, want one per folder (%+v)", res.Imported, res)
	}
	movie, err := svc.GetByTMDB(ctx, 10)
	if err != nil || movie.LibraryFolderID != kids.ID {
		t.Errorf("Movie (found in Kids) = folder %d, %v", movie.LibraryFolderID, err)
	}
	other, err := svc.GetByTMDB(ctx, 20)
	if err != nil || other.LibraryFolderID != libfolders.DefaultID {
		t.Errorf("Other (found in the default) = folder %d, %v", other.LibraryFolderID, err)
	}
	un, err := svc.LastUnmatched(ctx)
	if err != nil || len(un) != 1 || un[0].Root != kids.Path {
		t.Fatalf("unmatched = %+v, %v; want the Kids folder's mystery", un, err)
	}
	// The manual pick imports from the folder it was found in — and only from a real one.
	if err := svc.ImportFolderAs(ctx, t.TempDir(), un[0].Folder, 20); err == nil {
		t.Error("imported from a folder that isn't a Movies folder")
	}
}

// The missing-file check judges each folder on its own: a Kids share that's down never
// marks its films missing.
func TestMissingCheckPerFolder(t *testing.T) {
	svc, _, movies, kids := foldersFixture(t)
	ctx := context.Background()
	videoFixture(t, filepath.Join(movies, "Other (2020)", "Other.2020.1080p.mkv"))
	up := filepath.Join(kids.Path, "Up (2009)", "Up.mkv")
	m, err := svc.repo.Create(ctx, Movie{TMDBID: 14160, Title: "Up", Year: 2009, LibraryFolderID: kids.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.setDefaultFile(ctx, m.ID, up); err != nil {
		t.Fatal(err)
	}
	// The Kids folder is empty — what an unmounted share looks like.
	out, err := svc.CheckMissingFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Flagged != 0 || !strings.Contains(out.Refused, kids.Path) {
		t.Errorf("sweep = %+v, want nothing flagged and the Kids folder named", out)
	}
	if got := anchorOf([]string{movies, kids.Path}, up); got != kids.Path {
		t.Errorf("anchor = %q", got)
	}
}
