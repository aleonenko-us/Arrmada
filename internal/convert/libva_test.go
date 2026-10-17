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
	old := drmClassDir
	drmClassDir = dir
	t.Cleanup(func() { drmClassDir = old })
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
