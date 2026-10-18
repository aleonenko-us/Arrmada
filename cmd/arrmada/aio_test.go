package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/aio"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Only the Compose install's own qBittorrent container is ever given a password by Arrmada.
func TestIsComposeBundleQbit(t *testing.T) {
	for _, tc := range []struct {
		cfg  config.Config
		want bool
	}{
		{config.Config{QbittorrentURL: "http://arrmada-qbittorrent:8080"}, true},
		{config.Config{QbittorrentURL: "http://arrmada-qbittorrent:8080/"}, true},
		// Its container name, as someone may have typed it, in any case.
		{config.Config{QbittorrentURL: "http://Arrmada-qbittorrent:8080"}, true},
		{config.Config{QbittorrentURL: "http://ARRMADA-QBITTORRENT:8080"}, true},
		// The owner's own, someone else's stack, no bundle at all, and the all-in-one image's.
		{config.Config{QbittorrentURL: "http://192.168.1.20:8080"}, false},
		{config.Config{QbittorrentURL: "http://qbittorrent:8080"}, false},
		{config.Config{QbittorrentURL: ""}, false},
		{config.Config{QbittorrentURL: "http://127.0.0.1:8080", AIOQbittorrentURL: "http://127.0.0.1:8080"}, false},
	} {
		if got := isComposeBundleQbit(tc.cfg); got != tc.want {
			t.Errorf("isComposeBundleQbit(%q) = %v, want %v", tc.cfg.QbittorrentURL, got, tc.want)
		}
	}
}

// The lock keeps a second process out while it's held, is free again once released, and a
// stale one (a crash) is taken over.
func TestLockFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "qbittorrent-login.lock")
	release, ok, err := lockFile(p, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first: %v %v", ok, err)
	}
	if _, ok, err := lockFile(p, time.Minute); err != nil || ok {
		t.Fatalf("second while held: ok %v, err %v", ok, err)
	}
	release()
	release, ok, err = lockFile(p, time.Minute)
	if err != nil || !ok {
		t.Fatalf("after release: %v %v", ok, err)
	}
	// Left behind by a crash an hour ago: taken over.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	release2, ok, err := lockFile(p, time.Minute)
	if err != nil || !ok {
		t.Fatalf("stale lock not taken over: %v %v", ok, err)
	}
	release() // the old holder's release after a takeover leaves the new holder's lock
	if _, ok, _ := lockFile(p, time.Minute); ok {
		t.Fatal("the old holder's release removed the new holder's lock")
	}
	release2()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("the lock is still there after its holder released it")
	}
}

// Two processes at once: the second leaves the switch to the first and changes nothing.
func TestSecureBundledQbitLocked(t *testing.T) {
	dir := t.TempDir()
	release, ok, err := lockFile(filepath.Join(dir, "qbittorrent-login.lock"), time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	defer release()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	downloads := download.NewService(st.DB(), log)
	secureBundledQbit(context.Background(), log, downloads, config.Config{DataDir: dir, QbittorrentURL: "http://arrmada-qbittorrent:8080", QbittorrentLogin: filepath.Join(dir, "qbittorrent-login.json")})
	if !strings.Contains(buf.String(), "another Arrmada process") {
		t.Errorf("log: %s", buf.String())
	}
}

// The login file: none before the switch, made and saved once, read back after.
func TestLoginFile(t *testing.T) {
	f := loginFile{filepath.Join(t.TempDir(), "qbittorrent-login.json")}
	if _, _, ok, err := f.Load(); ok || err != nil {
		t.Fatalf("before: ok %v, err %v", ok, err)
	}
	pass, err := f.Make("admin")
	if err != nil || len(pass) < 20 {
		t.Fatalf("Make = %q, %v", pass, err)
	}
	user, got, ok, err := f.Load()
	if !ok || err != nil || user != "admin" || got != pass {
		t.Fatalf("after: %q %v %v", user, ok, err)
	}
	// A broken file is an error, never "none" (which would make a new password and leave
	// qBittorrent's real one unknown).
	if err := os.WriteFile(f.path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.Load(); err == nil {
		t.Error("a broken login file read as no login")
	}
	// reopen's marker, beside the login: the same name update.sh writes.
	if f.Reopened() != download.ReopenNone {
		t.Error("reopened with no marker")
	}
	marker := filepath.Join(filepath.Dir(f.path), "qbittorrent-reopened")
	if err := os.WriteFile(marker, nil, 0o644); err != nil { // update.sh's touch
		t.Fatal(err)
	}
	if f.Reopened() != download.ReopenRollback {
		t.Error("update.sh's marker wasn't seen")
	}
	if err := os.WriteFile(marker, []byte("reset\n"), 0o644); err != nil { // troubleshooting
		t.Fatal(err)
	}
	if f.Reopened() != download.ReopenReset {
		t.Error("the reset marker wasn't seen")
	}
	if err := f.ClearReopened(); err != nil || f.Reopened() != download.ReopenNone {
		t.Errorf("clear: %v", err)
	}
	if err := f.ClearReopened(); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

// At boot the Compose bundle is registered without a login until Arrmada has made one,
// and with it after.
func TestEnsureBundledQbitCompose(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	downloads := download.NewService(st.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := config.Config{QbittorrentURL: "http://arrmada-qbittorrent:8080", QbittorrentLogin: filepath.Join(t.TempDir(), "qbittorrent-login.json")}
	if err := ensureBundledQbit(ctx, downloads, cfg); err != nil {
		t.Fatal(err)
	}
	c, ok, _ := downloads.BundledClient(ctx, download.KindQbittorrent)
	if !ok || c.Password != "" {
		t.Fatalf("before the switch: %+v %v", c, ok)
	}
	if err := aio.SaveLogin(cfg.QbittorrentLogin, aio.Login{Username: "admin", Password: "made-by-arrmada-xxxxxxx"}); err != nil {
		t.Fatal(err)
	}
	if err := ensureBundledQbit(ctx, downloads, cfg); err != nil {
		t.Fatal(err)
	}
	if c, _, _ = downloads.BundledClient(ctx, download.KindQbittorrent); c.Password != "made-by-arrmada-xxxxxxx" {
		t.Errorf("after: the client didn't get the saved login")
	}

	// The all-in-one image without its login is an error, as before.
	aioCfg := config.Config{QbittorrentURL: "http://127.0.0.1:8080", AIOQbittorrentURL: "http://127.0.0.1:8080", QbittorrentLogin: filepath.Join(t.TempDir(), "missing.json")}
	if err := ensureBundledQbit(ctx, downloads, aioCfg); err == nil {
		t.Error("the all-in-one image's missing login wasn't reported")
	}
}
