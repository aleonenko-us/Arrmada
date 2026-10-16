package download

import (
	"context"
	"testing"
)

// The all-in-one image's qBittorrent is registered with the login made for it. A new login
// (a fresh qBittorrent config) reaches the client; one the owner typed on the card isn't
// overwritten at the next start; a client pointed elsewhere keeps its own; a removed one
// stays removed until restored, and comes back with the login.
func TestEnsureBundledLogin(t *testing.T) {
	svc, flags := newTestService(t)
	ctx := context.Background()
	const url = "http://127.0.0.1:8080"
	bundled := func() Client {
		t.Helper()
		c, ok, err := svc.BundledClient(ctx, KindQbittorrent)
		if err != nil || !ok {
			t.Fatalf("bundled client: %v, %v", ok, err)
		}
		return c
	}
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "first"); err != nil {
		t.Fatal(err)
	}
	if c := bundled(); c.Username != "admin" || c.Password != "first" || !c.Enabled || c.URL != url {
		t.Fatalf("registered %+v", c)
	}
	if flags.Get(ctx, KeyBundledLogin, "") == "" {
		t.Error("the login given wasn't recorded")
	}

	// The owner types the password they set in qBittorrent: a restart keeps it.
	c := bundled()
	c.Password = "mine"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "first"); err != nil || bundled().Password != "mine" {
		t.Fatalf("the owner's password was overwritten (err %v)", err)
	}

	// A fresh qBittorrent config comes with a new login: the client gets it.
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "second"); err != nil || bundled().Password != "second" {
		t.Fatalf("new login not applied (err %v): %+v", err, bundled())
	}

	// Pointed at another qBittorrent: left alone.
	c = bundled()
	c.URL, c.Password = "http://192.168.1.10:8080", "theirs"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "third"); err != nil || bundled().Password != "theirs" {
		t.Fatalf("a client pointed elsewhere got the login (err %v)", err)
	}

	// Removed: not added back at the next start; restored: back, with the login.
	if err := svc.Delete(ctx, bundled().ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "third"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.BundledClient(ctx, KindQbittorrent); ok {
		t.Fatal("a removed bundled client came back by itself")
	}
	if err := svc.RestoreBundledLogin(ctx, url, "admin", "third"); err != nil {
		t.Fatal(err)
	}
	if c := bundled(); c.Username != "admin" || c.Password != "third" || c.URL != url {
		t.Errorf("restored %+v", c)
	}
}

// The all-in-one image's switch for its built-in qBittorrent: off switches the bundled
// client off (kept, with its torrents), on switches it back on — but only a client the
// switch turned off, never one the owner turned off in Arrmada or pointed elsewhere.
func TestSyncBundledSwitch(t *testing.T) {
	svc, flags := newTestService(t)
	ctx := context.Background()
	const url = "http://127.0.0.1:8080"
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil { // no client yet: nothing to do
		t.Fatal(err)
	}
	if err := svc.EnsureBundled(ctx, url); err != nil {
		t.Fatal(err)
	}
	bundled := func() Client {
		t.Helper()
		c, ok, err := svc.BundledClient(ctx, KindQbittorrent)
		if err != nil || !ok {
			t.Fatalf("bundled client: %v, %v", ok, err)
		}
		return c
	}

	// Switched off in the container: the client follows, and stays registered.
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil {
		t.Fatal(err)
	}
	if c := bundled(); c.Enabled {
		t.Error("client still on with its qBittorrent switched off")
	}
	if flags.Get(ctx, KeyBundledSwitchedOff, "") != "1" {
		t.Error("the switch-off wasn't recorded")
	}
	// A second start, still off: nothing changes.
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil || bundled().Enabled {
		t.Fatalf("second off start: %v", err)
	}

	// Back on: the client comes back on and the record is cleared.
	if err := svc.SyncBundledSwitch(ctx, url, true); err != nil {
		t.Fatal(err)
	}
	if c := bundled(); !c.Enabled {
		t.Error("client still off with its qBittorrent switched back on")
	}
	if flags.Get(ctx, KeyBundledSwitchedOff, "") != "" {
		t.Error("the switch-off record wasn't cleared")
	}

	// The owner switches it off in Arrmada: switching qBittorrent on doesn't override that.
	c := bundled()
	c.Enabled = false
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitch(ctx, url, true); err != nil || bundled().Enabled {
		t.Errorf("the owner's off was overridden (err %v)", err)
	}

	// Pointed at another qBittorrent: the container's switch no longer applies to it.
	c = bundled()
	c.Enabled, c.URL = true, "http://192.168.1.10:8080"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil || !bundled().Enabled {
		t.Errorf("a client pointing elsewhere was switched off (err %v)", err)
	}
}
