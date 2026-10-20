package aio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func planNames(o Options) []string {
	var names []string
	for _, s := range Plan(o, nil, envOf(nil)) {
		names = append(names, s.Name)
	}
	return names
}

// A qBittorrent setup that fails must not keep SABnzbd from being seeded: SABnzbd started
// without its config would have no page login and none of Arrmada's categories. The
// qBittorrent that couldn't be set up is left out instead, and the log says why.
func TestPrepareQbittorrentFailureStillSeedsSABnzbd(t *testing.T) {
	data := t.TempDir()
	o := Options{UID: os.Getuid(), GID: os.Getgid(), DataDir: data, Downloads: "/storage/downloads",
		Qbittorrent: true, SABnzbd: true, Prowlarr: true}
	var ran []string
	got, notes := Prepare(o, Setup{
		Qbittorrent: func() error { ran = append(ran, "qbittorrent"); return errors.New("its config: disk full") },
		Prowlarr:    func() error { ran = append(ran, "prowlarr"); return nil },
		SABnzbd: func() error {
			ran = append(ran, "sabnzbd")
			_, err := SeedSABnzbd(SABSeedOptions{Config: o.SABnzbdConfig(), Downloads: o.Downloads, UID: o.UID, GID: o.GID})
			return err
		},
	})
	if strings.Join(ran, ",") != "qbittorrent,prowlarr,sabnzbd" {
		t.Fatalf("setups run: %v", ran)
	}
	if _, err := os.Stat(filepath.Join(data, "sabnzbd", "sabnzbd.ini")); err != nil {
		t.Fatalf("SABnzbd wasn't seeded: %v", err)
	}
	if got.Qbittorrent || !got.SABnzbd || !got.Prowlarr {
		t.Errorf("after setup: %+v", got)
	}
	if names := strings.Join(planNames(got), ","); names != "arrmada,prowlarr,sabnzbd" {
		t.Errorf("plan = %s", names)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "qBittorrent: its config: disk full; not starting it") {
		t.Errorf("notes = %q", notes)
	}
	// Arrmada is told it isn't running, so it stops sending to it.
	env := strings.Join(AppEnv(got, envOf(nil)), "\n")
	if !strings.Contains(env, "ARRMADA_QBITTORRENT_URL=\n") && !strings.HasSuffix(env, "ARRMADA_QBITTORRENT_URL=") {
		t.Errorf("Arrmada still registers qBittorrent:\n%s", env)
	}
}

// SABnzbd whose seed failed isn't started unseeded, and Arrmada isn't pointed at it.
func TestPrepareSABnzbdFailureLeavesItOut(t *testing.T) {
	o := Options{DataDir: "/data", Qbittorrent: true, SABnzbd: true}
	got, notes := Prepare(o, Setup{SABnzbd: func() error { return errors.New("its config: read-only file system") }})
	if got.SABnzbd || !got.Qbittorrent {
		t.Fatalf("after setup: %+v", got)
	}
	if names := strings.Join(planNames(got), ","); names != "arrmada,qbittorrent" {
		t.Errorf("plan = %s", names)
	}
	m := map[string]string{}
	for _, kv := range AppEnv(got, envOf(nil)) {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["ARRMADA_SABNZBD_URL"] != "" || m["ARRMADA_AIO_SABNZBD_URL"] != SABnzbdURL {
		t.Errorf("env = %v", m)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "SABnzbd: its config: read-only file system; not starting it") {
		t.Errorf("notes = %q", notes)
	}
}

// A warning (a folder that couldn't be handed to a new PUID) is logged and the companion
// still starts, as before; a failure to close appdata keeps FlareSolverr out, and its
// later step isn't run at all; switched-off companions' steps never run.
func TestPrepareWarningsAndFlareSolverr(t *testing.T) {
	o := Options{DataDir: "/data", Qbittorrent: true, FlareSolverr: true}
	homeRan, sabRan := false, false
	got, notes := Prepare(o, Setup{
		CloseData:    func() error { return errors.New("closing /data to its user: operation not permitted") },
		Qbittorrent:  func() error { return Warning(errors.New("its folder: lchown failed")) },
		SABnzbd:      func() error { sabRan = true; return nil },
		FlareSolverr: func() error { homeRan = true; return nil },
	})
	if !got.Qbittorrent || got.FlareSolverr || got.SABnzbd {
		t.Fatalf("after setup: %+v", got)
	}
	if homeRan || sabRan {
		t.Errorf("ran a step it shouldn't have: FlareSolverr home %v, SABnzbd (switched off) %v", homeRan, sabRan)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "qBittorrent: its folder: lchown failed (starting it anyway)") ||
		!strings.Contains(joined, "FlareSolverr: closing /data to its user: operation not permitted; not starting it") {
		t.Errorf("notes = %q", notes)
	}
	if names := strings.Join(planNames(got), ","); names != "arrmada,qbittorrent" {
		t.Errorf("plan = %s", names)
	}
	if Warning(nil) != nil {
		t.Error("Warning(nil) isn't nil")
	}
	// Everything fine: nothing changes, nothing is said.
	if got, notes := Prepare(o, Setup{}); got != o || len(notes) != 0 {
		t.Errorf("an empty setup changed %+v / said %q", got, notes)
	}
}
