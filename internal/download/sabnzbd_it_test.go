package download

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download/nntptest"
)

// TestSABnzbdLive runs the SABnzbd client against a real SABnzbd (the pinned
// linuxserver/sabnzbd image) and a fake news server in this process. It needs:
//
//	ARRMADA_SABNZBD_IT=1
//	ARRMADA_SABNZBD_URL        SABnzbd by an address it accepts (an IP: no hostname check)
//	ARRMADA_SABNZBD_KEY        its full API key (never printed)
//	ARRMADA_SABNZBD_NNTP_HOST  the name SABnzbd reaches this test process by
//	ARRMADA_SABNZBD_REMOTE     SABnzbd's downloads folder as SABnzbd names it …
//	ARRMADA_SABNZBD_LOCAL      … and as this process sees it (a shared volume)
//	ARRMADA_SABNZBD_HOSTNAME_URL  optional: SABnzbd by a name it doesn't whitelist
//	ARRMADA_SABNZBD_CAPTURE    optional: a folder to write the raw queue/history answers to
//
// It changes the test SABnzbd: adds Arrmada's categories and a news server (removed after).
func TestSABnzbdLive(t *testing.T) {
	if os.Getenv("ARRMADA_SABNZBD_IT") == "" {
		t.Skip("set ARRMADA_SABNZBD_IT=1 (and the ARRMADA_SABNZBD_* addresses) to run against a real SABnzbd")
	}
	key := os.Getenv("ARRMADA_SABNZBD_KEY")
	remote, local := os.Getenv("ARRMADA_SABNZBD_REMOTE"), os.Getenv("ARRMADA_SABNZBD_LOCAL")
	if key == "" || os.Getenv("ARRMADA_SABNZBD_URL") == "" || remote == "" || local == "" {
		t.Fatal("ARRMADA_SABNZBD_URL, _KEY, _REMOTE and _LOCAL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dc := Client{ID: 1, Name: "SABnzbd", Kind: KindSABnzbd, URL: os.Getenv("ARRMADA_SABNZBD_URL"), Password: key,
		PathMaps: PathMaps{{Remote: remote, Local: local}}}
	sab := NewSABnzbd()
	sab.SetFolders(func(context.Context) PathEnv { return PathEnv{Downloads: local, DataDir: "/nonexistent-arrmada-data"} })
	noKey := func(err error) {
		t.Helper()
		if err != nil && strings.Contains(err.Error(), key) {
			t.Fatalf("an error carries the API key")
		}
	}

	// The plain-language failures first.
	if hu := os.Getenv("ARRMADA_SABNZBD_HOSTNAME_URL"); hu != "" {
		bad := dc
		bad.URL = hu
		err := sab.Test(ctx, bad)
		noKey(err)
		u, _ := url.Parse(hu)
		if err == nil || !strings.Contains(err.Error(), "host_whitelist") || !strings.Contains(err.Error(), u.Hostname()) {
			t.Errorf("Test by an unlisted hostname = %v, want the host_whitelist fix", err)
		}
	}
	wrong := dc
	wrong.Password = "0123456789abcdef0123456789abcdef"
	if err := sab.Test(ctx, wrong); !errors.Is(err, errSABKey) {
		t.Errorf("Test with a wrong key = %v, want the API key message", err)
	}
	if err := sab.Test(ctx, dc); err != nil {
		t.Fatalf("Test: %v", err)
	}
	ver, err := sab.Version(ctx, dc)
	if err != nil || ver == "" {
		t.Fatalf("Version = %q, %v", ver, err)
	}
	t.Logf("SABnzbd %s", ver)

	// Categories: created only when asked, then present.
	created, err := sab.AddMissingCategories(ctx, dc)
	if err != nil {
		t.Fatalf("AddMissingCategories: %v", err)
	}
	t.Logf("created categories %v", created)
	cats, err := sab.Categories(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range sab.categories() {
		if !hasCategory(cats, c.name) {
			t.Errorf("category %q missing after AddMissingCategories (have %v)", c.name, cats)
		}
	}

	// A news server in this process: one article it has, one it hasn't.
	news := nntptest.Start(t, "0.0.0.0:0")
	if _, err := sab.call(ctx, dc, "set_config", url.Values{"section": {"servers"}, "keyword": {"arrmada-it"}, "name": {"arrmada-it"},
		"host": {os.Getenv("ARRMADA_SABNZBD_NNTP_HOST")}, "port": {fmt.Sprint(news.Port())}, "connections": {"2"},
		"enable": {"1"}, "ssl": {"0"}, "retention": {"0"}}); err != nil {
		t.Fatalf("adding the test news server: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sab.call(context.Background(), dc, "del_config", url.Values{"section": {"servers"}, "keyword": {"arrmada-it"}})
	})
	facts, err := sab.Facts(ctx, dc)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if len(facts.Missing) != 0 || facts.ServersConfigured == 0 || facts.Version != ver || !facts.CompleteFolder.Visible {
		t.Errorf("Facts = %+v", facts)
	}
	for _, s := range facts.Servers {
		noKey(errors.New(s.Error))
	}

	run := fmt.Sprint(time.Now().UnixNano())
	goodName, badName := "Arrmada.IT.Good."+run, "Arrmada.IT.Missing."+run
	payload := make([]byte, 200_000)
	for i := range payload {
		payload[i] = byte(i*7 + i/251)
	}
	news.Add("good-"+run+"@arrmada.test", "arrmada.it."+run+".mkv", payload)
	good := nntptest.NZB("", map[string]string{"arrmada.it." + run + ".mkv": "good-" + run + "@arrmada.test"}, map[string]int{"arrmada.it." + run + ".mkv": 270_000})
	bad := nntptest.NZB("s3cret", map[string]string{"missing." + run + ".mkv": "missing-" + run + "@arrmada.test"}, map[string]int{"missing." + run + ".mkv": 270_000})

	// A category SABnzbd doesn't have is refused, not filed under *.
	if _, err := sab.AddKeyed(ctx, dc, AddRequest{Name: "Arrmada.IT.NoCat." + run, File: good, Filename: "x.nzb", Category: "arrmada-no-such-" + run, Transport: TransportUsenet}); err == nil || !strings.Contains(err.Error(), "category") {
		t.Errorf("add under an unknown category = %v, want a refusal", err)
	}

	// Held first, so the failing job is seen in the queue before it fails.
	if err := sab.Pause(ctx, dc, PauseAll); err != nil {
		t.Fatalf("pause all: %v", err)
	}
	badKey, err := sab.AddKeyed(ctx, dc, AddRequest{Name: badName, File: bad, Filename: badName + ".nzb", Category: CategoryTV, Transport: TransportUsenet})
	if err != nil || KeyTransport(badKey) != TransportUsenet {
		t.Fatalf("add (missing) = %q, %v", badKey, err)
	}
	items, err := sab.List(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if it, ok := findItem(items, badKey); !ok || it.State != "paused" || it.Phase() != "paused" || it.Category != CategoryTV {
		t.Errorf("queued job while paused = %+v (found %v)", it, ok)
	}
	capture(t, sab, dc, "queue_paused")
	if err := sab.Resume(ctx, dc, PauseAll); err != nil {
		t.Fatalf("resume all: %v", err)
	}
	goodKey, err := sab.AddKeyed(ctx, dc, AddRequest{Name: goodName, File: good, Filename: goodName + ".nzb", Category: DefaultMovieCategory, Transport: TransportUsenet})
	if err != nil || KeyTransport(goodKey) != TransportUsenet {
		t.Fatalf("add (good) = %q, %v", goodKey, err)
	}

	var goodItem, badItem Item
	deadline := time.Now().Add(3 * time.Minute)
	for {
		items, err := sab.List(ctx, dc)
		if err != nil {
			t.Fatal(err)
		}
		g, gok := findItem(items, goodKey)
		b, bok := findItem(items, badKey)
		if gok && bok && g.State == StateCompleted && b.State == "error" {
			goodItem, badItem = g, b
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs didn't finish: good %+v (%v), missing %+v (%v)", g, gok, b, bok)
		}
		time.Sleep(500 * time.Millisecond)
	}
	capture(t, sab, dc, "history_done")
	if badItem.FailMessage == "" || badItem.Complete() || badItem.Phase() != "error" {
		t.Errorf("failed job = %+v", badItem)
	}
	t.Logf("fail_message: %q", badItem.FailMessage)
	if !goodItem.Complete() || goodItem.ContentPath == "" {
		t.Fatalf("finished job = %+v", goodItem)
	}
	// SABnzbd names the job's folder, or for a job of one file that file inside it.
	leftover := dc.PathMaps.ToLocal(goodItem.ContentPath)
	fi, err := os.Stat(leftover)
	if err != nil {
		t.Fatalf("nothing where SABnzbd said the job is (%s): %v", leftover, err)
	}
	folder := leftover
	if !fi.IsDir() {
		folder = filepath.Dir(leftover)
	}
	if _, err := os.Stat(filepath.Join(folder, "arrmada.it."+run+".mkv")); err != nil {
		t.Fatalf("the finished file isn't in %s: %v", folder, err)
	}

	// Retry is only for the failed one; the finished one says so.
	if err := sab.TorrentAction(ctx, dc, goodKey, ActionRetry); err == nil {
		t.Error("retry of a finished job was accepted")
	}

	// Removing the finished job archives it and deletes its folder (inside the mapped root).
	if err := sab.Remove(ctx, dc, goodKey, true); err != nil {
		t.Fatalf("remove finished: %v", err)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the finished job's folder is still there: %v", err)
	}
	if err := sab.Remove(ctx, dc, badKey, true); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	items, err = sab.List(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{goodKey, badKey} {
		if _, ok := findItem(items, k); ok {
			t.Errorf("%s still listed after removal", k)
		}
	}
	// Removing again is no error: it's already gone.
	if err := sab.Remove(ctx, dc, goodKey, true); err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func findItem(items []Item, key string) (Item, bool) {
	for _, it := range items {
		if it.Hash == key {
			return it, true
		}
	}
	return Item{}, false
}

// capture writes the raw queue and history answers when ARRMADA_SABNZBD_CAPTURE names a
// folder: the recorded fixtures under testdata/sabnzbd came from here. The key is scrubbed.
func capture(t *testing.T, sab *SABnzbd, dc Client, label string) {
	dir := os.Getenv("ARRMADA_SABNZBD_CAPTURE")
	if dir == "" {
		return
	}
	for _, mode := range []string{"queue", "history"} {
		body, err := sab.call(context.Background(), dc, mode, nil)
		if err != nil {
			t.Logf("capture %s: %v", mode, err)
			continue
		}
		name := filepath.Join(dir, label+"_"+mode+".json")
		if err := os.WriteFile(name, []byte(scrub(dc, string(body))), 0o644); err != nil {
			t.Logf("capture: %v", err)
		}
	}
}
