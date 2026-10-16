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

// Once the owner edits or deletes the bundled client, the switch's note is gone: a client
// they switched off themselves is never switched back on by the container, and a stale
// note (no row, or the row pointed elsewhere) is dropped rather than kept for later.
func TestSyncBundledSwitchNoteDoesntOutliveTheOwnersChoice(t *testing.T) {
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
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "pw"); err != nil {
		t.Fatal(err)
	}

	// Switched off by the container, then edited by the owner (still off): on again in
	// the container, it stays off.
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil {
		t.Fatal(err)
	}
	c := bundled()
	c.Name = "my qB"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, KeyBundledSwitchedOff, "") != "" {
		t.Error("the owner's edit left the switch's note")
	}
	if err := svc.SyncBundledSwitch(ctx, url, true); err != nil || bundled().Enabled {
		t.Errorf("the owner's off was undone (err %v)", err)
	}

	// Switched off by the container, then deleted by the owner: the note goes with it,
	// and a restored client starts switched on without help from it.
	c = bundled()
	c.Enabled = true
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitch(ctx, url, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, bundled().ID); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, KeyBundledSwitchedOff, "") != "" {
		t.Error("deleting the client left the switch's note")
	}

	// A note with nothing behind it (written by an older start, say) is dropped when the
	// switch is on.
	_ = flags.Set(ctx, KeyBundledSwitchedOff, "1")
	if err := svc.SyncBundledSwitch(ctx, url, true); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, KeyBundledSwitchedOff, "") != "" {
		t.Error("a stale note survived")
	}
}

// The all-in-one image's qBittorrent answers at 127.0.0.1:8080, which with host
// networking can also be the owner's own qBittorrent. A row of theirs there with another
// login is never taken over: the bundled one is added beside it.
func TestEnsureBundledLoginLeavesTheOwnersClientAlone(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	const url = "http://127.0.0.1:8080"
	mine, err := svc.Create(ctx, Client{Name: "my qBittorrent", Kind: KindQbittorrent, URL: url, Username: "me", Password: "secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBundledLogin(ctx, url, "admin", "generated"); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.List(ctx)
	if len(all) != 2 {
		t.Fatalf("clients = %+v", all)
	}
	for _, c := range all {
		switch c.ID {
		case mine.ID:
			if c.Bundled || c.Username != "me" || c.Password != "secret" || c.Name != "my qBittorrent" {
				t.Errorf("the owner's client was changed: %+v", c)
			}
		default:
			if !c.Bundled || c.Username != "admin" || c.Password != "generated" {
				t.Errorf("bundled client = %+v", c)
			}
		}
	}

	// A row that already has the login (an install whose row predates the flag) is still
	// adopted rather than doubled.
	svc2, _ := newTestService(t)
	old, _ := svc2.Create(ctx, Client{Name: "qBittorrent (bundled)", Kind: KindQbittorrent, URL: url, Username: "admin", Password: "generated", Enabled: true})
	if err := svc2.EnsureBundledLogin(ctx, url, "admin", "generated"); err != nil {
		t.Fatal(err)
	}
	if all, _ := svc2.List(ctx); len(all) != 1 || !all[0].Bundled || all[0].ID != old.ID {
		t.Errorf("same-login row not adopted: %+v", all)
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
