package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
)

// otherFilesystem finds a folder on a different, measurable filesystem from dir (tmpfs at
// /dev/shm on Linux and in Docker), standing in for a mapped cache pool. Skips the test
// where there's none, or filesystem ids can't be read.
func otherFilesystem(t *testing.T, dir string) string {
	t.Helper()
	base, ok := diskspace.Device(dir)
	if !ok {
		t.Skip("filesystem ids can't be read on this platform")
	}
	for _, p := range []string{"/dev/shm", "/run", "/dev"} {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			continue
		}
		if u, ok := diskspace.Of(p); !ok || u.TotalBytes == 0 {
			continue
		}
		if d, ok := diskspace.Device(p); ok && d != base {
			return p
		}
	}
	t.Skip("no second measurable filesystem here")
	return ""
}

// guardSettingsAPI is settingsAPI with a disk guard over a temp downloads folder.
func guardSettingsAPI(t *testing.T) (*api, string) {
	t.Helper()
	a := settingsAPI(t)
	dl := t.TempDir()
	a.deps.DiskGuard = download.NewDiskGuard(nil, a.deps.Settings, slog.New(slog.NewTextHandler(io.Discard, nil)), dl)
	return a, dl
}

// "Measure a different drive" names a server path: admin-only, checked before it's
// stored, and refused with a reason a person can act on.
func TestDiskGuardMeasurePathSetting(t *testing.T) {
	a, dl := guardSettingsAPI(t)
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	put := func(p string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"downloads_disk_guard_measure_path": p})
		return putSettings(t, a, string(b))
	}
	for _, c := range []struct{ in, want string }{
		{"cache", "full path starting with /"},
		{"/no/such/folder/anywhere", "doesn't exist inside Arrmada's container"},
		{file, "isn't a folder"},
	} {
		if w := put(c.in); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: HTTP %d %s, want 400 with %q", c.in, w.Code, w.Body.String(), c.want)
		}
	}
	if _, ok := diskspace.Device(dl); ok {
		// Another folder on the downloads folder's own filesystem measures nothing new.
		if w := put(t.TempDir()); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "already what the guard measures") {
			t.Errorf("same filesystem: HTTP %d %s, want 400", w.Code, w.Body.String())
		}
	}
	if _, got := getSettings(t, a); got["downloads_disk_guard_measure_path"] != "" {
		t.Fatalf("a refused path was stored: %v", got["downloads_disk_guard_measure_path"])
	}

	pool := otherFilesystem(t, dl)
	for _, role := range []auth.Role{auth.RoleManager, auth.RoleRequester} {
		w := putSettingsAs(t, a, role, `{"downloads_disk_guard_measure_path":"`+pool+`"}`)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "the drive the disk guard measures") {
			t.Errorf("%s: HTTP %d %s, want 403", role, w.Code, w.Body.String())
		}
	}
	if w := put(pool + "/"); w.Code != http.StatusOK {
		t.Fatalf("a separate drive: HTTP %d %s", w.Code, w.Body.String())
	}
	if _, got := getSettings(t, a); got["downloads_disk_guard_measure_path"] != pool {
		t.Fatalf("stored %v, want %s", got["downloads_disk_guard_measure_path"], pool)
	}
	// A manager resending the page's copy unchanged still saves their own fields.
	if w := putSettingsAs(t, a, auth.RoleManager, `{"downloads_disk_guard_measure_path":"`+pool+`","write_nfo":true}`); w.Code != http.StatusOK {
		t.Errorf("manager unchanged resend: HTTP %d %s", w.Code, w.Body.String())
	}

	// The mount goes away later: resending the saved value isn't re-checked, so the rest of
	// the page keeps saving; clearing it always works.
	ctx := context.Background()
	_ = a.deps.Settings.Set(ctx, download.KeyDiskGuardMeasure, "/gone/cache")
	if w := putSettings(t, a, `{"downloads_disk_guard_measure_path":"/gone/cache","search_on_add":false}`); w.Code != http.StatusOK {
		t.Errorf("unchanged unreadable path: HTTP %d %s", w.Code, w.Body.String())
	}
	if w := put(""); w.Code != http.StatusOK {
		t.Errorf("clearing: HTTP %d %s", w.Code, w.Body.String())
	}
	if _, got := getSettings(t, a); got["downloads_disk_guard_measure_path"] != "" {
		t.Errorf("cleared value = %v", got["downloads_disk_guard_measure_path"])
	}
}

