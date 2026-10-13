// Package templates holds the Unraid template; this test keeps it to the owner's rules.
package templates

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

type container struct {
	Repository string `xml:"Repository"`
	Configs    []struct {
		Name    string `xml:"Name,attr"`
		Target  string `xml:"Target,attr"`
		Default string `xml:"Default,attr"`
		Type    string `xml:"Type,attr"`
		Value   string `xml:",chardata"`
	} `xml:"Config"`
}

// The template runs the published stable image, keeps the database in appdata at /data
// with nothing else there, brings media in through /storage, and carries no credential
// of any kind: keys are only ever entered in the app.
func TestUnraidTemplate(t *testing.T) {
	raw, err := os.ReadFile("arrmada.xml")
	if err != nil {
		t.Fatal(err)
	}
	var c container
	if err := xml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Repository != "ghcr.io/tristenlammi/arrmada:stable" {
		t.Errorf("repository = %q", c.Repository)
	}
	paths := map[string]string{}
	for _, cfg := range c.Configs {
		up := strings.ToUpper(cfg.Target + " " + cfg.Name)
		for _, secret := range []string{"KEY", "TOKEN", "PASSWORD", "SECRET", "API_"} {
			if strings.Contains(up, secret) {
				t.Errorf("%q (%s) looks like a credential; keys are entered in the app, never in the template", cfg.Name, cfg.Target)
			}
		}
		if cfg.Type != "Path" {
			continue
		}
		paths[cfg.Target] = cfg.Default
		if strings.HasPrefix(cfg.Target, "/data/") {
			t.Errorf("%q is mounted under /data, the database folder", cfg.Name)
		}
	}
	if paths["/data"] != "/mnt/user/appdata/arrmada" {
		t.Errorf("/data defaults to %q, want appdata", paths["/data"])
	}
	if _, ok := paths["/storage"]; !ok {
		t.Error("no /storage mount for media and downloads")
	}
	if strings.Contains(strings.ToLower(string(raw)), "ghp_") || strings.Contains(string(raw), "github_pat_") {
		t.Error("the template contains what looks like a GitHub token")
	}
}
