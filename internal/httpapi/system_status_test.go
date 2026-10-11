package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/backup"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/store"
)

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Status adds up the database's three files, names the newest backup, and lists each
// drive once with the folders that share it (temp dirs are all on one filesystem).
func TestSystemStatusReportsDBSizeAndRoots(t *testing.T) {
	a := settingsAPI(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeSized(t, filepath.Join(data, "arrmada.db"), 4096)
	writeSized(t, filepath.Join(data, "arrmada.db-wal"), 1000)
	writeSized(t, filepath.Join(data, "arrmada.db-shm"), 24)
	older := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
	writeSized(t, filepath.Join(store.BackupsDir(data), store.BackupName(store.BackupNightly, older)), 10)
	writeSized(t, filepath.Join(store.BackupsDir(data), store.BackupName(store.BackupManual, newer)), 10)
	for _, d := range []string{"movies", "tv"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.deps.Config = config.Config{DataDir: data, MoviesDir: filepath.Join(root, "movies"), TVDir: filepath.Join(root, "tv")}
	a.deps.Backups = backup.ForDataDir(data, nil)
	a.start = time.Now().Add(-time.Hour)

	w := httptest.NewRecorder()
	a.handleSystemStatus(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d %s", w.Code, w.Body)
	}
	var got struct {
		Version    string        `json:"version"`
		Go         string        `json:"go"`
		DataDir    string        `json:"data_dir"`
		DBBytes    int64         `json:"db_bytes"`
		Uptime     int           `json:"uptime_seconds"`
		LastBackup *statusBackup `json:"last_backup"`
		Disks      []statusDisk  `json:"disks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DBBytes != 5120 || got.DataDir != data {
		t.Errorf("db_bytes = %d in %q, want 5120 in %q", got.DBBytes, got.DataDir, data)
	}
	if got.Go == "" || got.Uptime < 3500 {
		t.Errorf("go = %q, uptime = %d", got.Go, got.Uptime)
	}
	if got.LastBackup == nil || !got.LastBackup.At.Equal(newer) || got.LastBackup.Kind != string(store.BackupManual) {
		t.Errorf("last_backup = %+v, want the manual one from %v", got.LastBackup, newer)
	}
	if len(got.Disks) == 0 || got.Disks[0].Name != "Movies" {
		t.Fatalf("disks = %+v", got.Disks)
	}
	// Movies, TV and the data folder share the temp filesystem (where that can be told):
	// one entry, the other two listed under it.
	if d := got.Disks[0]; d.Measurable {
		if len(got.Disks) != 1 || len(d.SharedWith) != 2 || d.SharedWith[0].Name != "TV" || d.SharedWith[1].Name != "Database & backups" {
			t.Errorf("disks = %+v, want Movies sharing with TV and the database", got.Disks)
		}
		if d.Total == 0 || d.Free > d.Total {
			t.Errorf("space = %d free of %d", d.Free, d.Total)
		}
	}
}

// With no backups yet, last_backup is null rather than a zero time.
func TestSystemStatusNoBackup(t *testing.T) {
	a := settingsAPI(t)
	a.deps.Config = config.Config{DataDir: t.TempDir()}
	a.deps.Backups = backup.ForDataDir(a.deps.Config.DataDir, nil)
	w := httptest.NewRecorder()
	a.handleSystemStatus(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["last_backup"] != nil {
		t.Errorf("last_backup = %v (%v), want null", got["last_backup"], err)
	}
}

// Status shows paths, so only an admin may read it.
func TestSystemStatusAdminOnly(t *testing.T) {
	s := newRouteServer(t, nil)
	_, adminC := s.user(t, "owner@example.com", auth.RoleAdmin)
	if rec := s.do("GET", "/api/v1/system/status", adminC); rec.Code != http.StatusOK {
		t.Errorf("admin: HTTP %d %s", rec.Code, rec.Body)
	}
	for _, role := range []auth.Role{auth.RoleManager, auth.RoleRequester, auth.RoleReadonly} {
		_, c := s.user(t, string(role)+"@example.com", role)
		if rec := s.do("GET", "/api/v1/system/status", c); rec.Code != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", role, rec.Code)
		}
	}
}
