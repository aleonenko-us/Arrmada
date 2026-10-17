package convert

import (
	"os"
	"path/filepath"
	"strings"
)

// Which VA driver libva loads is LIBVA_DRIVER_NAME. The image used to set it to iHD (Intel's
// driver) for every process, which kept AMD cards from ever starting VAAPI: Intel's driver
// doesn't open them, so their test frame failed and Convert stayed on the CPU. Now each
// ffmpeg that opens a VA device is told the driver for that device's vendor: on a box with
// an Intel iGPU and an AMD card, the device picked in Settings → Convert decides. A value set
// on the container always wins (the image no longer sets one, so any value is the owner's).

// drmClassDir is where the kernel describes each DRM device (tests point it elsewhere).
var drmClassDir = "/sys/class/drm"

// libvaDriver is the VA driver for a render node, from its PCI vendor: iHD for Intel,
// radeonsi for AMD (the image carries both), "" when the vendor is unknown or unreadable,
// which leaves the choice to libva.
func libvaDriver(device string) string {
	name := filepath.Base(device)
	if p, err := filepath.EvalSymlinks(device); err == nil {
		name = filepath.Base(p) // /dev/dri/by-path/…-render → renderD129
	}
	b, err := os.ReadFile(filepath.Join(drmClassDir, name, "device", "vendor"))
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(b)) {
	case "0x8086":
		return "iHD"
	case "0x1002":
		return "radeonsi"
	}
	return ""
}

// libvaEnv is the environment for an ffmpeg run with these arguments: the app's own plus
// LIBVA_DRIVER_NAME for the VA device the run opens (-vaapi_device / -hwaccel_device), or
// iHD for a Quick Sync encode (Intel only). nil keeps the app's environment as it is: no VA
// device in the run, a vendor libva works out by itself, or a driver set on the container.
func libvaEnv(args []string) []string {
	if os.Getenv("LIBVA_DRIVER_NAME") != "" {
		return nil
	}
	drv := ""
	for i, a := range args {
		if (a == "-vaapi_device" || a == "-hwaccel_device") && i+1 < len(args) {
			drv = libvaDriver(args[i+1])
			break
		}
	}
	if drv == "" {
		for _, a := range args {
			if strings.HasSuffix(a, "_qsv") {
				drv = "iHD"
				break
			}
		}
	}
	if drv == "" {
		return nil
	}
	return append(os.Environ(), "LIBVA_DRIVER_NAME="+drv)
}
