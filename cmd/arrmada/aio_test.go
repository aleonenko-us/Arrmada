package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
