package libfolders

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

// memSettings is the settings table in memory, for the default folders' downloads.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) get(_ context.Context, key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		return v
	}
	return def
}

func (s *memSettings) set(_ context.Context, key, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = v
	return nil
}

// withSettings rebuilds the fixture's resolver over a settings map the service can write.
func (f *fixture) withSettings(t *testing.T) *memSettings {
	t.Helper()
	s := &memSettings{m: map[string]string{}}
	f.roots = libroots.New(s.get, config.Config{MoviesDir: f.movies, TVDir: f.tv, DownloadsDir: f.dl, DataDir: f.data})
	f.svc = New(f.st.DB(), f.roots, f.data)
	f.roots.WithExtras(f.svc.Extras)
	f.svc.SetSettingWriter(s.set)
	return s
}

// What a downloads folder may be: a full path to an existing, writable folder, clear of
// the data folder and of every library folder. It may be shared, be the Downloads folder
// or sit inside it — those only decide where torrents save.
func TestSetDownloadsValidation(t *testing.T) {
	f := newFixture(t)
	s := f.withSettings(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", "")
	disk2 := mkdir(t, filepath.Join(f.base, "disk2", "downloads"))
	file := writeFile(t, filepath.Join(f.base, "disk2", "notes.txt"), "x")

	refused := map[string]string{
		"relative":                    "disk2/downloads",
		"missing":                     filepath.Join(f.base, "disk9"),
		"a file":                      file,
		"the data folder":             f.data,
		"inside the data folder":      mkdir(t, filepath.Join(f.data, "dl")),
		"the container data dir":      "/data/downloads",
		"a library folder":            f.movies,
		"inside a library folder":     mkdir(t, filepath.Join(f.tv, "dl")),
		"inside an extra folder":      mkdir(t, filepath.Join(kids.Path, "dl")),
		"around the library folders":  filepath.Join(f.base, "media"),
		"around an ebooks library":    mkdir(t, filepath.Join(f.base, "books-root")),
		"the filesystem root (above)": string(filepath.Separator),
	}
	// An ebooks library under books-root/ebooks makes books-root "around a library".
	f.roots = libroots.New(func(ctx context.Context, key, def string) string {
		if key == libroots.KeyEbooks {
			return filepath.Join(f.base, "books-root", "ebooks")
		}
		return s.get(ctx, key, def)
	}, config.Config{MoviesDir: f.movies, TVDir: f.tv, DownloadsDir: f.dl, DataDir: f.data})
	f.svc.roots = f.roots
	f.roots.WithExtras(f.svc.Extras)
	mkdir(t, filepath.Join(f.base, "books-root", "ebooks"))

	for name, p := range refused {
		_, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, p)
		if !errors.Is(err, ErrBadPath) {
			t.Errorf("%s (%s): err = %v, want ErrBadPath", name, p, err)
		}
	}
	if got, _ := f.svc.Get(ctx, MediaMovie, kids.ID); got.Downloads != "" {
		t.Fatalf("a refused folder was saved: %q", got.Downloads)
	}
	// One inside a library is named as that, even before it exists.
	if _, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, filepath.Join(f.movies, "not-yet")); err == nil || !strings.Contains(err.Error(), "overlaps the Movies library folder") {
		t.Errorf("missing folder inside a library: %v", err)
	}

	// Accepted: its own disk, the Downloads folder itself, a folder inside it, and one
	// already used by another library folder.
	for _, p := range []string{disk2, f.dl, mkdir(t, filepath.Join(f.dl, "kids")), disk2} {
		res, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, p+string(filepath.Separator))
		if err != nil {
			t.Fatalf("%s refused: %v", p, err)
		}
		if res.Folder.Downloads != filepath.Clean(p) || res.Folder.ID != kids.ID {
			t.Errorf("saved %+v, want %s", res.Folder, p)
		}
	}
	if _, err := f.svc.SetDownloads(ctx, MediaSeries, DefaultID, disk2); err != nil {
		t.Fatal(err)
	}
	if v := s.get(ctx, libroots.KeyTVDownloads, ""); v != disk2 {
		t.Errorf("TV downloads setting = %q", v)
	}
	if got, _ := f.svc.Get(ctx, MediaSeries, DefaultID); got.Downloads != disk2 || !got.Default {
		t.Errorf("default TV folder = %+v", got)
	}
	// Extras carry it to libroots (and so to grabs, the disk guard and the health panel).
	ex := f.svc.Extras(ctx)
	if len(ex) != 1 || ex[0].Downloads != disk2 {
		t.Errorf("Extras = %+v", ex)
	}
	if got := f.roots.DownloadsFor(ctx, MediaMovie, kids.Path); got != disk2 {
		t.Errorf("DownloadsFor Kids = %q", got)
	}

	// Unknown library or folder.
	if _, err := f.svc.SetDownloads(ctx, "music", DefaultID, disk2); !errors.Is(err, ErrBadMedia) {
		t.Errorf("music: %v", err)
	}
	if _, err := f.svc.SetDownloads(ctx, MediaSeries, kids.ID, disk2); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Movies folder as TV: %v", err)
	}
	// Cleared.
	if res, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, "  "); err != nil || res.Folder.Downloads != "" {
		t.Errorf("clear: %+v %v", res, err)
	}
	if got := f.roots.DownloadsFor(ctx, MediaMovie, kids.Path); got != f.dl {
		t.Errorf("after clearing, Kids downloads to %q, want the Downloads folder", got)
	}
}

