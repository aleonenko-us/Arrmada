package download

import (
	"context"
	"testing"
)

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
