// Package templates holds the Unraid template; this test keeps it to the owner's rules.
package templates

import (
	"encoding/xml"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/aio"
)

type container struct {
	Repository  string `xml:"Repository"`
	WebUI       string `xml:"WebUI"`
	Icon        string `xml:"Icon"`
	ExtraParams string `xml:"ExtraParams"`
	Configs     []struct {
		Name     string `xml:"Name,attr"`
		Target   string `xml:"Target,attr"`
		Default  string `xml:"Default,attr"`
		Mode     string `xml:"Mode,attr"`
		Type     string `xml:"Type,attr"`
		Display  string `xml:"Display,attr"`
		Required string `xml:"Required,attr"`
		Mask     string `xml:"Mask,attr"`
		Value    string `xml:",chardata"`
	} `xml:"Config"`
}

// The template runs the published stable all-in-one image, keeps the database in appdata
// at /data with nothing else there, brings media in through /storage, and carries no
// credential of any kind: keys are only ever entered in the app.
func TestUnraidTemplate(t *testing.T) {
	raw, err := os.ReadFile("arrmada.xml")
	if err != nil {
		t.Fatal(err)
	}
	var c container
	if err := xml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Repository != "ghcr.io/tristenlammi/arrmada:stable-aio" {
		t.Errorf("repository = %q", c.Repository)
	}
	paths := map[string]string{}
	vars := map[string]string{}
	values := map[string]string{}  // a variable's value as the template fills it in
	ports := map[string]string{}   // "8080/tcp" → host port
	hostPorts := map[string]string{} // "8085/tcp" (host side) → the entry's name
	for _, cfg := range c.Configs {
		up := strings.ToUpper(cfg.Target + " " + cfg.Name)
		for _, secret := range []string{"KEY", "TOKEN", "PASSWORD", "SECRET", "API_"} {
			if strings.Contains(up, secret) {
				t.Errorf("%q (%s) looks like a credential; keys are entered in the app, never in the template", cfg.Name, cfg.Target)
			}
		}
		if !oneOf(cfg.Display, "always", "always-hide", "advanced", "advanced-hide") || !oneOf(cfg.Required, "true", "false") || !oneOf(cfg.Mask, "true", "false") {
			t.Errorf("%q: Display %q, Required %q, Mask %q aren't values Unraid knows", cfg.Name, cfg.Display, cfg.Required, cfg.Mask)
		}
		switch cfg.Type {
		case "Path":
			paths[cfg.Target] = cfg.Default
			if strings.HasPrefix(cfg.Target, "/data/") {
				t.Errorf("%q is mounted under /data, the database folder", cfg.Name)
			}
		case "Variable":
			vars[cfg.Target] = cfg.Default
			values[cfg.Target] = cfg.Value
		case "Port":
			if cfg.Default == "" || cfg.Value != cfg.Default {
				t.Errorf("port %q: default %q, value %q", cfg.Name, cfg.Default, cfg.Value)
			}
			ports[cfg.Target+"/"+cfg.Mode] = cfg.Default
			// Every port is published whether or not what's behind it is switched on, so
			// two entries with one host port would stop the container from starting.
			if other, dup := hostPorts[cfg.Default+"/"+cfg.Mode]; dup {
				t.Errorf("ports %q and %q both default to host port %s/%s", other, cfg.Name, cfg.Default, cfg.Mode)
			}
			hostPorts[cfg.Default+"/"+cfg.Mode] = cfg.Name
		case "Device":
			// Unraid passes a blank Device as its Default, and Docker refuses to start a
			// container whose device doesn't exist: a server without a GPU couldn't run it.
			t.Errorf("%q is a Device entry; the GPU is added by hand (docs/unraid.md)", cfg.Name)
		default:
			t.Errorf("%q has type %q", cfg.Name, cfg.Type)
		}
	}
	if paths["/data"] != "/mnt/user/appdata/arrmada" {
		t.Errorf("/data defaults to %q, want appdata", paths["/data"])
	}
	if _, ok := paths["/storage"]; !ok {
		t.Error("no /storage mount for media and downloads")
	}
	// Unraid sets TZ to the server's own timezone; a template TZ would override it.
	if _, ok := vars["TZ"]; ok {
		t.Error("the template sets TZ, which would override the server's timezone")
	}
	if vars["PUID"] != "99" || vars["PGID"] != "100" {
		t.Errorf("runs as %s:%s, want Unraid's 99:100", vars["PUID"], vars["PGID"])
	}
	// The starting folders sit on the media share, never in the database folder or the
	// image's own /media (inside docker.img on Unraid).
	for _, v := range []string{"ARRMADA_LIBRARY_DIR", "ARRMADA_DOWNLOADS_DIR"} {
		if d := vars[v]; d != "/storage" && !strings.HasPrefix(d, "/storage/") {
			t.Errorf("%s = %q, want a folder under /storage", v, d)
		}
	}
	// The built-in companions: on by default and switchable, the torrent port the same
	// number on both sides, for TCP and UDP, and in the variable qBittorrent listens on
	// (it can't know a different host port), and the web UI link following its port.
	for _, v := range []string{"ARRMADA_BUNDLE_QBITTORRENT", "ARRMADA_BUNDLE_FLARESOLVERR"} {
		if d, ok := vars[v]; !ok || !strings.HasPrefix(d, "true") {
			t.Errorf("%s defaults to %q, want on", v, d)
		}
	}
	tp := vars["ARRMADA_QBIT_PORT"]
	if tp == "" || ports[tp+"/tcp"] != tp || ports[tp+"/udp"] != tp {
		t.Errorf("torrent port: ARRMADA_QBIT_PORT %q, ports %v", tp, ports)
	}
	if tp != strconv.Itoa(aio.DefaultTorrentPort) {
		t.Errorf("torrent port %s, but the image defaults to %d", tp, aio.DefaultTorrentPort)
	}
	if ui := ports[strconv.Itoa(aio.WebUIPort)+"/tcp"]; ui == "" || vars["ARRMADA_QBIT_WEBUI_HOST_PORT"] != ui {
		t.Errorf("qBittorrent web UI: port %q, ARRMADA_QBIT_WEBUI_HOST_PORT %q", ui, vars["ARRMADA_QBIT_WEBUI_HOST_PORT"])
	}
	// The opt-in companions: off unless switched on (false is both the value filled in
	// and the dropdown's first choice, which Unraid uses for a blank field), each with its
	// web UI published on the port the image listens on and the link following it.
	for _, oc := range []struct {
		sw, hostVar string
		port        int
	}{
		{"ARRMADA_BUNDLE_SABNZBD", "ARRMADA_SABNZBD_HOST_PORT", aio.SABnzbdPort},
		{"ARRMADA_BUNDLE_PROWLARR", "ARRMADA_PROWLARR_HOST_PORT", aio.ProwlarrPort},
	} {
		if d, ok := vars[oc.sw]; !ok || !strings.HasPrefix(d, "false") || values[oc.sw] != "false" {
			t.Errorf("%s defaults to %q (value %q), want off", oc.sw, d, values[oc.sw])
		}
		if ui := ports[strconv.Itoa(oc.port)+"/tcp"]; ui == "" || vars[oc.hostVar] != ui || values[oc.hostVar] != ui {
			t.Errorf("%s: port %d published on %q, %s %q", oc.sw, oc.port, ui, oc.hostVar, vars[oc.hostVar])
		}
	}
	// FlareSolverr listens inside the container only; no port of its own is published.
	if _, ok := ports[strconv.Itoa(aio.FlareSolverrPort)+"/tcp"]; ok {
		t.Error("the template publishes FlareSolverr's port")
	}
	if !strings.Contains(c.ExtraParams, "--stop-timeout=") {
		t.Errorf("ExtraParams %q: qBittorrent needs a stop timeout to save its state", c.ExtraParams)
	}
	if c.WebUI != "http://[IP]:[PORT:7878]/" {
		t.Errorf("WebUI = %q", c.WebUI)
	}
	if !strings.HasPrefix(c.Icon, "https://raw.githubusercontent.com/tristenlammi/Arrmada/main/") || !strings.HasSuffix(c.Icon, ".png") {
		t.Errorf("icon = %q, want a PNG in this repository", c.Icon)
	} else if _, err := os.Stat("../" + strings.TrimPrefix(c.Icon, "https://raw.githubusercontent.com/tristenlammi/Arrmada/main/")); err != nil {
		t.Errorf("icon %q isn't in the repository: %v", c.Icon, err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "ghp_") || strings.Contains(string(raw), "github_pat_") {
		t.Error("the template contains what looks like a GitHub token")
	}
}

// Community Apps won't take a repository without ca_profile.xml at its root and a
// non-empty <Profile>.
func TestCAProfile(t *testing.T) {
	raw, err := os.ReadFile("../ca_profile.xml")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		XMLName xml.Name `xml:"CommunityApplications"`
		Profile string   `xml:"Profile"`
	}
	if err := xml.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(p.Profile) == "" {
		t.Error("ca_profile.xml has no Profile")
	}
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