// Without a settings writer, a default folder's downloads folder can't be saved (an
// extra's still can).
func TestSetDownloadsDefaultNeedsSettings(t *testing.T) {
	f := newFixture(t)
	disk2 := mkdir(t, filepath.Join(f.base, "disk2"))
	if _, err := f.svc.SetDownloads(context.Background(), MediaMovie, DefaultID, disk2); err == nil {
		t.Error("saved without a settings writer")
	}
}

// Library folders keep clear of downloads folders from then on: an extra folder can't be
// added on, in or around one, and a default library folder can't move onto one.
func TestLibraryFoldersAvoidDownloadsFolders(t *testing.T) {
	f := newFixture(t)
	f.withSettings(t)
	ctx := context.Background()
	disk2 := mkdir(t, filepath.Join(f.base, "disk2", "downloads"))
	if _, err := f.svc.SetDownloads(ctx, MediaMovie, DefaultID, disk2); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{disk2, mkdir(t, filepath.Join(disk2, "kids")), filepath.Join(f.base, "disk2")} {
		if _, err := f.svc.Add(ctx, AddInput{Media: MediaSeries, Path: p}); !errors.Is(err, ErrBadPath) && !errors.Is(err, ErrDuplicates) {
			t.Errorf("extra folder at %s: %v, want refused", p, err)
		}
		if msg := f.svc.LibraryConflict(ctx, p); !strings.Contains(msg, "downloads folder") {
			t.Errorf("LibraryConflict(%s) = %q", p, msg)
		}
		// The Downloads folder may share or hold one: Conflict alone says nothing.
		if msg := f.svc.Conflict(ctx, p); msg != "" {
			t.Errorf("Conflict(%s) = %q, want nothing for the Downloads folder", p, msg)
		}
	}
	if msg := f.svc.LibraryConflict(ctx, mkdir(t, filepath.Join(f.base, "disk3", "movies"))); msg != "" {
		t.Errorf("an unrelated folder: %q", msg)
	}
}

// A downloads folder on another filesystem from its library is saved with a warning:
// imports from it would copy. One on the same filesystem, or one that can't be judged,
// has none.
func TestDownloadsWarning(t *testing.T) {
	defer func(fn func(a, b string) (bool, bool)) { sameDevice = fn }(sameDevice)
	f := newFixture(t)
	f.withSettings(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", "")
	disk2 := mkdir(t, filepath.Join(f.base, "disk2"))

	sameDevice = func(a, b string) (bool, bool) { return false, true }
	res, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, disk2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "copied, not hardlinked") || res.Folder.Downloads != disk2 {
		t.Errorf("other filesystem: %+v — want saved with a warning", res)
	}
	sameDevice = func(a, b string) (bool, bool) { return true, true }
	if w := DownloadsWarning(disk2, kids.Path); w != "" {
		t.Errorf("same filesystem warned: %q", w)
	}
	sameDevice = func(a, b string) (bool, bool) { return false, false }
	if w := DownloadsWarning(disk2, kids.Path); w != "" {
		t.Errorf("unknown warned: %q", w)
	}
	if w := DownloadsWarning("", kids.Path); w != "" {
		t.Errorf("none set warned: %q", w)
	}
}

// Removing an extra folder drops its downloads folder with it; nothing on disk changes.
func TestRemoveFolderDropsItsDownloads(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	kids := f.add(t, MediaMovie, "kids", "")
	disk2 := mkdir(t, filepath.Join(f.base, "disk2"))
	if _, err := f.svc.SetDownloads(ctx, MediaMovie, kids.ID, disk2); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Remove(ctx, kids.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.roots.DownloadFolders(ctx); len(got) != 1 || got[0].Path != f.dl {
		t.Errorf("DownloadFolders after removing = %+v", got)
	}
}
