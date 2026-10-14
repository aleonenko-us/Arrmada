package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// composeService is what these tests read from one service of a compose file.
type composeService struct {
	image         string
	containerName string
	env           map[string]string
	volumes       []string
	ports         []string
}

// interpDefault matches a ${VAR:-default} with a plain default.
var interpDefault = regexp.MustCompile(`\$\{[A-Z0-9_]+:-([^${}]*)\}`)

// withDefaults is s as docker-compose.yml reads with nothing in .env.
func withDefaults(s string) string { return interpDefault.ReplaceAllString(s, "$1") }

// readCompose reads the services of a compose file this repository writes. It is not a
// YAML parser: it knows the two-space layout both files use, `x-name: &anchor` maps
// merged with `<<: *anchor` (or used whole as `environment: *anchor`), and `&name` /
// `*name` on a volume line. Enough to compare the two files' essentials without a
// YAML dependency in the module.
func readCompose(t *testing.T, path string) map[string]*composeService {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	unquote := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.Index(s, " #"); i >= 0 && !strings.HasPrefix(s, `"`) {
			s = strings.TrimSpace(s[:i])
		}
		if strings.HasPrefix(s, `"`) {
			if j := strings.LastIndex(s, `"`); j > 0 {
				s = s[1:j]
			}
		}
		return s
	}
	anchors := map[string]map[string]string{} // x- maps
	scalars := map[string]string{}            // &name on a list item
	services := map[string]*composeService{}
	var topKey, svc, section, anchor string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			topKey, svc, section, anchor = strings.SplitN(trimmed, ":", 2)[0], "", "", ""
			if strings.HasPrefix(topKey, "x-") {
				if i := strings.Index(trimmed, "&"); i >= 0 {
					anchor = strings.TrimSpace(trimmed[i+1:])
					anchors[anchor] = map[string]string{}
				}
			}
		case topKey == "services" && indent == 2:
			svc, section = strings.TrimSuffix(trimmed, ":"), ""
			services[svc] = &composeService{env: map[string]string{}}
		case anchor != "" && indent == 2:
			k, v, _ := strings.Cut(trimmed, ":")
			anchors[anchor][k] = unquote(v)
		case svc != "" && indent == 4:
			k, v, _ := strings.Cut(trimmed, ":")
			v = strings.TrimSpace(v)
			section = k
			s := services[svc]
			switch k {
			case "image":
				s.image = unquote(v)
			case "container_name":
				s.containerName = unquote(v)
			case "environment":
				if strings.HasPrefix(v, "*") {
					for ek, ev := range anchors[v[1:]] {
						s.env[ek] = ev
					}
				}
			}
		case svc != "" && indent == 6:
			s := services[svc]
			switch section {
			case "environment":
				k, v, _ := strings.Cut(trimmed, ":")
				if k == "<<" {
					for ek, ev := range anchors[strings.TrimPrefix(strings.TrimSpace(v), "*")] {
						s.env[ek] = ev
					}
					continue
				}
				s.env[k] = withDefaults(unquote(v))
			case "volumes", "ports":
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if strings.HasPrefix(item, "&") {
					name, rest, _ := strings.Cut(item[1:], " ")
					scalars[name] = unquote(rest)
					item = rest
				} else if strings.HasPrefix(item, "*") {
					item = scalars[item[1:]]
				}
				item = withDefaults(unquote(item))
				if section == "volumes" {
					s.volumes = append(s.volumes, item)
				} else {
					s.ports = append(s.ports, item)
				}
			}
		}
	}
	return services
}

