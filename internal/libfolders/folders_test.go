package libfolders

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/store"
)

// fixture is a database and a set of real temp folders: the default Movies and TV folders,
// downloads, the data folder and a media share to add extra folders from. Nothing here
// touches a real library.
type fixture struct {
	st                         *store.Store
	svc                        *Service
	roots                      *libroots.Roots
	base, movies, tv, data, dl string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		base:   base,
		movies: filepath.Join(base, "media", "movies"),
		tv:     filepath.Join(base, "media", "tv"),
		data:   filepath.Join(base, "data"),
		dl:     filepath.Join(base, "downloads"),
	}
	for _, d := range []string{f.movies, f.tv, f.data, f.dl} {
		mkdir(t, d)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f.st = st
	f.roots = libroots.New(nil, config.Config{MoviesDir: f.movies, TVDir: f.tv, DownloadsDir: f.dl, DataDir: f.data})
	f.svc = New(st.DB(), f.roots, f.data)
	f.roots.WithExtras(f.svc.Extras)
	return f
}

func mkdir(t *testing.T, d string) string {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeFile(t *testing.T, p, body string) string {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) add(t *testing.T, media, name, rule string) Folder {
	t.Helper()
	got, err := f.svc.Add(context.Background(), AddInput{Media: media, Path: mkdir(t, filepath.Join(f.base, "media", name)), Rule: rule})
	if err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	return got
}

// An install that never adds a folder: one folder per library, the default, and every
// answer is it — exactly as before extra folders existed.
func TestNoExtraFoldersIsTheDefault(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, media := range []string{MediaMovie, MediaSeries} {
		if f.svc.Multi(ctx, media) {
			t.Errorf("%s: Multi with no extra folder", media)
		}
		list := f.svc.List(ctx, media)
		if len(list) != 1 || !list[0].Default || list[0].ID != DefaultID {
			t.Errorf("%s: List = %+v, want only the default", media, list)
		}
		if id := f.svc.Pick(ctx, media, Facts{Genres: []string{"Animation"}, Certification: "G", Anime: true}); id != DefaultID {
			t.Errorf("%s: Pick = %d, want the default", media, id)
		}
	}
	if got := f.svc.Path(ctx, MediaMovie, DefaultID); got != f.movies {
		t.Errorf("default movies path = %q", got)
	}
	if got := f.svc.Paths(ctx, MediaSeries); len(got) != 1 || got[0] != f.tv {
		t.Errorf("TV paths = %v", got)
	}
	if ex := f.svc.Extras(ctx); len(ex) != 0 {
		t.Errorf("Extras = %v, want none", ex)
	}
	if libs := f.roots.Libraries(ctx); len(libs) != 2 {
		t.Errorf("Libraries = %+v, want just the two defaults", libs)
	}
	// A folder id that doesn't exist falls back to the default rather than nowhere.
	if got := f.svc.Path(ctx, MediaMovie, 42); got != f.movies {
		t.Errorf("unknown id resolved to %q", got)
	}
}

func TestAddRefusesBadFolders(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	file := writeFile(t, filepath.Join(f.base, "media", "notes.txt"), "x")
	nested := mkdir(t, filepath.Join(f.movies, "Kids"))
	cases := []struct {
		name string
		in   AddInput
		want error
		say  string
	}{
		{"books can't", AddInput{Media: "book", Path: f.base}, ErrBadMedia, ""},
		{"bad rule", AddInput{Media: MediaMovie, Path: f.base, Rule: "romance"}, ErrBadRule, ""},
		{"blank", AddInput{Media: MediaMovie, Path: "  "}, ErrBadPath, "choose"},
		{"relative", AddInput{Media: MediaMovie, Path: "media/kids"}, ErrBadPath, "full path"},
		{"missing", AddInput{Media: MediaMovie, Path: filepath.Join(f.base, "media", "kdis")}, ErrBadPath, "doesn't exist"},
		{"a file", AddInput{Media: MediaMovie, Path: file}, ErrBadPath, "file"},
		{"the data folder", AddInput{Media: MediaMovie, Path: f.data}, ErrBadPath, "data folder"},
		{"inside the default", AddInput{Media: MediaMovie, Path: nested}, ErrBadPath, "overlaps"},
		{"around the default", AddInput{Media: MediaMovie, Path: filepath.Join(f.base, "media")}, ErrBadPath, "overlaps"},
		{"the TV folder", AddInput{Media: MediaMovie, Path: f.tv}, ErrDuplicates, "already"},
		{"the downloads folder", AddInput{Media: MediaSeries, Path: f.dl}, ErrDuplicates, "Downloads"},
	}
	for _, c := range cases {
		_, err := f.svc.Add(ctx, c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
			continue
		}
		if c.say != "" && !strings.Contains(err.Error(), c.say) {
			t.Errorf("%s: %q doesn't say %q", c.name, err, c.say)
		}
	}
	// One extra folder can't sit inside another.
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	if _, err := f.svc.Add(ctx, AddInput{Media: MediaSeries, Path: mkdir(t, filepath.Join(kids.Path, "cartoons"))}); !errors.Is(err, ErrBadPath) {
		t.Errorf("a folder inside an extra one: %v", err)
	}
	if _, err := f.svc.Add(ctx, AddInput{Media: MediaMovie, Path: kids.Path + string(filepath.Separator)}); !errors.Is(err, ErrDuplicates) {
		t.Errorf("the same folder twice: %v", err)
	}
	// The default can't move onto, into or around an extra folder either.
	if msg := f.svc.Conflict(ctx, filepath.Join(kids.Path, "x")); msg == "" {
		t.Error("a default inside an extra folder wasn't flagged")
	}
	if msg := f.svc.Conflict(ctx, mkdir(t, filepath.Join(f.base, "elsewhere"))); msg != "" {
		t.Errorf("an unrelated default was flagged: %s", msg)
	}
}

// Rules are tried in folder order and the first match wins; nothing matching goes to the
// default. Each library has its own folders and order.
func TestPickFollowsRuleOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids-movies", RuleKids)
	anim := f.add(t, MediaMovie, "animation", RuleAnimation)
	plain := f.add(t, MediaMovie, "4k", RuleNone)
	anime := f.add(t, MediaSeries, "anime", RuleAnime)

	if !f.svc.Multi(ctx, MediaMovie) || !f.svc.Multi(ctx, MediaSeries) {
		t.Fatal("both libraries should have several folders")
	}
	frozen := Facts{Genres: []string{"Animation", "Family"}, Certification: "PG"}
	toyStory := Facts{Genres: []string{"Animation"}, Certification: "G"}
	heat := Facts{Genres: []string{"Crime"}, Certification: "R"}
	if got := f.svc.Pick(ctx, MediaMovie, toyStory); got != kids.ID {
		t.Errorf("a G animated film went to %d, want the first matching folder (kids %d)", got, kids.ID)
	}
	if got := f.svc.Pick(ctx, MediaMovie, frozen); got != anim.ID {
		t.Errorf("a PG animated film went to %d, want animation %d", got, anim.ID)
	}
	if got := f.svc.Pick(ctx, MediaMovie, heat); got != DefaultID {
		t.Errorf("an R crime film went to %d, want the default", got)
	}
	// A folder with no rule is never picked by itself.
	for _, facts := range []Facts{toyStory, frozen, heat} {
		if f.svc.Pick(ctx, MediaMovie, facts) == plain.ID {
			t.Error("a folder with no rule was picked")
		}
	}
	// Another library's folders don't take part.
	if got := f.svc.Pick(ctx, MediaMovie, Facts{Anime: true, Genres: []string{"Drama"}}); got != DefaultID {
		t.Errorf("a film picked %d", got)
	}
	if got := f.svc.Pick(ctx, MediaSeries, Facts{Anime: true}); got != anime.ID {
		t.Errorf("an anime show went to %d, want %d", got, anime.ID)
	}

	// Put animation first: now it wins for the G animated film.
	if err := f.svc.Reorder(ctx, MediaMovie, []int64{anim.ID, kids.ID, plain.ID}); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.Pick(ctx, MediaMovie, toyStory); got != anim.ID {
		t.Errorf("after reordering, a G animated film went to %d, want animation %d", got, anim.ID)
	}
	list := f.svc.List(ctx, MediaMovie)
	if len(list) != 4 || !list[0].Default || list[1].ID != anim.ID || list[2].ID != kids.ID || list[3].ID != plain.ID {
		t.Errorf("List order = %+v", list)
	}
	// An order must name each of the library's extra folders exactly once.
	for _, bad := range [][]int64{{anim.ID, kids.ID}, {anim.ID, kids.ID, plain.ID, anime.ID}, {anim.ID, anim.ID, plain.ID}} {
		if err := f.svc.Reorder(ctx, MediaMovie, bad); err == nil {
			t.Errorf("order %v accepted", bad)
		}
	}

	// Labels: the given one, else the folder's name; the default is named after its library.
	if list[0].Label != "Movies" || list[1].Label != "animation" {
		t.Errorf("labels = %q, %q", list[0].Label, list[1].Label)
	}
	label, rule := "Kids & family", RuleNone
	up, err := f.svc.Update(ctx, kids.ID, UpdateInput{Label: &label, Rule: &rule})
	if err != nil || up.Label != label || up.Rule != RuleNone {
		t.Fatalf("update = %+v, %v", up, err)
	}
	if got := f.svc.Pick(ctx, MediaMovie, Facts{Certification: "G"}); got != DefaultID {
		t.Errorf("a folder whose rule was cleared still picked a G film (%d)", got)
	}
	bad := "romance"
	if _, err := f.svc.Update(ctx, kids.ID, UpdateInput{Rule: &bad}); !errors.Is(err, ErrBadRule) {
		t.Errorf("bad rule update: %v", err)
	}
	// Extras reach libroots with their labels, after the default folders.
	libs := f.roots.Libraries(ctx)
	if len(libs) != 6 || libs[2].Label != "animation" || libs[2].Name != "movies" || libs[5].Name != "tv" {
		t.Errorf("Libraries = %+v", libs)
	}
	if got := f.roots.MovieFolders(ctx); len(got) != 4 || got[0] != f.movies {
		t.Errorf("MovieFolders = %v", got)
	}
}

