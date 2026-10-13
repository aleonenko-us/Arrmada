package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Under `go test`, a brand-new database starts as a copy of one migrated once per test
// binary, instead of running every migration again. Most tests open a fresh store, and
// applying 170-odd migrations with the race detector on takes about five seconds each
// time — which put the race suite past CI's per-package timeout (internal/httpapi alone
// ran for most of an hour). The copy is the very file those migrations produced, so a
// test sees the same schema either way.
//
// testing.Testing is false in the real binary, so this never runs outside tests. It only
// stands in for a fresh install with the embedded migrations: a test that brings its own
// migration set or a BeforeMigrate hook, or opens an existing database, takes the normal
// path, and so does everything that migration and restore tests check.

var testTemplate struct {
	once sync.Once
	db   []byte
	err  error
}

// seedFromTestTemplate writes the migrated template to dbPath when this is a test
// binary and there's no database there yet.
func seedFromTestTemplate(dbPath string) error {
	if !testing.Testing() {
		return nil
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-journal"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return nil // something is already there: open it as it is
		}
	}
	testTemplate.once.Do(func() { testTemplate.db, testTemplate.err = buildTestTemplate() })
	if testTemplate.err != nil {
		return fmt.Errorf("test database template: %w", testTemplate.err)
	}
	return os.WriteFile(dbPath, testTemplate.db, 0o644)
}

// buildTestTemplate migrates a database from scratch the normal way, folds its WAL back
// into the main file and returns that file's bytes.
func buildTestTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "arrmada-store-template-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	st, err := OpenWith(dir, Options{noTestTemplate: true})
	if err != nil {
		return nil, err
	}
	if _, err := st.db.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, "arrmada.db"))
}