// mountTarget is the path inside the container a volume line mounts at.
func mountTarget(v string) string {
	parts := strings.Split(v, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return parts[0]
}

// TestComposeExampleMatchesCompose: docker-compose.example.yml is the copy-paste install
// in the README, for people with neither the installer nor a checkout. It has to start
// the same app the repository's docker-compose.yml does — same published image, the
// same companions at the same versions, the same settings with the same defaults — or
// a change made to one quietly breaks the other.
func TestComposeExampleMatchesCompose(t *testing.T) {
	root := repoRoot(t)
	main := readCompose(t, filepath.Join(root, "docker-compose.yml"))
	ex := readCompose(t, filepath.Join(root, "docker-compose.example.yml"))

	const published = "ghcr.io/tristenlammi/arrmada:stable"
	if !strings.HasSuffix(main["arrmada-app"].image, published+"}") {
		t.Errorf("docker-compose.yml's app image %q no longer defaults to %s", main["arrmada-app"].image, published)
	}
	if got := ex["arrmada-app"].image; got != published {
		t.Errorf("docker-compose.example.yml runs %q, want %s", got, published)
	}

	// Every service the app needs to work out of the box (Prowlarr is optional, and
	// commented out in the example).
	for name, m := range main {
		if name == "arrmada-prowlarr" {
			continue
		}
		e, ok := ex[name]
		if !ok {
			t.Errorf("docker-compose.example.yml has no %s service", name)
			continue
		}
		if e.containerName != m.containerName {
			t.Errorf("%s: container_name %q in the example, %q in docker-compose.yml", name, e.containerName, m.containerName)
		}
		if name != "arrmada-app" && e.image != m.image {
			t.Errorf("%s: image %q in the example, %q in docker-compose.yml", name, e.image, m.image)
		}
	}

	// The app's settings: the example sets the same ones to docker-compose.yml's defaults,
	// except the ones that are API keys (entered in the app), legacy fallbacks, starting
	// library folders (picked in the setup) and last-resort switches.
	notInExample := map[string]bool{
		"ARRMADA_TMDB_API_KEY": true, "ARRMADA_OMDB_API_KEY": true, "ARRMADA_HARDCOVER_API_KEY": true,
		"ARRMADA_PLEX_URL": true, "ARRMADA_PLEX_TOKEN": true,
		"ARRMADA_LIBRARY_DIR": true, "ARRMADA_DOWNLOADS_DIR": true, "ARRMADA_MOVIES_DIR": true,
		"ARRMADA_TV_DIR": true, "ARRMADA_EBOOKS_DIR": true, "ARRMADA_AUDIOBOOKS_DIR": true, "ARRMADA_MUSIC_DIR": true,
		"ARRMADA_SKIP_MIGRATION_SNAPSHOT": true, "ARRMADA_ALLOW_NEWER_SCHEMA": true,
	}
	for _, svc := range []string{"arrmada-app", "arrmada-qbittorrent"} {
		for k, mv := range main[svc].env {
			ev, ok := ex[svc].env[k]
			switch {
			case notInExample[k]:
				if ok {
					t.Errorf("%s: the example sets %s, which belongs in the app (or nowhere)", svc, k)
				}
			case !ok:
				t.Errorf("%s: docker-compose.yml sets %s but the example doesn't", svc, k)
			case ev != mv:
				t.Errorf("%s: %s is %q in the example, %q (default) in docker-compose.yml", svc, k, ev, mv)
			}
		}
		for k := range ex[svc].env {
			if _, ok := main[svc].env[k]; !ok {
				t.Errorf("%s: the example sets %s, which docker-compose.yml doesn't", svc, k)
			}
		}
	}

	// Mounts: the database at /data and nothing else there (media at /data once
	// clobbered a database), the media folder at /storage in both the app and
	// qBittorrent (one mount, so imports hardlink), and the shared managed /media.
	for _, svc := range []string{"arrmada-app", "arrmada-qbittorrent"} {
		targets := map[string]string{}
		for _, v := range ex[svc].volumes {
			targets[mountTarget(v)] = v
		}
		if targets["/storage"] == "" || targets["/media"] != "arrmada-media:/media" {
			t.Errorf("%s: the example should mount the media folder at /storage and arrmada-media at /media; has %v", svc, ex[svc].volumes)
		}
		for _, mv := range main[svc].volumes {
			if _, ok := targets[mountTarget(mv)]; !ok {
				t.Errorf("%s: docker-compose.yml mounts %s, the example doesn't", svc, mountTarget(mv))
			}
		}
	}
	if d := ex["arrmada-app"].volumes; len(d) == 0 || mountTarget(d[0]) != "/data" || strings.Contains(d[0], "media") {
		t.Errorf("arrmada-app: the example's first mount should be its own data folder at /data, got %v", d)
	}
	var appStorage string
	for _, v := range ex["arrmada-app"].volumes {
		if mountTarget(v) == "/storage" {
			appStorage = v
		}
	}
	for _, v := range ex["arrmada-qbittorrent"].volumes {
		if mountTarget(v) == "/storage" && v != appStorage {
			t.Errorf("qBittorrent mounts %q at /storage, the app %q: they must be the same folder", v, appStorage)
		}
	}

	// The same ports inside the containers (the host side is the user's to change).
	for _, svc := range []string{"arrmada-app", "arrmada-qbittorrent"} {
		inside := func(ps []string) map[string]bool {
			out := map[string]bool{}
			for _, p := range ps {
				out[p[strings.LastIndex(p, ":")+1:]] = true
			}
			return out
		}
		m, e := inside(main[svc].ports), inside(ex[svc].ports)
		for p := range m {
			if p == "8080" && svc == "arrmada-qbittorrent" {
				continue // qBittorrent's own web page: optional in the example
			}
			if !e[p] {
				t.Errorf("%s: docker-compose.yml publishes %s, the example doesn't", svc, p)
			}
		}
	}
}