// Without a running guard there's nothing to measure for; a path is refused, clearing
// isn't.
func TestDiskGuardMeasurePathWithoutGuard(t *testing.T) {
	a := settingsAPI(t)
	if w := putSettings(t, a, `{"downloads_disk_guard_measure_path":"/cache"}`); w.Code != http.StatusBadRequest {
		t.Errorf("HTTP %d, want 400", w.Code)
	}
	if w := putSettings(t, a, `{"downloads_disk_guard_measure_path":""}`); w.Code != http.StatusOK {
		t.Errorf("clearing: HTTP %d", w.Code)
	}
}

type guardStatusJSON struct {
	Path              string          `json:"path"`
	Measurable        bool            `json:"measurable"`
	MeasurePath       string          `json:"measure_path"`
	MeasuredPath      string          `json:"measured_path"`
	Override          bool            `json:"override"`
	MeasureUnreadable bool            `json:"measure_unreadable"`
	TotalBytes        uint64          `json:"total_bytes"`
	FreeBytes         uint64          `json:"free_bytes"`
	SharedWith        []health.Folder `json:"shared_with"`
}

func guardStatus(t *testing.T, a *api) guardStatusJSON {
	t.Helper()
	w := httptest.NewRecorder()
	a.handleDiskGuardStatus(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: HTTP %d %s", w.Code, w.Body.String())
	}
	var got guardStatusJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// The status says which drive it reads and how big it is; a mapped pool on another
// filesystem drops the "shares a drive with" warning, and a pool that goes away is
// reported (here and in the health panel) instead of quietly reading the downloads folder.
func TestDiskGuardStatusWithOverride(t *testing.T) {
	a, _ := folderTestAPI(t)
	healthAPI(t, a)
	a.deps.DiskGuard = download.NewDiskGuard(a.deps.Downloads, a.deps.Settings, a.deps.Log, a.deps.Config.DownloadsDir)
	pool := otherFilesystem(t, a.deps.Config.DownloadsDir)
	ctx := context.Background()

	before := guardStatus(t, a)
	if before.Override || before.MeasuredPath != before.Path || before.MeasurePath != "" || len(before.SharedWith) == 0 || before.TotalBytes == 0 {
		t.Fatalf("without an override: %+v", before)
	}

	_ = a.deps.Settings.Set(ctx, download.KeyDiskGuardMeasure, pool)
	got := guardStatus(t, a)
	if !got.Override || got.MeasuredPath != pool || got.MeasurePath != pool || got.Path != before.Path ||
		!got.Measurable || got.MeasureUnreadable || got.TotalBytes == 0 || got.FreeBytes > got.TotalBytes {
		t.Errorf("with %s: %+v", pool, got)
	}
	if len(got.SharedWith) != 0 {
		t.Errorf("the libraries aren't on %s, but shared_with = %+v", pool, got.SharedWith)
	}
	if f := health.DiskGuardCheck(a.diskGuardState).Run(ctx); len(f) != 0 {
		t.Errorf("health with a readable drive and nothing held: %+v", f)
	}

	_ = a.deps.Settings.Set(ctx, download.KeyDiskGuardMeasure, "/gone/cache")
	got = guardStatus(t, a)
	if !got.MeasureUnreadable || got.Measurable || !got.Override || got.MeasuredPath != "/gone/cache" {
		t.Errorf("unreadable: %+v", got)
	}
	f := health.DiskGuardCheck(a.diskGuardState).Run(ctx)
	if len(f) != 1 || f[0].Level != health.LevelWarning || !strings.Contains(f[0].Message, "/gone/cache") {
		t.Errorf("health with the drive gone: %+v", f)
	}
}
