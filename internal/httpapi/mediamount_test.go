package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

// Real mountinfo lines (trimmed): the managed volume on a plain Docker host, on Unraid's
// docker.img and under rootless Docker; a host folder (ARRMADA_MEDIA_HOST); the database.
const (
	volumeLine   = "1201 1180 8:2 /var/lib/docker/volumes/arrmada_arrmada-media/_data /media rw,relatime master:1 - ext4 /dev/sda2 rw"
	unraidVolume = "88 70 0:51 /volumes/arrmada_arrmada-media/_data /media rw,noatime - btrfs /dev/loop2 rw,space_cache=v2"
	rootlessVol  = "330 310 0:33 /home/me/.local/share/docker/volumes/arrmada_arrmada-media/_data /media rw - ext4 /dev/nvme0n1p2 rw"
	bindLine     = "1202 1180 0:45 /mnt/user/arrmada\\040media /media rw,noatime - fuse.shfs shfs rw,user_id=0"
	dataLine     = "1203 1180 8:2 /opt/arrmada/data /data rw,relatime - ext4 /dev/sda2 rw"
)

func TestHostFolderIn(t *testing.T) {
	for name, c := range map[string]struct {
		table string
		want  bool
	}{
		"managed volume":                  {dataLine + "\n" + volumeLine, false},
		"managed volume in docker.img":    {unraidVolume, false},
		"managed volume, rootless Docker": {rootlessVol, false},
		"host folder":                     {dataLine + "\n" + bindLine, true},
		"not a mount (the image's own)":   {dataLine, false},
		"the last mount wins":             {volumeLine + "\n" + bindLine, true},
		"empty table":                     {"", false},
	} {
		if got := hostFolderIn(c.table, "/media"); got != c.want {
			t.Errorf("%s: hostFolderIn = %v, want %v", name, got, c.want)
		}
	}
	// Spaces in a mount point are written \040.
	if !hostFolderIn("1 2 0:1 /srv/x /my\\040media rw - ext4 /dev/sda rw", "/my media") {
		t.Error(`a mount point written with \040 should match its real name`)
	}
}

func TestIsHostFolderReadsTheMountTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(p, []byte(dataLine+"\n"+bindLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := mountinfoPath
	mountinfoPath = p
	t.Cleanup(func() { mountinfoPath = old })
	if !isHostFolder("/media") || isHostFolder("/storage") {
		t.Error("isHostFolder should read the table: /media is a host folder, /storage isn't mounted")
	}
	mountinfoPath = filepath.Join(t.TempDir(), "missing")
	if isHostFolder("/media") {
		t.Error("an unreadable table should count as not a host folder")
	}
}
