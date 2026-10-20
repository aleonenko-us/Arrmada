package aio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SABSeedOptions is what the bundled SABnzbd's first config is written from.
type SABSeedOptions struct {
	Config    string // its sabnzbd.ini, in appdata (Options.SABnzbdConfig)
	Downloads string // the downloads folder; its incomplete and complete folders go under usenet/ there
	// Hostname is the container's own name, which SABnzbd would add to its host whitelist
	// by itself. Arrmada reaches it on 127.0.0.1, which needs no entry.
	Hostname string
	UID, GID int // the user SABnzbd and Arrmada run as
}

// SeedSABnzbd writes the bundled SABnzbd's first sabnzbd.ini, the same settings the
// Compose install's init container writes (docker-compose.yml, arrmada-sabnzbd-init):
// English (Arrmada reads its failure reasons), Arrmada's categories with folders of their
// own, every job repaired, unpacked and deleted after (pp = 3), a random API key and NZB
// key, and a login for its own page (user arrmada, a random password), which an admin
// sees on its card in Settings → Connections. Its downloads go under usenet/ in the
// downloads folder, on the same share as the library, so an import is a rename; Arrmada
// moves them under the Downloads folder picked in the app once it's running.
//
// Its host whitelist stays on (DNS-rebinding protection: FlareSolverr's Chromium opens
// tracker pages in the same container). Arrmada uses 127.0.0.1, an address SABnzbd always
// accepts.
//
// It does nothing to a config that already exists: SABnzbd owns it from then on, so its
// servers, a changed login and its key survive updates and a switch off and on again.
// seeded says a new config was written. The keys and the password are never logged.
func SeedSABnzbd(o SABSeedOptions) (seeded bool, err error) {
	if _, err := os.Stat(o.Config); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	dir := filepath.Dir(o.Config)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, err
	}
	var keys [3]string
	for i, n := range []int{16, 16, 18} { // as the Compose seed: 32, 32 and 36 hex characters
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			return false, err
		}
		keys[i] = hex.EncodeToString(b)
	}
	if err := writeFile(o.Config, []byte(sabnzbdINI(o, keys[0], keys[1], keys[2])), 0o600, o.UID, o.GID); err != nil {
		return false, err
	}
	return true, ChownTree(dir, o.UID, o.GID)
}

// sabnzbdINI is the seeded config. The categories, their order and their settings are the
// Compose seed's; only the folders and the whitelist differ.
func sabnzbdINI(o SABSeedOptions, apiKey, nzbKey, password string) string {
	usenet := strings.TrimRight(o.Downloads, "/") + "/usenet"
	var b strings.Builder
	b.WriteString("__version__ = 19\n__encoding__ = utf-8\n[misc]\nlanguage = en\n")
	fmt.Fprintf(&b, "api_key = %s\nnzb_key = %s\n", apiKey, nzbKey)
	fmt.Fprintf(&b, "username = arrmada\npassword = %s\n", password)
	// Without a name SABnzbd adds its own on first start; the whitelist is on either way.
	if h := strings.ToLower(strings.TrimSpace(o.Hostname)); h != "" && strings.Trim(h, "abcdefghijklmnopqrstuvwxyz0123456789.-") == "" {
		fmt.Fprintf(&b, "host_whitelist = %s,\n", h)
	}
	fmt.Fprintf(&b, "download_dir = %s/incomplete\ncomplete_dir = %s/complete\n", usenet, usenet)
	b.WriteString("auto_browser = 0\n[categories]\n")
	b.WriteString("[[*]]\nname = *\norder = 0\npp = 3\nscript = None\ndir = \"\"\nnewzbin = \"\"\npriority = 0\n")
	for i, c := range []struct{ name, dir string }{
		{"arrmada", "movies"}, {"arrmada-tv", "tv"}, {"arrmada-books", "books"}, {"arrmada-music", "music"},
	} {
		fmt.Fprintf(&b, "[[%s]]\nname = %s\norder = %d\npp = 3\nscript = Default\ndir = %s\nnewzbin = \"\"\npriority = -100\n", c.name, c.name, i+1, c.dir)
	}
	return b.String()
}