func TestOwnerIsTheLongestFolder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	if got := f.svc.OwnerID(ctx, MediaMovie, filepath.Join(kids.Path, "Up (2009)", "Up.mkv")); got != kids.ID {
		t.Errorf("owner = %d, want %d", got, kids.ID)
	}
	if got := f.svc.OwnerID(ctx, MediaMovie, filepath.Join(f.movies, "Heat (1995)", "Heat.mkv")); got != DefaultID {
		t.Errorf("owner = %d, want the default", got)
	}
	if _, ok := f.svc.Owner(ctx, MediaMovie, filepath.Join(f.base, "elsewhere", "x.mkv")); ok {
		t.Error("a file outside every folder has an owner")
	}
	// "/media/kids-old" is not inside "/media/kids".
	if _, ok := f.svc.Owner(ctx, MediaMovie, kids.Path+"-old/x.mkv"); ok {
		t.Error("a sibling folder sharing a prefix was taken for the folder")
	}
	if got := f.svc.IDForRoot(ctx, MediaMovie, kids.Path); got != kids.ID {
		t.Errorf("IDForRoot = %d", got)
	}
}

// A folder still holding titles can't be removed: they'd point at a folder Arrmada no
// longer knows. Both the assignment and files lying in it count.
func TestRemoveRefusedWhileInUse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	db := f.st.DB()
	kids := f.add(t, MediaMovie, "kids", RuleKids)
	anime := f.add(t, MediaSeries, "anime", RuleAnime)

	if _, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, library_folder_id) VALUES (1, 10, 'Up', ?)`, kids.ID); err != nil {
		t.Fatal(err)
	}
	err := f.svc.Remove(ctx, kids.ID)
	var use *UseError
	if !errors.As(err, &use) || use.Titles != 1 || !errors.Is(err, ErrInUse) {
		t.Fatalf("remove with an assigned film: %v", err)
	}
	if !strings.Contains(err.Error(), "move them") {
		t.Errorf("the refusal doesn't say what to do: %q", err)
	}
	// Assigned elsewhere, but its file still lies in the folder.
	if _, err := db.Exec(`UPDATE movies SET library_folder_id = 0, has_file = 1, movie_file_path = ? WHERE id = 1`, filepath.Join(kids.Path, "Up (2009)", "Up.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Remove(ctx, kids.ID); !errors.As(err, &use) || use.Files != 1 || use.Titles != 0 {
		t.Fatalf("remove with a file in it: %v", err)
	}
	if u, err := f.svc.UsageOf(ctx, kids); err != nil || u.Files != 1 {
		t.Errorf("usage = %+v, %v", u, err)
	}
	// Episodes count for a TV folder.
	if _, err := db.Exec(`INSERT INTO series (id, tmdb_id, title) VALUES (1, 20, 'Frieren')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episodes (series_id, season_number, episode_number, has_file, file_path) VALUES (1, 1, 1, 1, ?)`,
		filepath.Join(anime.Path, "Frieren", "Season 01", "e1.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Remove(ctx, anime.ID); !errors.As(err, &use) || use.Files != 1 {
		t.Fatalf("remove a TV folder holding an episode: %v", err)
	}
	// Emptied, it goes — from Arrmada only: the folder itself stays on disk.
	if _, err := db.Exec(`UPDATE movies SET has_file = 0, movie_file_path = '' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Remove(ctx, kids.ID); err != nil {
		t.Fatalf("remove an empty folder: %v", err)
	}
	if _, err := os.Stat(kids.Path); err != nil {
		t.Errorf("removing the folder from Arrmada touched the disk: %v", err)
	}
	if f.svc.Multi(ctx, MediaMovie) {
		t.Error("Movies still has several folders")
	}
	if err := f.svc.Remove(ctx, kids.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing it twice: %v", err)
	}
	counts, err := f.svc.Counts(ctx, MediaMovie)
	if err != nil || counts[DefaultID] != 1 {
		t.Errorf("counts = %v, %v", counts, err)
	}
}
