package convert

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeDRM points drmClassDir at a temp folder with one render node per vendor id given.
func fakeDRM(t *testing.T, nodes map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, vendor := range nodes {
		d := filepath.Join(dir, name, "device")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "vendor"), []byte(vendor+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, oldNum := drmClassDir, devNum
	drmClassDir = dir
	devNum = func(string) (string, bool) { return "", false } // by name only, unless a test says
	t.Cleanup(func() { drmClassDir, devNum = old, oldNum })
}

// driverIn is the LIBVA_DRIVER_NAME an environment sets, or "" when it doesn't.
func driverIn(env []string) string {
	got := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "LIBVA_DRIVER_NAME="); ok {
			got = v
		}
	}
	return got
}

func TestLibvaDriverByVendor(t *testing.T) {
	fakeDRM(t, map[string]string{"renderD128": "0x8086", "renderD129": "0x1002", "renderD130": "0x10de"})
	for dev, want := range map[string]string{
		"/dev/dri/renderD128": "iHD",      // Intel, as the image always had it
		"/dev/dri/renderD129": "radeonsi", // AMD: Intel's driver can't open it
		"/dev/dri/renderD130": "",         // NVIDIA (nouveau): libva's own choice
		"/dev/dri/renderD131": "",         // not there at all
	} {
		if got := libvaDriver(dev); got != want {
			t.Errorf("libvaDriver(%s) = %q, want %q", dev, got, want)
		}
	}
}

// TestLibvaEnvFollowsTheDeviceTheRunOpens: an iGPU (Intel) and a card (AMD) in one box —
// each run gets the driver for the device it opens, Quick Sync always Intel's, and a run
// that opens no VA device keeps the app's environment.
func TestLibvaEnvFollowsTheDeviceTheRunOpens(t *testing.T) {
	fakeDRM(t, map[string]string{"renderD128": "0x8086", "renderD129": "0x1002"})
	t.Setenv("LIBVA_DRIVER_NAME", "")
	vaapi, qsv := Encoder{Name: "hevc_vaapi", Kind: "vaapi"}, Encoder{Name: "hevc_qsv", Kind: "qsv"}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default device probe", globalArgs(vaapi, false, ""), "iHD"},
		{"VAAPI encode on the AMD card", globalArgs(vaapi, false, "/dev/dri/renderD129"), "radeonsi"},
		{"VAAPI decode on the AMD card", globalArgs(vaapi, true, "/dev/dri/renderD129"), "radeonsi"},
		{"Quick Sync", append(globalArgs(qsv, false, "/dev/dri/renderD129"), "-c:v", "hevc_qsv"), "iHD"},
		{"CPU encode", []string{"-i", "in.mkv", "-c:v", "libx265", "out.mkv"}, ""},
	}
	for _, c := range cases {
		env := libvaEnv(c.args)
		if got := driverIn(env); got != c.want {
			t.Errorf("%s: LIBVA_DRIVER_NAME = %q, want %q (args %v)", c.name, got, c.want, c.args)
		}
		if c.want == "" && env != nil {
			t.Errorf("%s: a run with no VA device should keep the app's environment (nil), got %d entries", c.name, len(env))
		}
		if c.want != "" && !slices.Contains(env, "PATH="+os.Getenv("PATH")) {
			t.Errorf("%s: the run should keep the app's own environment too", c.name)
		}
	}
}

// TestLibvaEnvOwnerSettingWins: a LIBVA_DRIVER_NAME set on the container is never replaced.
func TestLibvaEnvOwnerSettingWins(t *testing.T) {
	fakeDRM(t, map[string]string{"renderD128": "0x1002"})
	t.Setenv("LIBVA_DRIVER_NAME", "iHD")
	if env := libvaEnv([]string{"-vaapi_device", "/dev/dri/renderD128", "-c:v", "hevc_vaapi"}); env != nil {
		t.Errorf("the owner's LIBVA_DRIVER_NAME should be inherited untouched, got an environment with %q", driverIn(env))
	}
}

// TestLibvaDriverByDeviceNumber: the node's major:minor decides, through /sys/dev/char, even
// when its name in the container is another node's on the host (one card passed in as
// renderD128, which the host calls renderD129); the name is only the fallback.
func TestLibvaDriverByDeviceNumber(t *testing.T) {
	fakeDRM(t, map[string]string{"renderD128": "0x8086", "renderD129": "0x1002"})
	chars := t.TempDir()
	d := filepath.Join(chars, "226:129", "device")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "vendor"), []byte("0x1002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDir := sysDevCharDir
	sysDevCharDir = chars
	t.Cleanup(func() { sysDevCharDir = oldDir })
	nums := map[string]string{"/dev/dri/renderD128": "226:129", "/dev/dri/renderD130": "226:130"}
	devNum = func(p string) (string, bool) { n, ok := nums[p]; return n, ok }

	// Named renderD128 (Intel on the host), but it's the AMD card's numbers.
	if got := libvaDriver("/dev/dri/renderD128"); got != "radeonsi" {
		t.Errorf("by device number: got %q, want radeonsi", got)
	}
	// Numbers with nothing under /sys/dev/char: the name decides.
	if got := libvaDriver("/dev/dri/renderD130"); got != "" {
		t.Errorf("unknown number and unknown name: got %q, want \"\"", got)
	}
	nums["/dev/dri/renderD129"] = "226:200"
	if got := libvaDriver("/dev/dri/renderD129"); got != "radeonsi" {
		t.Errorf("number not in sysfs, name renderD129 (AMD): got %q, want radeonsi", got)
	}
	// No numbers at all (not a character device): the name.
	if got := libvaDriver("/dev/dri/renderD999"); got != "" {
		t.Errorf("nothing known: got %q", got)
	}
}
