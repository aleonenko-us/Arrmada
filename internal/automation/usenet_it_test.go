package automation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/download/nntptest"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// TestUsenetLifecycleLive runs a usenet grab's whole life against a real SABnzbd (the
// pinned linuxserver/sabnzbd image) and a fake news server in this process: the happy path
// from grab to import to SABnzbd's job archived and its folder gone, and a release whose
// articles the provider doesn't have from grab to blocklist and a new search. It needs the
// same settings as download's TestSABnzbdLive:
//
//	ARRMADA_SABNZBD_IT=1
//	ARRMADA_SABNZBD_URL        SABnzbd by an address it accepts (an IP: no hostname check)
//	ARRMADA_SABNZBD_KEY        its full API key (never printed)
//	ARRMADA_SABNZBD_NNTP_HOST  the name SABnzbd reaches this test process by
//	ARRMADA_SABNZBD_REMOTE     SABnzbd's downloads folder as SABnzbd names it …
//	ARRMADA_SABNZBD_LOCAL      … and as this process sees it (a shared volume)
//
// It changes the test SABnzbd: adds Arrmada's categories and a news server (removed after).
func TestUsenetLifecycleLive(t *testing.T) {
	if os.Getenv("ARRMADA_SABNZBD_IT") == "" {
		t.Skip("set ARRMADA_SABNZBD_IT=1 (and the ARRMADA_SABNZBD_* addresses) to run against a real SABnzbd")
	}
	key := os.Getenv("ARRMADA_SABNZBD_KEY")
	sabURL := os.Getenv("ARRMADA_SABNZBD_URL")
	remote, local := os.Getenv("ARRMADA_SABNZBD_REMOTE"), os.Getenv("ARRMADA_SABNZBD_LOCAL")
	if key == "" || sabURL == "" || remote == "" || local == "" {
		t.Fatal("ARRMADA_SABNZBD_URL, _KEY, _REMOTE and _LOCAL are required")
	}
	h := newLifecycleHarness(t)
	h.c.removeTorrent = nil // removals go to SABnzbd for real
	ctx, cancel := context.WithTimeout(h.ctx, 8*time.Minute)
	defer cancel()

	h.c.downloads.SetFolders(func(context.Context) download.PathEnv {
		return download.PathEnv{Downloads: local, DataDir: "/nonexistent-arrmada-data"}
	})
	sab, err := h.c.downloads.Create(ctx, download.Client{Name: "SABnzbd", Kind: download.KindSABnzbd, URL: sabURL, Password: key,
		Enabled: true, Priority: 1, PathMaps: download.PathMaps{{Remote: remote, Local: local}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.c.downloads.Test(ctx, sab.ID); err != nil {
		t.Fatalf("SABnzbd test: %s", scrubKey(err, key))
	}
	if _, err := h.c.downloads.AddMissingCategories(ctx, sab.ID); err != nil {
		t.Fatalf("categories: %s", scrubKey(err, key))
	}

	news := nntptest.Start(t, "0.0.0.0:0")
	sabCall(t, sabURL, key, "set_config", url.Values{"section": {"servers"}, "keyword": {"arrmada-u3-it"}, "name": {"arrmada-u3-it"},
		"host": {os.Getenv("ARRMADA_SABNZBD_NNTP_HOST")}, "port": {fmt.Sprint(news.Port())}, "connections": {"4"},
		"enable": {"1"}, "ssl": {"0"}, "retention": {"0"}})
	t.Cleanup(func() {
		sabCall(t, sabURL, key, "del_config", url.Values{"section": {"servers"}, "keyword": {"arrmada-u3-it"}})
	})

	if _, err := h.c.indexers.Create(ctx, indexer.Indexer{Name: "NZBIdx", Kind: indexer.KindNewznab, URL: "http://nzb.invalid", Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	nzbs := map[string][]byte{}
	h.c.fetchRelease = func(_ context.Context, name, link string) (indexer.FetchResult, error) {
		if b, ok := nzbs[link]; ok && name == "NZBIdx" {
			return indexer.FetchResult{File: b, Filename: "release.nzb"}, nil
		}
		return indexer.FetchResult{}, errors.New("no such NZB")
	}

	run := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	pass := func() {
		h.c.downloads.InvalidateSnapshot()
		if _, err := h.c.ReconcileAcquisitions(ctx); err != nil {
			t.Fatal(err)
		}
		h.c.DetectStalled(ctx)
		h.c.ImportSeriesDownloads(ctx)
	}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		for !done() {
			if time.Now().After(deadline) {
				q, _ := h.c.downloads.Queue(ctx)
				t.Fatalf("timed out waiting for %s; queue: %+v", what, q)
			}
			time.Sleep(time.Second)
			pass()
		}
	}

	// --- The happy path: grab → SABnzbd completes it → import → job archived, folder gone.
	good := "Show.S01E01.1080p.WEB-DL-ARR" + run
	payload := make([]byte, 52<<20) // above the importer's 50 MB sample floor
	for i := range payload {
		payload[i] = byte(i*31 + i/4093)
	}
	file := news.AddFile("good-"+run+"@arrmada.test", good+".mkv", payload, 768_000)
	nzbs["https://nzb.invalid/get?id=good"] = nntptest.NZBFiles(file)
	goodKey, err := h.c.grabTo(ctx, "NZBIdx", "https://nzb.invalid/get?id=good", good, seriesCategory)
	if err != nil || download.KeyTransport(goodKey) != download.TransportUsenet {
		t.Fatalf("grab = %q, %v", goodKey, scrubKey(err, key))
	}
	h.c.recordSeriesGrab(ctx, h.seriesID, good, "NZBIdx", "", goodKey)

	var leftover string
	waitFor("the good release to import", func() bool {
		if q, err := h.c.downloads.Queue(ctx); err == nil {
			for _, it := range q {
				if it.Hash == goodKey && it.ContentPath != "" {
					leftover = it.ContentPath
				}
			}
		}
		return episodeHasFile(t, h, 1, 1)
	})
	if g := grabRowOf(t, h.c, good); g.status != grabStatusImported || g.transport != "usenet" || g.seed {
		t.Errorf("imported grab = %+v", g)
	}
	if leftover == "" {
		t.Fatal("never saw where SABnzbd put the finished job")
	}
	h.c.downloads.InvalidateSnapshot()
	h.c.ManageSeeding(ctx)
	if g := grabRowOf(t, h.c, good); g.status != grabStatusSeeded {
		t.Errorf("after clean-up the grab = %+v, want out of the client", g)
	}
	h.c.downloads.InvalidateSnapshot()
	q, err := h.c.downloads.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range q {
		if it.Hash == goodKey {
			t.Errorf("the job is still in SABnzbd after clean-up: %+v", it)
		}
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("what SABnzbd left at %s is still there (%v)", leftover, err)
	}
	if !episodeHasFile(t, h, 1, 1) {
		t.Error("clean-up took the imported episode with it")
	}

	// --- Missing articles: SABnzbd fails it → blocklisted, job removed, a new search.
	bad := "Show.S01E02.1080p.WEB-DL-ARR" + run
	missing := nntptest.File{Name: bad + ".mkv"}
	for i := 1; i <= 3; i++ {
		missing.Segments = append(missing.Segments, nntptest.Segment{ID: fmt.Sprintf("missing-%s.%d@arrmada.test", run, i), Bytes: 780_000})
	}
	nzbs["https://nzb.invalid/get?id=bad"] = nntptest.NZBFiles(missing)
	searchesBefore := h.ix.searchCount()
	badKey, err := h.c.grabTo(ctx, "NZBIdx", "https://nzb.invalid/get?id=bad", bad, seriesCategory)
	if err != nil {
		t.Fatalf("grab: %v", scrubKey(err, key))
	}
	h.c.recordSeriesGrab(ctx, h.seriesID, bad, "NZBIdx", "", badKey)
	waitFor("the missing release to fail over", func() bool { return grabRowOf(t, h.c, bad).status == grabStatusFailed })

	var reason string
	if err := h.c.db.QueryRow(`SELECT reason FROM blocklist WHERE norm_title = ? AND media_type = 'series'`, normTitle(bad)).Scan(&reason); err != nil {
		t.Fatalf("the failed release isn't blocklisted: %v", err)
	}
	if reason != "usenet download failed: articles are missing from your provider" {
		t.Errorf("blocklist reason = %q", reason)
	}
	if h.ix.searchCount() <= searchesBefore {
		t.Error("no new search after the failure")
	}
	h.c.downloads.InvalidateSnapshot()
	if q, err := h.c.downloads.Queue(ctx); err == nil {
		for _, it := range q {
			if it.Hash == badKey {
				t.Errorf("the failed job is still in SABnzbd: %+v", it)
			}
		}
	}
	var events int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM series_events WHERE series_id = ? AND event = 'failed' AND detail LIKE ?`,
		h.seriesID, bad+" failed in SABnzbd (articles are missing from your provider) — blocklisted%").Scan(&events); err != nil || events != 1 {
		t.Errorf("history lines for the failure = %d (%v), want 1", events, err)
	}
}

// sabCall runs one SABnzbd API command for the test's own setup.
func sabCall(t *testing.T, base, key, mode string, params url.Values) {
	t.Helper()
	form := url.Values{}
	for k, v := range params {
		form[k] = v
	}
	form.Set("mode", mode)
	form.Set("output", "json")
	form.Set("apikey", key)
	resp, err := http.PostForm(strings.TrimRight(base, "/")+"/api", form)
	if err != nil {
		t.Fatalf("sabnzbd %s: %s", mode, scrubKey(err, key))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sabnzbd %s: HTTP %d %s", mode, resp.StatusCode, strings.ReplaceAll(string(body), key, "[key]"))
	}
}

// scrubKey keeps the API key out of anything the test prints.
func scrubKey(err error, key string) string {
	if err == nil {
		return "<nil>"
	}
	return strings.ReplaceAll(err.Error(), key, "[key]")
}
