package libroots

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
)

// managedCfg is an install on the environment's defaults, with the library folder at
// lib (a temp dir standing in for /media/library).
func managedCfg(base, lib string) config.Config {
	return config.Config{
		DataDir:       filepath.Join(base, "data"),
		LibraryDir:    lib,
		MoviesDir:     filepath.Join(lib, config.SubdirMovies),
		TVDir:         filepath.Join(lib, config.SubdirTV),
		EbooksDir:     filepath.Join(lib, config.SubdirEbooks),
		AudiobooksDir: filepath.Join(lib, config.SubdirAudiobooks),
		MusicDir:      filepath.Join(lib, config.SubdirMusic),
		DownloadsDir:  filepath.Join(base, "downloads"),
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A fresh install on the managed volume gets every default library folder at startup,
// once; the issue was a book import failing because /media/library/audiobooks was never made.
func TestEnsureManagedDefaultsMakesTheMissingFolders(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "library")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	// Movies is already there (an older install): left as it is.
	if err := os.Mkdir(filepath.Join(lib, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := New((&fakeSettings{m: map[string]string{}}).get, managedCfg(base, lib))
	created, err := r.EnsureManagedDefaults(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(created)
	want := []string{
		filepath.Join(lib, "audiobooks"), filepath.Join(lib, "ebooks"),
		filepath.Join(lib, "music"), filepath.Join(lib, "tvshows"),
	}
	if len(created) != len(want) {
		t.Fatalf("created %v, want %v", created, want)
	}
	for i := range want {
		if created[i] != want[i] || !exists(want[i]) {
			t.Errorf("created[%d] = %q, want %q made", i, created[i], want[i])
		}
	}
	again, err := r.EnsureManagedDefaults(context.Background(), lib)
	if err != nil || len(again) != 0 {
		t.Errorf("second run made %v (err %v), want nothing", again, err)
	}
}

// Anything that isn't the managed library folder's own default is never made: a folder
// moved in Settings or by an environment variable, a library folder that isn't the
// managed one, and a managed folder that isn't there at all (a volume or share that
// isn't mounted).
func TestEnsureManagedDefaultsLeavesOtherFoldersAlone(t *testing.T) {
	ctx := context.Background()

	t.Run("moved in settings or the environment", func(t *testing.T) {
		base := t.TempDir()
		lib := filepath.Join(base, "library")
		if err := os.Mkdir(lib, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := managedCfg(base, lib)
		cfg.TVDir = filepath.Join(base, "share", "tv") // environment override
		saved := filepath.Join(base, "array", "audiobooks")
		s := &fakeSettings{m: map[string]string{KeyAudiobooks: saved}}
		created, err := New(s.get, cfg).EnsureManagedDefaults(ctx, lib)
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 3 {
			t.Errorf("created %v, want movies, ebooks and music only", created)
		}
		for _, p := range []string{cfg.TVDir, saved, filepath.Join(lib, "tvshows"), filepath.Join(lib, "audiobooks"), filepath.Join(base, "share"), filepath.Join(base, "array")} {
			if exists(p) {
				t.Errorf("%s was made; it isn't the managed default", p)
			}
		}
	})

	t.Run("library folder isn't the managed one", func(t *testing.T) {
		base := t.TempDir()
		storage := filepath.Join(base, "storage") // e.g. ARRMADA_LIBRARY_DIR=/storage on Unraid
		if err := os.Mkdir(storage, 0o755); err != nil {
			t.Fatal(err)
		}
		r := New(nil, managedCfg(base, storage))
		created, err := r.EnsureManagedDefaults(ctx, filepath.Join(base, "library"))
		if err != nil || len(created) != 0 {
			t.Errorf("made %v (err %v) in a library folder that isn't managed", created, err)
		}
		if exists(filepath.Join(storage, "movies")) {
			t.Error("made a folder in a mounted share")
		}
	})

	t.Run("managed folder missing", func(t *testing.T) {
		base := t.TempDir()
		lib := filepath.Join(base, "library")
		created, err := New(nil, managedCfg(base, lib)).EnsureManagedDefaults(ctx, lib)
		if err != nil || len(created) != 0 {
			t.Errorf("made %v (err %v) under a library folder that isn't there", created, err)
		}
		if exists(lib) {
			t.Error("the library folder itself was made")
		}
	})

	t.Run("no managed folder", func(t *testing.T) {
		base := t.TempDir()
		lib := filepath.Join(base, "library")
		if err := os.Mkdir(lib, 0o755); err != nil {
			t.Fatal(err)
		}
		if created, _ := New(nil, managedCfg(base, lib)).EnsureManagedDefaults(ctx, ""); len(created) != 0 {
			t.Errorf("made %v with no managed folder named", created)
		}
	})
}
