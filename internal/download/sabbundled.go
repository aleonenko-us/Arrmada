package download

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The optional bundled SABnzbd (docker-compose.yml's "sabnzbd" profile, install.sh
// --with-sabnzbd). An init container writes its first sabnzbd.ini with a generated API key,
// and the volume holding it is mounted read-only into the app (config.SABnzbdConfig,
// /bundled/sabnzbd/sabnzbd.ini). The key is read from there and stored like any client's
// key: it is never in .env, never printed by the installer, never logged.

var errNoSABKey = errors.New("sabnzbd.ini has no usable api_key")

// sabKeyRe is what an API key may look like: SABnzbd makes 32 hex characters; anything
// with spaces, quotes or symbols in it is not a key it wrote.
var sabKeyRe = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

// ReadSABAPIKey reads [misc] api_key from a sabnzbd.ini. A missing file comes back as an
// error satisfying errors.Is(err, os.ErrNotExist): the bundled SABnzbd isn't switched on.
func ReadSABAPIKey(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseSABAPIKey(io.LimitReader(f, 4<<20))
}

// parseSABAPIKey is ReadSABAPIKey's reader: ConfigObj's layout, [section] then key = value.
func parseSABAPIKey(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
			section = "sub:" + section // a subsection (a category, a server): never misc's keys
			continue
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "misc" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "api_key" {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if !sabKeyRe.MatchString(v) {
			return "", errNoSABKey
		}
		return v, nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading sabnzbd.ini: %w", err)
	}
	return "", errNoSABKey
}

// SABTuned is what TuneBundledSAB changed in the bundled SABnzbd.
type SABTuned struct {
	Categories []string // Arrmada categories it created
	Folders    string   // the folder downloads now go under ("" when unchanged)
}

// bundledSABTuner is the part of the SABnzbd downloader that sets up the bundled one.
type bundledSABTuner interface {
	AddMissingCategories(ctx context.Context, dc Client) ([]string, error)
	UseDownloadsFolder(ctx context.Context, dc Client, root string) (string, error)
}

// TuneBundledSAB gets the bundled SABnzbd ready for Arrmada, the way the bundled
// qBittorrent's save path is set: Arrmada's categories (its seeded config has the default
// movie category, which ARRMADA_DOWNLOAD_CATEGORY can rename), and its incomplete and
// completed folders under usenet/ in Arrmada's downloads folder, so a finished job sits on
// the same filesystem as the library and imports by a rename rather than a copy. Only the
// bundled SABnzbd is ever changed like this; someone's own SABnzbd is theirs.
// ErrBundledInactive when there's none, or it's switched off.
func (s *Service) TuneBundledSAB(ctx context.Context, downloadsRoot string) (SABTuned, error) {
	var out SABTuned
	c, ok, err := s.repo.BundledOf(ctx, KindSABnzbd)
	if err != nil {
		return out, err
	}
	if !ok || !c.Enabled {
		return out, ErrBundledInactive
	}
	impl, found := s.registry.For(c.Kind)
	if !found {
		return out, fmt.Errorf("no downloader for kind %q", c.Kind)
	}
	t, ok := impl.(bundledSABTuner)
	if !ok {
		return out, nil
	}
	if out.Categories, err = t.AddMissingCategories(ctx, c); err != nil {
		return out, err
	}
	if downloadsRoot != "" {
		if out.Folders, err = t.UseDownloadsFolder(ctx, c, downloadsRoot); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ErrSABCantSee means SABnzbd doesn't see Arrmada's downloads folder, so its folders were
// left where they are rather than pointed at a path that only exists inside its container.
var ErrSABCantSee = errors.New("SABnzbd doesn't see Arrmada's downloads folder")

// UseDownloadsFolder points SABnzbd's incomplete and completed folders at usenet/incomplete
// and usenet/complete inside root (Arrmada's downloads folder, as Arrmada sees it). Before
// changing anything it checks SABnzbd sees the same folder: Arrmada writes a marker file
// there and asks SABnzbd to list the folder. A folder that's only mounted into Arrmada
// would otherwise be created inside SABnzbd's container, and every download would vanish
// with it. It returns the new folder SABnzbd downloads under, "" when nothing changed.
func (s *SABnzbd) UseDownloadsFolder(ctx context.Context, dc Client, root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", nil
	}
	local := filepath.Join(filepath.Clean(root), "usenet")
	remote := strings.TrimRight(filepath.ToSlash(dc.PathMaps.ToRemote(local)), "/")
	complete, incomplete := remote+"/complete", remote+"/incomplete"
	st, err := s.fullStatus(ctx, dc)
	if err != nil {
		return "", err
	}
	if sameRemote(st.Status.CompleteDir, complete) && sameRemote(st.Status.DownloadDir, incomplete) {
		return "", nil
	}

	if err := os.MkdirAll(local, 0o775); err != nil {
		return "", fmt.Errorf("couldn't make %s: %w", local, err)
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	marker := ".arrmada-sab-check-" + hex.EncodeToString(b[:])
	if err := os.WriteFile(filepath.Join(local, marker), nil, 0o644); err != nil {
		return "", fmt.Errorf("couldn't write to %s: %w", local, err)
	}
	defer os.Remove(filepath.Join(local, marker))
	seen, err := s.sees(ctx, dc, remote, marker)
	if err != nil {
		return "", err
	}
	if !seen {
		return "", fmt.Errorf("%w (%s): it keeps downloading to %s. Mount the same folder into SABnzbd", ErrSABCantSee, root, st.Status.CompleteDir)
	}

	for _, kv := range [][2]string{{"download_dir", incomplete}, {"complete_dir", complete}} {
		if _, err := s.call(ctx, dc, "set_config", url.Values{"section": {"misc"}, "keyword": {kv[0]}, "value": {kv[1]}}); err != nil {
			return "", fmt.Errorf("couldn't set SABnzbd's %s: %w", kv[0], err)
		}
	}
	// Read back: SABnzbd keeps the old folder, without an error, when it can't make the new one.
	after, err := s.fullStatus(ctx, dc)
	if err != nil {
		return "", err
	}
	if !sameRemote(after.Status.CompleteDir, complete) || !sameRemote(after.Status.DownloadDir, incomplete) {
		return "", fmt.Errorf("SABnzbd didn't take the folders %s and %s (it has %s and %s)", incomplete, complete, after.Status.DownloadDir, after.Status.CompleteDir)
	}
	return remote, nil
}

// sees asks SABnzbd to list dir (its folder browser) and reports whether name is in it.
func (s *SABnzbd) sees(ctx context.Context, dc Client, dir, name string) (bool, error) {
	body, err := s.call(ctx, dc, "browse", url.Values{"name": {dir}, "show_files": {"1"}, "show_hidden_folders": {"1"}})
	if err != nil {
		return false, err
	}
	list, err := decode[struct {
		Paths []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"paths"`
	}](dc, body, "folder listing")
	if err != nil {
		return false, err
	}
	for _, p := range list.Paths {
		if p.Name == name && sameRemote(p.Path, path.Join(dir, name)) {
			return true, nil
		}
	}
	return false, nil
}

// sameRemote compares two of SABnzbd's paths, ignoring a trailing slash.
func sameRemote(a, b string) bool {
	return strings.TrimRight(a, "/\\") == strings.TrimRight(b, "/\\")
}
