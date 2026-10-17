package httpapi

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The setup wizard warns that Arrmada is on its own managed storage when nothing is mounted
// at /storage. That's right for the Docker volume /media is by default, and wrong when /media
// is a folder of the owner's (ARRMADA_MEDIA_HOST in .env, or a mount of their own): there
// the library they mean is already inside it. The kernel's mount table tells the two apart.

// mountinfoPath is the mount table read (tests point it at a file of their own).
var mountinfoPath = "/proc/self/mountinfo"

// dockerVolumeRoot matches where a named volume's files live on the host, whatever the data
// root: /var/lib/docker/volumes/<name>/_data, Unraid's docker.img (/volumes/<name>/_data),
// rootless Docker and Podman (…/volumes/<name>/_data under the user's home).
var dockerVolumeRoot = regexp.MustCompile(`/volumes/[^/]+/_data$`)

// isHostFolder reports whether target is a mount of a host folder (a bind mount) rather
// than a Docker volume. Not a mount at all (the image's own folder), or a table that can't be
// read, counts as not: the wizard then says what it always has.
func isHostFolder(target string) bool {
	b, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return false
	}
	return hostFolderIn(string(b), target)
}

// hostFolderIn reads a /proc/<pid>/mountinfo: per line, field 4 is the mount's root within
// its filesystem and field 5 where it's mounted (both with spaces as \040). The last mount
// at target is the one in effect.
func hostFolderIn(mountinfo, target string) bool {
	found, host := false, false
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || unescapeMount(f[4]) != target {
			continue
		}
		found, host = true, !dockerVolumeRoot.MatchString(unescapeMount(f[3]))
	}
	return found && host
}

// unescapeMount undoes mountinfo's octal escapes (\040 space, \011 tab, \012, \134).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
