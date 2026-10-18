package store

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// The database holds every account's password hash, sessions, API keys and the logins
// of the services Arrmada talks to, and every backup is a full copy of it. So they're
// readable by the user Arrmada runs as (PUID) and nobody else: files 0600, the backups
// folder 0700. update.sh never reads them from the host: its backups, restores and checks
// all run the app's own binary inside the container, as PUID or as root.
const (
	privateFile fs.FileMode = 0o600
	privateDir  fs.FileMode = 0o700
)

// makePrivate takes group and other access off path (a file or a folder), keeping the
// owner's. A path that doesn't exist is fine; nothing else is ever widened.
func makePrivate(path string) error {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return os.Chmod(path, fi.Mode().Perm()&^0o077)
}

// restrictDataFiles makes the database files and the backups already on disk private:
// installs from before this were made with 0644 files and a 0755 backups folder. Best
// effort: a filesystem that can't change modes (some network shares) is no reason not to
// start, so a failure is logged once and the rest carry on.
func restrictDataFiles(dataDir, dbPath string, log *slog.Logger) {
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		note(makePrivate(p))
	}
	dir := BackupsDir(dataDir)
	note(makePrivate(dir))
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				note(makePrivate(filepath.Join(dir, e.Name())))
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		note(err)
	}
	if first != nil {
		log.Warn("couldn't make the database and its backups readable by Arrmada's user only", "err", first)
	}
}

// mkPrivateDir makes dir (and any missing parents) and, when it's new, gives it 0700.
// An existing folder keeps the mode it has: the data folder may be shared on purpose.
func mkPrivateDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return err
	}
	return os.Chmod(dir, privateDir) // MkdirAll's mode is cut by the umask
}
