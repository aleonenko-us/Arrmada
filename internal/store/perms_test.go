package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A new data folder, its database and every backup are Arrmada's user's alone; an
// install from before (0644 database, 0755 backups folder) is put right at the next start.
func TestDatabaseAndBackupsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("new data folder %o, want 700", m)
	}
	snap, err := st.Snapshot(ctx, BackupManual)
	if err != nil {
		t.Fatal(err)
	}
	if m := mode(t, snap); m != 0o600 {
		t.Errorf("backup %o, want 600", m)
	}
	if m := mode(t, BackupsDir(dir)); m != 0o700 {
		t.Errorf("backups folder %o, want 700", m)
	}
	db := filepath.Join(dir, "arrmada.db")
	if _, err := st.DB().ExecContext(ctx, `CREATE TABLE perms_probe (x)`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{db, db + "-wal", db + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if m := mode(t, p); m != 0o600 {
				t.Errorf("%s %o, want 600", filepath.Base(p), m)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// As an older build left them.
	for _, p := range []string{db, snap} {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(BackupsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if m := mode(t, db); m != 0o600 {
		t.Errorf("existing database %o after start, want 600", m)
	}
	if m := mode(t, snap); m != 0o600 {
		t.Errorf("existing backup %o after start, want 600", m)
	}
	if m := mode(t, BackupsDir(dir)); m != 0o700 {
		t.Errorf("existing backups folder %o after start, want 700", m)
	}
	// The data folder itself may be shared on purpose: an existing one is left as it is.
	if m := mode(t, dir); m != 0o755 {
		t.Errorf("existing data folder %o, want it left at 755", m)
	}
}
