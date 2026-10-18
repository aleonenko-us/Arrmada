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
// where there's none, or filesystem ids can't be read. tmpfs is refused as "not a disk",
// so a guard using it must read filesystem types through diskTypes.
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

// diskTypes makes every path read as ext4 except those listed as RAM (tmpfs): the test's
// stand-in pool on /dev/shm is a disk to the guard, and a listed folder is Docker's
// RAM-root folder for a mistyped Host Path.
func diskTypes(ram ...string) func(string) (uint64, bool) {
	return func(p string) (uint64, bool) {
		for _, r := range ram {
			if p == r {
				return diskspace.TypeTmpfs, true
			}
		}
		return 0xEF53, true
	}
}

// guardSettingsAPI is settingsAPI with a disk guard over a temp downloads folder.
func guardSettingsAPI(t *testing.T, ram ...string) (*api, string) {
	t.Helper()
	a := settingsAPI(t)
	dl := t.TempDir()
	a.deps.DiskGuard = download.NewDiskGuard(nil, a.deps.Settings, slog.New(slog.NewTextHandler(io.Discard, nil)), dl)
	a.deps.DiskGuard.SetFSTypeFunc(diskTypes(ram...))
	return a, dl
}

// "Measure a different drive" names a server path: admin-only, checked before it's
// stored, and refused with a reason a person can act on.
func TestDiskGuardMeasurePathSetting(t *testing.T) {
	ram := t.TempDir() // Docker's folder in the RAM root for a mistyped Host Path
	a, dl := guardSettingsAPI(t, ram)
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
		{"/", "/ is the container's own filesystem, not a disk"},
		{ram, "is a RAM or container filesystem (tmpfs), not a disk — check the Host Path matches your pool's name"},
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
	MeasureNotDisk    string          `json:"measure_not_disk"`
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
	a.deps.DiskGuard.SetFSTypeFunc(diskTypes())
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
	// The free-space check reads the pool too, not the array.
	if got := a.deps.DiskGuard.MeasuredPathFor(ctx, a.deps.Config.DownloadsDir); got != pool {
		t.Errorf("free-space check reads %s, want %s", got, pool)
	}

	// The mount vanished and Docker made an empty folder in RAM in its place: reported like
	// an unreadable drive, saying what it reads as.
	a.deps.DiskGuard.SetFSTypeFunc(diskTypes(pool))
	got = guardStatus(t, a)
	if !got.MeasureUnreadable || got.MeasureNotDisk != "tmpfs" || got.Measurable {
		t.Errorf("RAM folder: %+v", got)
	}
	f := health.DiskGuardCheck(a.diskGuardState).Run(ctx)
	if len(f) != 1 || !strings.Contains(f[0].Message, "reads as a RAM or container filesystem (tmpfs)") {
		t.Errorf("health with a RAM folder: %+v", f)
	}
	a.deps.DiskGuard.SetFSTypeFunc(diskTypes())

	_ = a.deps.Settings.Set(ctx, download.KeyDiskGuardMeasure, "/gone/cache")
	got = guardStatus(t, a)
	if !got.MeasureUnreadable || got.Measurable || !got.Override || got.MeasuredPath != "/gone/cache" {
		t.Errorf("unreadable: %+v", got)
	}
	f = health.DiskGuardCheck(a.diskGuardState).Run(ctx)
	if len(f) != 1 || f[0].Level != health.LevelWarning || !strings.Contains(f[0].Message, "/gone/cache") {
		t.Errorf("health with the drive gone: %+v", f)
	}
}

// The free-space limit is admin-only, a whole number of GB, and blank means off.
func TestDiskGuardPauseFreeSetting(t *testing.T) {
	a := settingsAPI(t)
	if _, got := getSettings(t, a); got["downloads_disk_guard_pause_free_gb"] != "0" {
		t.Fatalf("default = %v, want \"0\" (off)", got["downloads_disk_guard_pause_free_gb"])
	}
	for _, bad := range []string{`"abc"`, `"-1"`, `"1.5"`, `"99999999"`} {
		if w := putSettings(t, a, `{"downloads_disk_guard_pause_free_gb":`+bad+`}`); w.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", bad, w.Code)
		}
	}
	if w := putSettingsAs(t, a, auth.RoleManager, `{"downloads_disk_guard_pause_free_gb":"100"}`); w.Code != http.StatusForbidden {
		t.Errorf("manager: HTTP %d, want 403", w.Code)
	}
	if w := putSettings(t, a, `{"downloads_disk_guard_pause_free_gb":" 100 "}`); w.Code != http.StatusOK {
		t.Fatalf("100: HTTP %d %s", w.Code, w.Body.String())
	}
	if _, got := getSettings(t, a); got["downloads_disk_guard_pause_free_gb"] != "100" {
		t.Errorf("stored %v", got["downloads_disk_guard_pause_free_gb"])
	}
	if w := putSettings(t, a, `{"downloads_disk_guard_pause_free_gb":""}`); w.Code != http.StatusOK {
		t.Fatalf("blank: HTTP %d", w.Code)
	}
	if _, got := getSettings(t, a); got["downloads_disk_guard_pause_free_gb"] != "0" {
		t.Errorf("blank stored as %v, want 0", got["downloads_disk_guard_pause_free_gb"])
	}
}

// A refused resume names the drive that torrent is held on, not the fullest one, and the
// free-space limit when there is one.
func TestGuardHeldMessageNamesTheVolume(t *testing.T) {
	st := download.GuardStatus{Path: "/mnt/disk3/dl", MeasuredPath: "/mnt/disk3/dl", UsedPct: 97, PausePct: 85, ResumePct: 80}
	v := download.GuardVolume{Path: "/mnt/user/dl", MeasuredPath: "/cache", UsedPct: 91, FreeBytes: 92 << 30}
	msg := guardHeldMessage(v, st)
	if !strings.Contains(msg, "/cache is 91% full with 92 GB free (pauses at 85%, resumes below 80%)") || strings.Contains(msg, "disk3") {
		t.Errorf("message = %s", msg)
	}
	st.PauseFreeBytes, st.ResumeFreeBytes = 100<<30, 110<<30
	if msg := guardHeldMessage(v, st); !strings.Contains(msg, "pauses at 85% or under 100 GB free, resumes below 80% with 110 GB free") {
		t.Errorf("with a floor: %s", msg)
	}
}
