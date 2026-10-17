package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(c Check) []Finding { return c.Run(context.Background()) }

// No enabled indexers is an error linking to Indexers; a read error says nothing.
func TestIndexersCheck(t *testing.T) {
	got := run(IndexersCheck(func(context.Context) (int, error) { return 0, nil }))
	if len(got) != 1 || got[0].Key != "indexers.none" || got[0].Level != LevelError || got[0].Fix != FixIndexers {
		t.Errorf("no indexers: %+v", got)
	}
	if got := run(IndexersCheck(func(context.Context) (int, error) { return 2, nil })); len(got) != 0 {
		t.Errorf("two indexers: %+v", got)
	}
	if got := run(IndexersCheck(func(context.Context) (int, error) { return 0, errors.New("db") })); len(got) != 0 {
		t.Errorf("read error: %+v", got)
	}
}

// The disk guard holding torrents is a warning that links to its settings.
func TestDiskGuardCheck(t *testing.T) {
	got := run(DiskGuardCheck(func(context.Context) (DiskGuardState, bool) {
		return DiskGuardState{Enabled: true, UsedPct: 96.4, PausePct: 95, ResumePct: 90, Holding: 3}, true
	}))
	if len(got) != 1 || got[0].Level != LevelWarning || got[0].Fix != FixDiskGuard ||
		!strings.Contains(got[0].Message, "96.4% full") || !strings.Contains(got[0].Message, "3 torrents") {
		t.Errorf("holding: %+v", got)
	}
	if got := run(DiskGuardCheck(func(context.Context) (DiskGuardState, bool) {
		return DiskGuardState{Enabled: true, Holding: 0}, true
	})); len(got) != 0 {
		t.Errorf("not holding: %+v", got)
	}
}

func TestDiskFreeCheck(t *testing.T) {
	at := func(gb float64) []Finding {
		return run(diskFreeCheck(func() string { return "/dl" }, func(string) (float64, bool) { return gb, true }))
	}
	if got := at(1.5); len(got) != 1 || got[0].Level != LevelError {
		t.Errorf("1.5 GB: %+v", got)
	}
	if got := at(5); len(got) != 1 || got[0].Level != LevelWarning {
		t.Errorf("5 GB: %+v", got)
	}
	if got := at(50); len(got) != 0 {
		t.Errorf("50 GB: %+v", got)
	}
}

func TestAudiobookServerCheck(t *testing.T) {
	got := run(AudiobookServerCheck(func(context.Context) (bool, bool, string) { return true, false, "port 13378 is taken" }))
	if len(got) != 1 || got[0].Level != LevelError || !strings.Contains(got[0].Message, "port 13378") {
		t.Errorf("on but down: %+v", got)
	}
	if got := run(AudiobookServerCheck(func(context.Context) (bool, bool, string) { return false, false, "" })); len(got) != 0 {
		t.Errorf("off: %+v", got)
	}
}

// The library check judges each folder it's given, never creates one, and flags a folder
// inside the data folder without probing it.
func TestLibraryFoldersCheck(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	movies := filepath.Join(base, "unmounted", "movies") // parent missing: not mounted
	tv := filepath.Join(base, "tv")                      // missing, parent writable
	music := filepath.Join(data, "music")                // inside the data folder
	for _, d := range []string{data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st := LibraryState{
		Folders:  []Folder{{Role: "movies", Label: "Movies", Path: movies}, {Role: "tv", Label: "TV", Path: tv}, {Role: "music", Label: "Music", Path: music}},
		All:      []Folder{{Role: "movies", Label: "Movies", Path: movies}, {Role: "tv", Label: "TV", Path: tv}, {Role: "music", Label: "Music", Path: music}},
		DataDir:  data,
		Recorded: func(context.Context, string) (int, error) { return 0, nil },
	}
	got := run(LibraryFoldersCheck(func(context.Context) LibraryState { return st }, NewProbeCache(0)))
	byKey := map[string]Finding{}
	for _, f := range got {
		byKey[f.Key] = f
	}
	if f := byKey["library.movies"]; f.Level != LevelError || !strings.Contains(f.Message, "isn't mounted") {
		t.Errorf("unmounted movies: %+v", f)
	}
	if f := byKey["library.tv"]; f.Level != LevelWarning || !strings.Contains(f.Message, "doesn't exist yet") || f.Fix != FixLibraryFolders {
		t.Errorf("missing tv: %+v", f)
	}
	if f := byKey["library.music"]; f.Level != LevelError || !strings.Contains(f.Message, "data folder") {
		t.Errorf("music in the data dir: %+v", f)
	}
	for _, p := range []string{tv, filepath.Dir(movies), music} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("the check created %s", p)
		}
	}
}

// A missing folder suggests Create it only when it's known to have held nothing. One
// with files on record is a share that isn't mounted: the advice is the mount, in red.
// When the records can't be read, the advice covers both and doesn't press Create it.
func TestLibraryFoldersCheckMissingFolderThatHeldFiles(t *testing.T) {
	base := t.TempDir()
	tv := filepath.Join(base, "tv") // missing, parent writable
	folders := []Folder{{Role: "tv", Label: "TV", Path: tv}}
	check := func(rec func(context.Context, string) (int, error)) Finding {
		t.Helper()
		st := LibraryState{Folders: folders, All: folders, DataDir: filepath.Join(base, "data"), Recorded: rec}
		got := run(LibraryFoldersCheck(func(context.Context) LibraryState { return st }, NewProbeCache(0)))
		if len(got) != 1 {
			t.Fatalf("findings = %+v, want one", got)
		}
		return got[0]
	}
	held := check(func(_ context.Context, folder string) (int, error) {
		if folder != tv {
			t.Errorf("asked about %s", folder)
		}
		return 42, nil
	})
	if held.Level != LevelError || !strings.Contains(held.Message, "42 files") || !strings.Contains(held.Message, "not mounted") || strings.Contains(held.Message, "Create it") {
		t.Errorf("a missing folder that held files: %+v", held)
	}
	unknown := check(func(context.Context, string) (int, error) { return 0, errors.New("database is locked") })
	if strings.Contains(unknown.Message, "Create it") || !strings.Contains(unknown.Message, "mounted") {
		t.Errorf("records unreadable: %+v", unknown)
	}
	if fresh := check(func(context.Context, string) (int, error) { return 0, nil }); !strings.Contains(fresh.Message, "Create it") {
		t.Errorf("a folder that never held anything: %+v", fresh)
	}
	if _, err := os.Stat(tv); !os.IsNotExist(err) {
		t.Errorf("the check created %s", tv)
	}
}
