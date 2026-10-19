package aio

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func ProwlarrProfile(data string) string { return filepath.Join(data, "prowlarr") }
func SABProfile(data string) string      { return filepath.Join(data, "sabnzbd") }
func SABConfigPath(data string) string   { return filepath.Join(SABProfile(data), "sabnzbd.ini") }

// SeedSAB writes the initial config once. SABnzbd's key and web UI login live in appdata,
// where Arrmada can register the client and show the login to admins. Never log either.
func SeedSAB(profile, downloads string, uid, gid int) (bool, error) {
	if err := os.MkdirAll(profile, 0o750); err != nil {
		return false, err
	}
	file := filepath.Join(profile, "sabnzbd.ini")
	if _, err := os.Stat(file); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	apiKey, err := randomHex(16)
	if err != nil {
		return false, err
	}
	nzbKey, err := randomHex(16)
	if err != nil {
		return false, err
	}
	password, err := randomHex(18)
	if err != nil {
		return false, err
	}
	incomplete := filepath.Join(downloads, "usenet", "incomplete")
	complete := filepath.Join(downloads, "usenet", "complete")
	for _, dir := range []string{incomplete, complete} {
		if err := os.MkdirAll(dir, 0o775); err != nil {
			return false, err
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			return false, err
		}
	}
	content := fmt.Sprintf(`__version__ = 19
__encoding__ = utf-8
[misc]
language = en
api_key = %s
nzb_key = %s
username = arrmada
password = %s
host_whitelist_enabled = 0
download_dir = %s
complete_dir = %s
auto_browser = 0
[categories]
[[*]]
name = *
order = 0
pp = 3
script = None
dir = ""
newzbin = ""
priority = 0
[[arrmada]]
name = arrmada
order = 1
pp = 3
script = Default
dir = movies
newzbin = ""
priority = -100
[[arrmada-tv]]
name = arrmada-tv
order = 2
pp = 3
script = Default
dir = tv
newzbin = ""
priority = -100
[[arrmada-books]]
name = arrmada-books
order = 3
pp = 3
script = Default
dir = books
newzbin = ""
priority = -100
[[arrmada-music]]
name = arrmada-music
order = 4
pp = 3
script = Default
dir = music
newzbin = ""
priority = -100
`, apiKey, nzbKey, password, incomplete, complete)
	tmp := file + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return false, err
	}
	if err := os.Chown(tmp, uid, gid); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Chown(profile, uid, gid); err != nil {
		return false, err
	}
	return true, nil
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate SABnzbd credential: %w", err)
	}
	return hex.EncodeToString(b), nil
}
