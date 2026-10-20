package download

import (
	"context"
	"testing"
)

// The all-in-one image's optional SABnzbd follows its switch the way its qBittorrent does:
// switched off in the container, its client is switched off (not removed) and gets no new
// downloads; switched back on, it's switched back on. Each kind keeps its own note, so one
// switch never wakes the other client, and the owner's own off and edits win.
func TestSyncBundledSwitchSABnzbd(t *testing.T) {
	svc, flags := newTestService(t)
	ctx := context.Background()
	const sabURL, qbURL = "http://127.0.0.1:8085", "http://127.0.0.1:8080"
	sab := func() Client {
		t.Helper()
		c, ok, err := svc.BundledClient(ctx, KindSABnzbd)
		if err != nil || !ok {
			t.Fatalf("bundled SABnzbd: %v, %v", ok, err)
		}
		return c
	}
	if _, err := svc.EnsureBundledSAB(ctx, sabURL, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureBundledLogin(ctx, qbURL, "admin", "pw"); err != nil {
		t.Fatal(err)
	}

	// On (or no switch change): nothing happens.
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, true); err != nil || !sab().Enabled {
		t.Fatalf("on: enabled %v, err %v", sab().Enabled, err)
	}

	// Off: the client is switched off and kept, and only SABnzbd's note is written.
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, false); err != nil {
		t.Fatal(err)
	}
	if sab().Enabled {
		t.Fatal("a switched-off SABnzbd's client is still on")
	}
	if flags.Get(ctx, switchedOffKey(KindSABnzbd), "") != "1" || flags.Get(ctx, KeyBundledSwitchedOff, "") != "" {
		t.Errorf("notes: sabnzbd %q, qbittorrent %q", flags.Get(ctx, switchedOffKey(KindSABnzbd), ""), flags.Get(ctx, KeyBundledSwitchedOff, ""))
	}
	if list, _ := svc.repo.ListEnabled(ctx); len(list) != 1 || list[0].Kind != KindQbittorrent {
		t.Errorf("enabled clients = %+v, want only qBittorrent", list)
	}
	// qBittorrent's switch, on, doesn't touch SABnzbd's note or client.
	if err := svc.SyncBundledSwitch(ctx, qbURL, true); err != nil || sab().Enabled {
		t.Fatalf("qBittorrent's switch woke SABnzbd (err %v)", err)
	}

	// On again: back on, note gone.
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, true); err != nil || !sab().Enabled {
		t.Fatalf("on again: enabled %v, err %v", sab().Enabled, err)
	}
	if flags.Get(ctx, switchedOffKey(KindSABnzbd), "") != "" {
		t.Error("the note outlived the switch")
	}

	// The owner switches it off in Arrmada: the container's switch doesn't undo that.
	c := sab()
	c.Enabled = false
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, true); err != nil || sab().Enabled {
		t.Errorf("the owner's off was undone (err %v)", err)
	}

	// Switched off by the container, then edited by the owner: their choice from then on.
	c = sab()
	c.Enabled = true
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, false); err != nil {
		t.Fatal(err)
	}
	c = sab()
	c.Name = "my SAB"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, switchedOffKey(KindSABnzbd), "") != "" {
		t.Error("the owner's edit left the switch's note")
	}

	// Pointed at the owner's own SABnzbd: the container's switch leaves it alone.
	c = sab()
	c.URL, c.Enabled = "http://192.168.1.10:8080", true
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, false); err != nil || !sab().Enabled {
		t.Errorf("a SABnzbd pointed elsewhere was switched off (err %v)", err)
	}

	// Deleted after the container switched it off: the note goes with the row.
	c.URL = sabURL
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncBundledSwitchOf(ctx, KindSABnzbd, sabURL, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, sab().ID); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, switchedOffKey(KindSABnzbd), "") != "" {
		t.Error("deleting the client left the switch's note")
	}
}
