package series

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/store"
)

// shelfMeta knows a few shows by title and id.
type shelfMeta struct {
	shows map[int]metadata.SeriesDetails
}

func (m shelfMeta) Available() bool { return true }
func (m shelfMeta) SearchSeries(_ context.Context, q string) ([]metadata.SeriesResult, error) {
	var out []metadata.SeriesResult
	for _, d := range m.shows {
		if parser.TitleKey(d.Title) == parser.TitleKey(q) {
			out = append(out, d.SeriesResult)
		}
	}
	return out, nil
}
func (m shelfMeta) GetSeries(_ context.Context, id int) (*metadata.SeriesDetails, error) {
	d, ok := m.shows[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &d, nil
}

func touchEpisode(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(200 << 20) // sparse
	_ = f.Close()
}

// The TV scan walks every TV folder, each show belonging to the folder it was found in;
// a new show is placed by the rules (Anime → the Anime folder), and its episodes import
// there.
func TestSeriesFoldersScanAndPick(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	base := t.TempDir()
	tv, animeDir := filepath.Join(base, "tv"), filepath.Join(base, "anime")
	for _, d := range []string{tv, animeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := libroots.New(nil, config.Config{TVDir: tv})
	folders := libfolders.New(st.DB(), roots, filepath.Join(base, "data"))
	anime, err := folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: animeDir, Rule: libfolders.RuleAnime})
	if err != nil {
		t.Fatal(err)
	}
	show := func(id int, title string, genres []string, lang string) metadata.SeriesDetails {
		return metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: id, Title: title, Year: 2020}, Genres: genres, OriginalLang: lang,
			Seasons: listing(2)}
	}
	meta := shelfMeta{shows: map[int]metadata.SeriesDetails{
		1: show(1, "Bluey", []string{"Animation", "Kids"}, "en"),
		2: show(2, "Dandadan", []string{"Animation"}, "ja"),
		3: show(3, "Frieren", []string{"Animation"}, "ja"),
	}}
	svc := NewService(st.DB(), meta, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return tv })
	svc.SetFolders(folders)

	// Bluey sits in the default folder, Dandadan in Anime: each stays where it was found,
	// whatever the rules say.
	touchEpisode(t, filepath.Join(tv, "Bluey (2020)", "Season 01", "Bluey - S01E01.mkv"))
	touchEpisode(t, filepath.Join(animeDir, "Dandadan (2020)", "Season 01", "Dandadan - S01E01.mkv"))
	res, err := svc.ScanLibrary(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 {
		t.Fatalf("imported %d, want both folders' shows (%+v)", res.Imported, res)
	}
	for id, want := range map[int]int64{1: libfolders.DefaultID, 2: anime.ID} {
		s, err := svc.GetByTMDB(ctx, id)
		if err != nil || s.LibraryFolderID != want {
			t.Errorf("show %d = folder %d, %v; want %d", id, s.LibraryFolderID, err, want)
		}
		if got := svc.RootFor(ctx, s); (want == anime.ID) != (got == animeDir) {
			t.Errorf("show %d imports into %q", id, got)
		}
	}

	// Added fresh, an anime show goes to the Anime folder; a chosen folder wins.
	fr, err := svc.AddWith(ctx, 3, "hd", AddOptions{Monitored: true})
	if err != nil || fr.LibraryFolderID != anime.ID {
		t.Fatalf("Frieren = folder %d, %v; want Anime %d", fr.LibraryFolderID, err, anime.ID)
	}
	if got := svc.RootFor(ctx, fr); got != animeDir {
		t.Errorf("a new anime show with no files imports into %q", got)
	}
	if id, err := svc.SuggestFolder(ctx, 3); err != nil || id != anime.ID {
		t.Errorf("suggest = %d, %v", id, err)
	}
	bad := int64(42)
	if _, err := svc.AddWith(ctx, 1, "hd", AddOptions{FolderID: &bad}); !errors.Is(err, ErrUnknownFolder) {
		t.Errorf("an add naming an unknown folder: %v", err)
	}
	// The manual pick only imports from a real TV folder.
	if err := svc.ImportFolderAs(ctx, base, "Dandadan (2020)", 2); err == nil {
		t.Error("imported from a folder that isn't a TV folder")
	}
}
