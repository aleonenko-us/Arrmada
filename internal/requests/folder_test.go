package requests

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// foldersMovies is the in-memory library with the add-with-options the approve uses when
// staff picked a folder.
type foldersMovies struct {
	*fakeMovies
	folder *int64
}

func (f *foldersMovies) AddWith(ctx context.Context, tmdbID int, o movies.AddOptions) (movies.Movie, error) {
	f.folder = o.FolderID
	m, err := f.Add(ctx, tmdbID, o.QualityProfile, o.Monitored)
	if o.FolderID != nil {
		m.LibraryFolderID = *o.FolderID
	}
	return m, err
}

// Approving with a folder adds the film to that folder; approving without one leaves the
// choice to the rules (the plain add, as before folders).
func TestApproveCarriesTheChosenFolder(t *testing.T) {
	s, _, _ := quietFixture(t)
	fm := &foldersMovies{fakeMovies: newFakeMovies()}
	s.movies = fm
	ctx := context.Background()

	req, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 862, Title: "Toy Story", RequestedBy: 7}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kids := int64(3)
	if _, err := s.Approve(ctx, req.ID, ApproveOptions{DecidedBy: 1, FolderID: &kids}); err != nil {
		t.Fatal(err)
	}
	if fm.folder == nil || *fm.folder != kids {
		t.Fatalf("folder passed = %v, want %d", fm.folder, kids)
	}

	fm.folder = nil
	other, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 949, Title: "Heat", RequestedBy: 7}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, other.ID, ApproveOptions{DecidedBy: 1}); err != nil {
		t.Fatal(err)
	}
	if fm.folder != nil {
		t.Errorf("an approve with no folder chose one: %v", *fm.folder)
	}
	if _, ok := fm.byTMDB[949]; !ok {
		t.Error("the plain approve didn't add the film")
	}
}
