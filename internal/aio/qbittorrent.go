// Package aio is the Unraid all-in-one image's side of the bundled qBittorrent and
// FlareSolverr: the small supervisor that runs them next to Arrmada in one container
// (cmd/arrmada-aio), and the qBittorrent login it sets up on first start. The plain image
// and the Compose install never run any of it; there each companion is its own container.
package aio

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The bundled qBittorrent inside the all-in-one image. Its web UI listens on WebUIPort
// inside the container, and Arrmada talks to it on the loopback address with the login
// below, like everyone else. There is deliberately no login-free way in, not even from
// the loopback address (WebUI\LocalHostAuth stays on): FlareSolverr's Chromium runs in
// the same container and opens tracker pages, and a hostile one could reach 127.0.0.1
// through DNS rebinding.
const (
	WebUIPort  = 8080
	LocalURL   = "http://127.0.0.1:8080"
	WebUIUser  = "admin"
	loginFile  = "arrmada-webui-login.json"
	confSubdir = "qBittorrent/config" // under the profile folder (--profile)
)

// Login is the qBittorrent web UI login Arrmada made on first start, which Arrmada signs
// in with too. It's stored next to qBittorrent's own config, readable only by the user
// both run as, and shown to admins in Settings → Connections; it never goes in the
// template, the environment or a log.
type Login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginPath is where the login is kept for a qBittorrent profile folder.
func LoginPath(profile string) string { return filepath.Join(profile, loginFile) }

// ReadLogin reads a login written by SeedQbittorrent.
func ReadLogin(path string) (Login, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Login{}, err
	}
	var l Login
	if err := json.Unmarshal(raw, &l); err != nil {
		return Login{}, fmt.Errorf("qbittorrent login: %w", err)
	}
	if l.Username == "" || l.Password == "" {
		return Login{}, errors.New("qbittorrent login: incomplete")
	}
	return l, nil
}

// SeedOptions is what the first qBittorrent config is written from.
type SeedOptions struct {
	Profile     string // qBittorrent's --profile folder, in appdata
	Downloads   string // where downloads land (under the media share, so imports hardlink)
	TorrentPort int    // the incoming BitTorrent port the template publishes
	UID, GID    int    // the user qBittorrent and Arrmada run as
}

// SeedQbittorrent writes qBittorrent's first config, the way the Compose bundle's init
// container does, plus a generated web UI login. It does nothing to a profile that
// already has a config: qBittorrent owns it from then on, and its torrents, settings and
// a password the owner changed all survive updates and a switch off and on again. seeded
// says a new config was written.
func SeedQbittorrent(o SeedOptions) (seeded bool, err error) {
	confDir := filepath.Join(o.Profile, confSubdir)
	conf := filepath.Join(confDir, "qBittorrent.conf")
	if _, err := os.Stat(conf); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(confDir, 0o750); err != nil {
		return false, err
	}
	pass, err := randomPassword()
	if err != nil {
		return false, err
	}
	hash, err := passwordPBKDF2(pass)
	if err != nil {
		return false, err
	}
	// The login first, so a config is never left without its login being known.
	login, _ := json.Marshal(Login{Username: WebUIUser, Password: pass})
	if err := writeFile(LoginPath(o.Profile), login, 0o600, o.UID, o.GID); err != nil {
		return false, err
	}
	if err := writeFile(conf, []byte(qbittorrentConf(o, hash)), 0o640, o.UID, o.GID); err != nil {
		return false, err
	}
	// qBittorrent writes under the whole profile as the dropped-privilege user.
	return true, ChownTree(o.Profile, o.UID, o.GID)
}

// qbittorrentConf is the seeded config. Like the Compose seed: downloads on the shared
// share, TCP only (uTP is costly through Docker's network stack), the legal notice
// accepted. Unlike it, nobody gets in without the password, Arrmada included (see
// above); there's no subnet whitelist. CSRF and clickjacking protection stay on. Host
// header validation is off, as in the Compose seed: it refuses any port but the one
// qBittorrent listens on, and the template publishes 8080 on another host port.
func qbittorrentConf(o SeedOptions, hash string) string {
	dl := strings.TrimRight(o.Downloads, "/")
	var b strings.Builder
	b.WriteString("[Application]\nFileLogger\\Enabled=true\n\n")
	b.WriteString("[BitTorrent]\n")
	fmt.Fprintf(&b, "Session\\DefaultSavePath=%s\n", dl)
	fmt.Fprintf(&b, "Session\\TempPath=%s/incomplete\n", dl)
	b.WriteString("Session\\TempPathEnabled=true\n")
	if o.TorrentPort > 0 {
		fmt.Fprintf(&b, "Session\\Port=%d\n", o.TorrentPort)
	}
	b.WriteString("Session\\BTProtocol=TCP\n\n")
	b.WriteString("[LegalNotice]\nAccepted=true\n\n")
	b.WriteString("[Preferences]\n")
	fmt.Fprintf(&b, "WebUI\\Port=%d\n", WebUIPort)
	b.WriteString("WebUI\\Address=*\n")
	fmt.Fprintf(&b, "WebUI\\Username=%s\n", WebUIUser)
	fmt.Fprintf(&b, "WebUI\\Password_PBKDF2=\"@ByteArray(%s)\"\n", hash)
	b.WriteString("WebUI\\LocalHostAuth=true\n")
	b.WriteString("WebUI\\AuthSubnetWhitelistEnabled=false\n")
	b.WriteString("WebUI\\CSRFProtection=true\n")
	b.WriteString("WebUI\\ClickjackingProtection=true\n")
	b.WriteString("WebUI\\HostHeaderValidation=false\n")
	return b.String()
}

// passwordPBKDF2 is qBittorrent's stored form of a web UI password: PBKDF2-HMAC-SHA512,
// 100,000 rounds, a 16-byte salt and a 64-byte key, written "salt:key" in base64.
func passwordPBKDF2(pass string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return hashWithSalt(pass, salt)
}

func hashWithSalt(pass string, salt []byte) (string, error) {
	key, err := pbkdf2.Key(sha512.New, pass, salt, 100000, 64)
	if err != nil {
		return "", err
	}
	enc := base64.StdEncoding
	return enc.EncodeToString(salt) + ":" + enc.EncodeToString(key), nil
}

// randomPassword is 24 characters of URL-safe base64 (letters, digits, - and _): 144
// random bits, and nothing that needs escaping when it's typed or pasted.
func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// writeFile writes a file whole (through a temporary name, so a crash never leaves half
// of one) with the given mode and owner.
func writeFile(path string, data []byte, mode os.FileMode, uid, gid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile's mode is cut by the umask
		os.Remove(tmp)
		return err
	}
	if err := chown(tmp, uid, gid); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// chown changes a file's owner when running as root (the container's supervisor), and
// does nothing otherwise (tests, which already own what they write).
func chown(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Lchown(path, uid, gid)
}

// CloseToOthers takes "others" off a folder's permissions (creating it if need be), so
// only its owner and group get in: what's inside stays out of reach of a user that is
// neither, however its files' own modes read.
func CloseToOthers(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o007 != 0 {
		keep := fi.Mode() & (os.ModePerm | os.ModeSetgid | os.ModeSetuid | os.ModeSticky)
		return os.Chmod(dir, keep&^0o007)
	}
	return nil
}

// ChownTree hands a folder and everything in it to uid:gid (as root only).
func ChownTree(root string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}
