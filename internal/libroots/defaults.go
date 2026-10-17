package libroots

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/config"
)

// ManagedLibraryDir is the library folder the Docker image and the Compose bundle set
// up themselves (ARRMADA_LIBRARY_DIR's default there), inside the /media volume they
// manage. Nothing the user mounts lives at this path unless they chose to put it there.
const ManagedLibraryDir = "/media/library"

// EnsureManagedDefaults makes the default library folders (movies, tvshows, ebooks,
// audiobooks, music) that are missing inside the managed library folder, and returns
// the ones it made. A fresh install points every library at those folders, but nothing
// made them, so the first book import failed with "library root missing/unmounted"
// until someone created the folder by hand.
//
// It is deliberately narrow, because a missing folder is also what an Unraid share that
// isn't mounted yet looks like, and making the folder there would put the library on
// the container's own disk:
//   - only when the environment's library folder is managedRoot (the managed volume),
//     never a folder the user mounted or picked;
//   - only a library that resolves to its own default folder inside it — one moved
//     anywhere else, in Settings or by an environment variable, is left alone;
//   - only when the library folder itself exists and can be written, and then only the
//     last step of the path (os.Mkdir, never MkdirAll), so nothing above it is made.
func (r *Roots) EnsureManagedDefaults(ctx context.Context, managedRoot string) (created []string, err error) {
	env := r.env()
	root := filepath.Clean(env.LibraryDir)
	if managedRoot == "" || env.LibraryDir == "" || root != filepath.Clean(managedRoot) {
		return nil, nil
	}
	if UnderDataDir(root, env.DataDir) {
		return nil, nil // the folder rules refuse it anyway; never add to it
	}
	fi, statErr := os.Stat(root)
	if statErr != nil || !fi.IsDir() {
		return nil, nil // the volume itself isn't there: not ours to make
	}
	if werr := ProbeWritable(root); werr != nil {
		return nil, fmt.Errorf("the library folder %s can't be written: %w", root, werr)
	}
	var errs []error
	for _, lib := range []struct {
		path string
		sub  string
	}{
		{r.Movies(ctx), config.SubdirMovies},
		{r.TV(ctx), config.SubdirTV},
		{r.Ebooks(ctx), config.SubdirEbooks},
		{r.Audiobooks(ctx), config.SubdirAudiobooks},
		{r.Music(ctx), config.SubdirMusic},
	} {
		want := filepath.Join(root, lib.sub)
		if lib.path == "" || filepath.Clean(lib.path) != want {
			continue
		}
		_, err := os.Lstat(want)
		switch {
		case err == nil:
			continue // there already (a file in its place is the health panel's to report)
		case !errors.Is(err, fs.ErrNotExist):
			errs = append(errs, err)
			continue
		}
		// Group-writable, as "Create it" makes them, so a download client sharing the
		// PGID can write there too.
		if err := os.Mkdir(want, 0o775); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				errs = append(errs, err)
			}
			continue
		}
		created = append(created, want)
	}
	return created, errors.Join(errs...)
}
