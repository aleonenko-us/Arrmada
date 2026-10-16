package libfolders

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// scans records the media-server scan requests a move makes.
type scans struct {
	mu   sync.Mutex
	dirs []string
}

func (s *scans) add(kind, dir string) {
	s.mu.Lock()
	s.dirs = append(s.dirs, kind+":"+dir)
	s.mu.Unlock()
}

// showFolder is series.ShowFolder (the mover takes it as a func so this package doesn't
// import series).
var seasonDir = regexp.MustCompile(`(?i)^(season[ ._-]?\d+|specials)$`)

func showFolder(p string) string {
	parent := filepath.Dir(p)
	if seasonDir.MatchString(filepath.Base(parent)) {
		return filepath.Dir(parent)
	}
	return parent
}

func (f *fixture) mover(t *testing.T, bin string) (*Mover, *scans) {
	t.Helper()
	sc := &scans{}
	return &Mover{
		DB: f.st.DB(), Folders: f.svc, Bin: library.SingleBin(bin), Scan: sc.add, ShowFolder: showFolder,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, sc
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func gone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s is still there (%v)", p, err)
	}
}

func queryString(t *testing.T, f *fixture, q string, args ...any) string {
	t.Helper()
	var s string
	if err := f.st.DB().QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// filmFixture makes Heat in the default Movies folder: a default file, a second version,
// a subtitle and the database rows that remember them.
func filmFixture(t *testing.T, f *fixture) (dir string) {
	t.Helper()
	dir = filepath.Join(f.movies, "Heat (1995)")
	main := writeFile(t, filepath.Join(dir, "Heat (1995).mkv"), "the film")
	v4k := writeFile(t, filepath.Join(dir, "Heat (1995) - 4K.mkv"), "the film in 4K")
	srt := writeFile(t, filepath.Join(dir, "Heat (1995).en.srt"), "subtitles")
	writeFile(t, filepath.Join(dir, "poster.jpg"), "art")
	db := f.st.DB()
	for _, q := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO movies (id, tmdb_id, title, year, has_file, movie_file_path) VALUES (1, 949, 'Heat', 1995, 1, ?)`, []any{main}},
		{`INSERT INTO movie_versions (movie_id, label, has_file, file_path) VALUES (1, '4K', 1, ?)`, []any{v4k}},
		{`INSERT INTO subtitle_files (media_key, kind, movie_id, video_path, path, lang, source, created_at) VALUES ('movie:1', 'movie', 1, ?, ?, 'en', 'provider', 1)`, []any{main, srt}},
		{`INSERT INTO convert_library (path, media_type, movie_id) VALUES (?, 'movie', 1)`, []any{main}},
	} {
		if _, err := db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// On one filesystem a move is a rename: the folder arrives whole, the database follows,
// the film is reassigned and both media-server folders are told.
func TestMoveRenamesOnOneFilesystem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	m, sc := f.mover(t, filepath.Join(f.base, "bin"))

	res, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(kids.Path, "Heat (1995)")
	if res.Method != "rename" || len(res.Moved) != 1 || res.Moved[0].To != dst {
		t.Fatalf("result = %+v", res)
	}
	gone(t, src)
	if got := readFile(t, filepath.Join(dst, "Heat (1995).mkv")); got != "the film" {
		t.Errorf("moved file holds %q", got)
	}
	readFile(t, filepath.Join(dst, "poster.jpg")) // artwork came along
	if got := queryString(t, f, `SELECT movie_file_path FROM movies WHERE id = 1`); got != filepath.Join(dst, "Heat (1995).mkv") {
		t.Errorf("movie path = %q", got)
	}
	if got := queryString(t, f, `SELECT file_path FROM movie_versions WHERE movie_id = 1`); got != filepath.Join(dst, "Heat (1995) - 4K.mkv") {
		t.Errorf("version path = %q", got)
	}
	if got := queryString(t, f, `SELECT path FROM subtitle_files`); got != filepath.Join(dst, "Heat (1995).en.srt") {
		t.Errorf("subtitle path = %q", got)
	}
	if got := queryString(t, f, `SELECT path FROM convert_library`); got != filepath.Join(dst, "Heat (1995).mkv") {
		t.Errorf("convert index path = %q", got)
	}
	if got := queryString(t, f, `SELECT CAST(library_folder_id AS TEXT) FROM movies WHERE id = 1`); got != "1" {
		t.Errorf("folder id = %s", got)
	}
	if got := queryString(t, f, `SELECT detail FROM movie_events WHERE movie_id = 1 AND event = 'moved'`); !strings.Contains(got, "kids") {
		t.Errorf("event = %q", got)
	}
	want := map[string]bool{"movie:" + src: true, "movie:" + dst: true}
	for _, d := range sc.dirs {
		delete(want, d)
	}
	if len(want) > 0 {
		t.Errorf("scans = %v, missing %v", sc.dirs, want)
	}
	// Moving it back is the same move the other way.
	if _, err := m.Move(ctx, MediaMovie, 1, DefaultID, nil); err != nil {
		t.Fatal(err)
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
	if got := queryString(t, f, `SELECT CAST(library_folder_id AS TEXT) FROM movies WHERE id = 1`); got != "0" {
		t.Errorf("folder id after moving back = %s", got)
	}
}

// Across filesystems the folder is copied, every file checked, the copy put in place, the
// database repointed — and only then do the originals go, to the recycle bin.
func TestMoveCopiesAcrossFilesystems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	bin := filepath.Join(f.base, "bin")
	m, _ := f.mover(t, bin)
	// The title folder's own rename fails like a rename between two mounts does; the
	// copy's rename into place (same drive) is real.
	m.rename = func(oldpath, newpath string) error {
		if oldpath == src {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
		return os.Rename(oldpath, newpath)
	}
	res, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Method != "copy" {
		t.Errorf("method = %s, want copy", res.Method)
	}
	dst := filepath.Join(kids.Path, "Heat (1995)")
	for name, body := range map[string]string{"Heat (1995).mkv": "the film", "Heat (1995) - 4K.mkv": "the film in 4K", "Heat (1995).en.srt": "subtitles", "poster.jpg": "art"} {
		if got := readFile(t, filepath.Join(dst, name)); got != body {
			t.Errorf("%s holds %q", name, got)
		}
	}
	gone(t, dst+movingSuffix)
	gone(t, src) // the originals went, and the emptied folder with them
	// ... to the recycle bin, not into thin air.
	if got := readFile(t, filepath.Join(bin, "Heat (1995)", "Heat (1995).mkv")); got != "the film" {
		t.Errorf("binned original holds %q", got)
	}
	if got := queryString(t, f, `SELECT movie_file_path FROM movies WHERE id = 1`); got != filepath.Join(dst, "Heat (1995).mkv") {
		t.Errorf("movie path = %q", got)
	}
}

// A copy that can't be verified leaves the originals and the database exactly as they
// were, and nothing half-copied behind.
func TestMoveCopyFailureLeavesTheOriginals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	m.rename = func(oldpath, newpath string) error {
		if oldpath == src {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
		return os.Rename(oldpath, newpath)
	}
	// A symlink in the folder stops the copy part-way.
	if err := os.Symlink(filepath.Join(src, "poster.jpg"), filepath.Join(src, "zz-link.jpg")); err != nil {
		t.Skip("this system can't make symlinks:", err)
	}
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); err == nil {
		t.Fatal("a move through a symlink went ahead")
	}
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
	gone(t, filepath.Join(kids.Path, "Heat (1995)"))
	gone(t, filepath.Join(kids.Path, "Heat (1995)"+movingSuffix))
	if got := queryString(t, f, `SELECT movie_file_path FROM movies WHERE id = 1`); got != filepath.Join(src, "Heat (1995).mkv") {
		t.Errorf("movie path = %q", got)
	}
	if got := queryString(t, f, `SELECT CAST(library_folder_id AS TEXT) FROM movies WHERE id = 1`); got != "0" {
		t.Errorf("folder id = %s", got)
	}
}

// A show moves by its show folder, season folders and all.
func TestMoveShowFolder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	anime := f.add(t, MediaSeries, "anime", RuleAnime)
	show := filepath.Join(f.tv, "Frieren (2023)")
	e1 := writeFile(t, filepath.Join(show, "Season 01", "Frieren - S01E01.mkv"), "e1")
	e2 := writeFile(t, filepath.Join(show, "Season 02", "Frieren - S02E01.mkv"), "e2")
	db := f.st.DB()
	if _, err := db.Exec(`INSERT INTO series (id, tmdb_id, title) VALUES (1, 209867, 'Frieren')`); err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{e1, e2} {
		if _, err := db.Exec(`INSERT INTO episodes (series_id, season_number, episode_number, has_file, file_path) VALUES (1, ?, 1, 1, ?)`, i+1, p); err != nil {
			t.Fatal(err)
		}
	}
	m, sc := f.mover(t, filepath.Join(f.base, "bin"))
	res, err := m.Move(ctx, MediaSeries, 1, anime.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(anime.Path, "Frieren (2023)")
	if len(res.Moved) != 1 || res.Moved[0].From != show || res.Moved[0].Files != 2 {
		t.Fatalf("moved = %+v, want the one show folder", res.Moved)
	}
	readFile(t, filepath.Join(dst, "Season 02", "Frieren - S02E01.mkv"))
	if got := queryString(t, f, `SELECT file_path FROM episodes WHERE season_number = 2`); got != filepath.Join(dst, "Season 02", "Frieren - S02E01.mkv") {
		t.Errorf("episode path = %q", got)
	}
	if got := queryString(t, f, `SELECT CAST(library_folder_id AS TEXT) FROM series WHERE id = 1`); got != "1" {
		t.Errorf("folder id = %s", got)
	}
	if len(sc.dirs) == 0 || !strings.HasPrefix(sc.dirs[0], "show:") {
		t.Errorf("scans = %v, want show scans", sc.dirs)
	}
}

func TestMoveRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	src := filmFixture(t, f)
	db := f.st.DB()
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))

	// A download on its way in.
	m.Busy = func(context.Context, string, int64) string { return "a download is on its way in" }
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "download") {
		t.Errorf("busy: %v", err)
	}
	m.Busy = nil

	// Convert in the middle of swapping one of its files.
	if _, err := db.Exec(`INSERT INTO convert_swaps (part, final, src, kind, movie_id) VALUES ('p', 'f', 's', 'movie', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("convert swap open: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM convert_swaps`); err != nil {
		t.Fatal(err)
	}

	// The destination already has a folder by that name.
	taken := mkdir(t, filepath.Join(kids.Path, "Heat (1995)"))
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBadPath) || !strings.Contains(err.Error(), "already") {
		t.Errorf("destination taken: %v", err)
	}
	if err := os.Remove(taken); err != nil {
		t.Fatal(err)
	}

	// Another film keeps a file in the same folder.
	if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, has_file, movie_file_path) VALUES (2, 950, 'Heat Extras', 1, ?)`, filepath.Join(src, "extras.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "Heat Extras") {
		t.Errorf("shared folder: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM movies WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	// A file loose in the library folder itself has no folder to move.
	loose := writeFile(t, filepath.Join(f.movies, "Ronin.1998.mkv"), "loose")
	if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, has_file, movie_file_path) VALUES (3, 8195, 'Ronin', 1, ?)`, loose); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Move(ctx, MediaMovie, 3, kids.ID, nil); !errors.Is(err, ErrBadPath) || !strings.Contains(err.Error(), "loose") {
		t.Errorf("loose file: %v", err)
	}

	// A folder that doesn't exist, and a film that doesn't.
	if _, err := m.Move(ctx, MediaMovie, 1, 99, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown folder: %v", err)
	}
	if _, err := m.Move(ctx, MediaMovie, 77, kids.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown film: %v", err)
	}
	// Nothing moved through all of that.
	readFile(t, filepath.Join(src, "Heat (1995).mkv"))
}

// A title with no files yet only changes its folder: its next import lands there.
func TestMoveWithoutFilesReassigns(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	if _, err := f.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title) VALUES (1, 862, 'Toy Story')`); err != nil {
		t.Fatal(err)
	}
	m, _ := f.mover(t, filepath.Join(f.base, "bin"))
	res, err := m.Move(ctx, MediaMovie, 1, kids.ID, nil)
	if err != nil || res.Method != "none" {
		t.Fatalf("move = %+v, %v", res, err)
	}
	if got := queryString(t, f, `SELECT CAST(library_folder_id AS TEXT) FROM movies WHERE id = 1`); got != "1" {
		t.Errorf("folder id = %s", got)
	}
}
