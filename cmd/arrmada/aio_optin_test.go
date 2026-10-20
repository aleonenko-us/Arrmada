package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
)

// The all-in-one image's SABnzbd config is the one Arrmada reads its API key and page login
// from, as it does the Compose install's: what the supervisor seeds must parse here.
func TestAIOSeededSABnzbdConfigReads(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "sabnzbd", "sabnzbd.ini")
	if _, err := aio.SeedSABnzbd(aio.SABSeedOptions{Config: conf, Downloads: "/storage/downloads", Hostname: "abc123", UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatal(err)
	}
	key, err := download.ReadSABAPIKey(conf)
	if err != nil || len(key) != 32 {
		t.Fatalf("API key %q: %v", key, err)
	}
	user, pass, ok, err := download.ReadSABLogin(conf)
	if err != nil || !ok || user != "arrmada" || len(pass) != 36 {
		t.Fatalf("login %q/%d chars, ok %v: %v", user, len(pass), ok, err)
	}
}

// What the supervisor puts in Arrmada's environment is what Arrmada's config reads: the
// bundled SABnzbd's address and config while it's on, and where it would be either way
// (so its client can be switched off with it); the bundled Prowlarr as Arrmada's own only
// while it's on, and an address from the template winning over it.
func TestAIOEnvReachesConfig(t *testing.T) {
	for _, tc := range []struct {
		name               string
		o                  aio.Options
		template           map[string]string
		sab, sabConf       string
		prowlarr, aioProwl string
	}{
		{name: "both off", o: aio.Options{DataDir: "/data"}, sabConf: "/bundled/sabnzbd/sabnzbd.ini"},
		{name: "both on", o: aio.Options{DataDir: "/data", SABnzbd: true, Prowlarr: true},
			sab: aio.SABnzbdURL, sabConf: "/data/sabnzbd/sabnzbd.ini", prowlarr: aio.ProwlarrURL, aioProwl: aio.ProwlarrURL},
		{name: "on, the template's own addresses", o: aio.Options{DataDir: "/data", SABnzbd: true, Prowlarr: true},
			template: map[string]string{"ARRMADA_PROWLARR_URL": "http://10.0.0.9:9696", "ARRMADA_SABNZBD_URL": "http://10.0.0.9:8080"},
			sab:      aio.SABnzbdURL, sabConf: "/data/sabnzbd/sabnzbd.ini", prowlarr: "http://10.0.0.9:9696", aioProwl: aio.ProwlarrURL},
		{name: "off, the template's own addresses", o: aio.Options{DataDir: "/data"},
			template: map[string]string{"ARRMADA_PROWLARR_URL": "http://10.0.0.9:9696", "ARRMADA_SABNZBD_URL": "http://10.0.0.9:8080"},
			sabConf:  "/bundled/sabnzbd/sabnzbd.ini", prowlarr: "http://10.0.0.9:9696"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"ARRMADA_PROWLARR_URL", "ARRMADA_SABNZBD_URL", "ARRMADA_SABNZBD_CONFIG", "ARRMADA_AIO_PROWLARR_URL", "ARRMADA_AIO_SABNZBD_URL"} {
				t.Setenv(k, tc.template[k])
			}
			get := func(k string) string { return tc.template[k] }
			for _, kv := range aio.AppEnv(tc.o, get) {
				k, v, _ := strings.Cut(kv, "=")
				t.Setenv(k, v)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.SABnzbdURL != tc.sab || cfg.SABnzbdConfig != tc.sabConf || cfg.AIOSABnzbdURL != aio.SABnzbdURL {
				t.Errorf("SABnzbd: url %q, config %q, aio %q", cfg.SABnzbdURL, cfg.SABnzbdConfig, cfg.AIOSABnzbdURL)
			}
			if cfg.ProwlarrURL != tc.prowlarr || cfg.AIOProwlarrURL != tc.aioProwl {
				t.Errorf("Prowlarr: url %q, aio %q; want %q, %q", cfg.ProwlarrURL, cfg.AIOProwlarrURL, tc.prowlarr, tc.aioProwl)
			}
			// The switch main.go applies to the SABnzbd client.
			if on := cfg.SABnzbdURL == cfg.AIOSABnzbdURL; on != tc.o.SABnzbd {
				t.Errorf("the SABnzbd client would be switched on=%v, want %v", on, tc.o.SABnzbd)
			}
		})
	}
}
